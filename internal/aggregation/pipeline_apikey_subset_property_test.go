package aggregation

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

var p8NamePool = []string{"alpha", "beta", "gamma", "delta", "search", "read", "write", "list_dir", "x", "ab", "abc"}
var p8RegexPool = []string{".*", "a.*", "[a-z_]+", `\w+`, "(alpha|beta)", "search", "read.*", "ab?c", "gamma|delta", "[a-z]{1,3}"}
var p8TargetNames = []string{"", "renamed_pub", "exposed_tool", "friendly_name"}
var p8TargetDescs = []string{"", "新对外描述 A", "新对外描述 B"}

func p8GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.SampledFrom(p8NamePool).Draw(t, "originalName")
		return domain.ToolDef{OriginalName: name, Name: name, Description: "原始描述", InputSchema: []byte("{}")}
	})
}

func p8GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(rapid.SampledFrom(p8RegexPool), rapid.String()).Draw(t, "filterRegexPattern")
		} else {
			pattern = rapid.OneOf(rapid.SampledFrom(p8NamePool), rapid.String()).Draw(t, "filterExactPattern")
		}
		return domain.FilterRule{Pattern: pattern, IsRegex: isRegex, Enabled: rapid.Bool().Draw(t, "filterEnabled")}
	})
}

func p8GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(rapid.SampledFrom(p8RegexPool), rapid.String()).Draw(t, "aliasRegexPattern")
		} else {
			pattern = rapid.OneOf(rapid.SampledFrom(p8NamePool), rapid.String()).Draw(t, "aliasExactPattern")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(p8TargetNames).Draw(t, "aliasTargetName"),
			TargetDesc: rapid.SampledFrom(p8TargetDescs).Draw(t, "aliasTargetDesc"),
			SortOrder:  rapid.IntRange(0, 4).Draw(t, "aliasSortOrder"),
		}
	})
}

func p8GenBundles() *rapid.Generator[[]upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) []upstreamBundle {
		n := rapid.IntRange(1, 3).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := range n {
			bundles[i] = upstreamBundle{
				upstreamID:   fmt.Sprintf("u%d", i),
				upstreamName: fmt.Sprintf("上游-%d", i),
				sortOrder:    rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder_%d", i)),
				tools:        rapid.SliceOfN(p8GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools_%d", i)),
				aliases:      rapid.SliceOfN(p8GenAlias(), 0, 4).Draw(t, fmt.Sprintf("aliases_%d", i)),
				mcpFilters:   rapid.SliceOfN(p8GenFilter(), 0, 4).Draw(t, fmt.Sprintf("filters_%d", i)),
			}
		}
		return bundles
	})
}

func p8RefMatch(pattern string, isRegex bool, originalName string) bool {
	if !isRegex {
		return pattern == originalName
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return false
	}
	return re.MatchString(originalName)
}

func p8MatchedByEnabled(originalName string, filters []domain.FilterRule) bool {
	for _, f := range filters {
		if f.Enabled && p8RefMatch(f.Pattern, f.IsRegex, originalName) {
			return true
		}
	}
	return false
}

func TestProperty8APIKeyFiltersSourcesBeforeGrouping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		engine := domain.NewRuleEngine()
		bundles := p8GenBundles().Draw(t, "bundles")
		apiKeyFilters := rapid.SliceOfN(p8GenFilter(), 0, 5).Draw(t, "apiKeyFilters")

		global, globalReverse := runPipeline(engine, bundles, nil)
		apiKeySet, apiKeyReverse := runPipeline(engine, bundles, apiKeyFilters)

		globalNames := make(map[string]struct{}, len(global))
		for _, tool := range global {
			globalNames[tool.Name] = struct{}{}
		}
		for _, tool := range apiKeySet {
			if _, ok := globalNames[tool.Name]; !ok {
				t.Fatalf("API Key 视角出现全局不存在的工具名：%q", tool.Name)
			}
			for _, c := range apiKeyReverse[tool.Name].Candidates {
				if p8MatchedByEnabled(c.OriginalName, apiKeyFilters) {
					t.Fatalf("被 API Key 规则命中的来源仍可见：tool=%q source=%+v", tool.Name, c)
				}
			}
		}
		for name, entry := range globalReverse {
			unmatched := 0
			for _, c := range entry.Candidates {
				if !p8MatchedByEnabled(c.OriginalName, apiKeyFilters) {
					unmatched++
				}
			}
			apiEntry, ok := apiKeyReverse[name]
			if unmatched == 0 {
				if ok {
					t.Fatalf("全部来源被过滤后工具不应可见：%q", name)
				}
				continue
			}
			if !ok {
				t.Fatalf("仍有未过滤来源时工具应可见：%q", name)
			}
			if len(apiEntry.Candidates) != unmatched {
				t.Fatalf("未过滤来源数量不一致：name=%q got=%d want=%d", name, len(apiEntry.Candidates), unmatched)
			}
		}
	})
}

func TestProperty8APIKeyVisibleSetDirected(t *testing.T) {
	engine := domain.NewRuleEngine()
	bundles := []upstreamBundle{
		{
			upstreamID: "up-a",
			sortOrder:  0,
			tools: []domain.ToolDef{
				{OriginalName: "search", Name: "search", InputSchema: []byte("{}")},
				{OriginalName: "read", Name: "read", InputSchema: []byte("{}")},
			},
		},
		{
			upstreamID: "up-b",
			sortOrder:  1,
			tools: []domain.ToolDef{
				{OriginalName: "read", Name: "read", InputSchema: []byte("{}")},
			},
		},
	}
	apiKeyFilters := []domain.FilterRule{{Pattern: "read", IsRegex: false, Enabled: true}}

	global, globalReverse := runPipeline(engine, bundles, nil)
	apiKeySet, apiKeyReverse := runPipeline(engine, bundles, apiKeyFilters)
	if len(global) != 2 || globalReverse["read"].Display.SourceCount != 2 {
		t.Fatalf("全局应有 search/read 两个工具，read 两个来源：global=%+v reverse=%+v", global, globalReverse["read"])
	}
	if len(apiKeySet) != 1 || apiKeySet[0].Name != "search" {
		t.Fatalf("read 的全部来源被过滤后只应保留 search：%+v", apiKeySet)
	}
	if _, ok := apiKeyReverse["read"]; ok {
		t.Fatalf("read 全部来源被过滤后不应可见：%+v", apiKeyReverse["read"])
	}

	disabledFilters := []domain.FilterRule{{Pattern: "read", IsRegex: false, Enabled: false}}
	withDisabled, _ := runPipeline(engine, bundles, disabledFilters)
	if len(withDisabled) != len(global) {
		t.Fatalf("停用规则后可见集合应与全局集合一致：got=%d global=%d", len(withDisabled), len(global))
	}
}
