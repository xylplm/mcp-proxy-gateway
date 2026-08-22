package aggregation

import (
	"fmt"
	"sort"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

var aggToolNamePool = []string{"read", "write", "list", "search", "exec", "query"}
var aggAliasTargetPool = []string{"common", "shared", "read", "tool"}
var aggRegexPool = []string{".*", "(read|write)", "[a-z]+", "search", "list|query"}

func aggGenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.OneOf(
			rapid.SampledFrom(aggToolNamePool),
			rapid.SampledFrom(aggToolNamePool),
			rapid.StringMatching(`[a-z]{1,5}`),
		).Draw(t, "toolName")
		return domain.ToolDef{OriginalName: name, Name: name, InputSchema: []byte("{}")}
	})
}

func aggGenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(aggRegexPool).Draw(t, "aliasRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(aggToolNamePool),
				rapid.StringMatching(`[a-z]{1,5}`),
			).Draw(t, "aliasExact")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(aggAliasTargetPool).Draw(t, "aliasTarget"),
			SortOrder:  rapid.IntRange(0, 5).Draw(t, "aliasSort"),
		}
	})
}

func aggGenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(aggRegexPool).Draw(t, "filterRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(aggToolNamePool),
				rapid.StringMatching(`[a-z]{1,5}`),
			).Draw(t, "filterExact")
		}
		return domain.FilterRule{
			Pattern:   pattern,
			IsRegex:   isRegex,
			Enabled:   rapid.Bool().Draw(t, "filterEnabled"),
			SortOrder: rapid.IntRange(0, 5).Draw(t, "filterSort"),
		}
	})
}

func aggGenBundle(i int) *rapid.Generator[upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) upstreamBundle {
		return upstreamBundle{
			upstreamID:   fmt.Sprintf("up-%d", i),
			upstreamName: fmt.Sprintf("上游-%d", i),
			sortOrder:    rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder-%d", i)),
			tools:        rapid.SliceOfN(aggGenTool(), 0, 5).Draw(t, fmt.Sprintf("tools-%d", i)),
			aliases:      rapid.SliceOfN(aggGenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases-%d", i)),
			mcpFilters:   rapid.SliceOfN(aggGenFilter(), 0, 2).Draw(t, fmt.Sprintf("filters-%d", i)),
		}
	})
}

func aggReconstructPostFilter(e domain.Rule_Engine, bundles []upstreamBundle) []domain.ToolDef {
	sorted := make([]upstreamBundle, len(bundles))
	copy(sorted, bundles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].sortOrder < sorted[j].sortOrder
	})
	out := make([]domain.ToolDef, 0)
	for _, b := range sorted {
		tools := make([]domain.ToolDef, len(b.tools))
		copy(tools, b.tools)
		for i := range tools {
			tools[i].UpstreamID = b.upstreamID
			tools[i].Order = b.sortOrder
		}
		tools = e.ApplyFilters(tools, b.mcpFilters)
		tools = e.ApplyAliases(tools, b.aliases)
		out = append(out, tools...)
	}
	return out
}

func TestProperty1AggregatedNamesAreGroupedByName(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := domain.NewRuleEngine()
		n := rapid.IntRange(1, 4).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := range n {
			bundles[i] = aggGenBundle(i).Draw(t, fmt.Sprintf("bundle-%d", i))
		}

		got, reverse := runPipeline(e, bundles, nil)
		preGroup := aggReconstructPostFilter(e, bundles)

		countByName := make(map[string]int)
		for _, tool := range preGroup {
			if tool.Name != "" {
				countByName[tool.Name]++
			}
		}
		if len(got) != len(countByName) {
			t.Fatalf("输出工具数应等于最终名称集合大小：got=%d unique=%d pre=%+v", len(got), len(countByName), preGroup)
		}
		seen := make(map[string]struct{}, len(got))
		for _, tool := range got {
			if _, ok := seen[tool.Name]; ok {
				t.Fatalf("输出工具名称重复：%q", tool.Name)
			}
			seen[tool.Name] = struct{}{}
			if tool.SourceCount != countByName[tool.Name] {
				t.Fatalf("来源数量不一致：name=%q got=%d want=%d", tool.Name, tool.SourceCount, countByName[tool.Name])
			}
			entry, ok := reverse[tool.Name]
			if !ok {
				t.Fatalf("反向映射缺少工具：%q", tool.Name)
			}
			if len(entry.Candidates) != countByName[tool.Name] {
				t.Fatalf("候选来源数量不一致：name=%q got=%d want=%d", tool.Name, len(entry.Candidates), countByName[tool.Name])
			}
		}
		for name := range countByName {
			if _, ok := seen[name]; !ok {
				t.Fatalf("输出缺少最终工具名：%q", name)
			}
		}
	})
}

func TestToolGroupingExamples(t *testing.T) {
	e := domain.NewRuleEngine()

	t.Run("跨上游同名只对外展示一个工具并保留两个来源", func(t *testing.T) {
		bundles := []upstreamBundle{
			{upstreamID: "up-a", upstreamName: "A", sortOrder: 0, tools: []domain.ToolDef{{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}}},
			{upstreamID: "up-b", upstreamName: "B", sortOrder: 1, tools: []domain.ToolDef{{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}}},
		}
		out, reverse := runPipeline(e, bundles, nil)
		if len(out) != 1 || out[0].Name != "read" || out[0].SourceCount != 2 {
			t.Fatalf("同名来源应展示为一个工具且来源数为 2：out=%+v", out)
		}
		if len(reverse["read"].Candidates) != 2 {
			t.Fatalf("read 应有两个候选来源：%+v", reverse["read"])
		}
	})

	t.Run("schema 不一致时只标记冲突不拆分工具", func(t *testing.T) {
		bundles := []upstreamBundle{
			{upstreamID: "up-a", sortOrder: 0, tools: []domain.ToolDef{{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"object"}`)}}},
			{upstreamID: "up-b", sortOrder: 1, tools: []domain.ToolDef{{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"string"}`)}}},
		}
		out, reverse := runPipeline(e, bundles, nil)
		if len(out) != 1 || !out[0].SchemaConflict {
			t.Fatalf("schema 不一致应展示一个工具并标记冲突：out=%+v", out)
		}
		if reverse["query"].Candidates[0].Compatible != true || reverse["query"].Candidates[1].Compatible != false {
			t.Fatalf("只有与展示 schema 一致的来源应为兼容：%+v", reverse["query"].Candidates)
		}
	})

	t.Run("schema 字段顺序不同但语义相同时仍视为兼容", func(t *testing.T) {
		bundles := []upstreamBundle{
			{upstreamID: "up-a", sortOrder: 0, tools: []domain.ToolDef{{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`)}}},
			{upstreamID: "up-b", sortOrder: 1, tools: []domain.ToolDef{{OriginalName: "query", Name: "query", InputSchema: []byte(`{"properties":{"q":{"type":"string"}},"type":"object"}`)}}},
		}
		out, reverse := runPipeline(e, bundles, nil)
		if len(out) != 1 || out[0].SchemaConflict {
			t.Fatalf("语义相同的 schema 不应标记冲突：out=%+v", out)
		}
		for _, c := range reverse["query"].Candidates {
			if !c.Compatible {
				t.Fatalf("语义相同的 schema 来源应可调用：%+v", reverse["query"].Candidates)
			}
		}
	})

	t.Run("空 schema 与默认 object schema 视为兼容", func(t *testing.T) {
		bundles := []upstreamBundle{
			{upstreamID: "up-a", sortOrder: 0, tools: []domain.ToolDef{{OriginalName: "ping", Name: "ping", InputSchema: nil}}},
			{upstreamID: "up-b", sortOrder: 1, tools: []domain.ToolDef{{OriginalName: "ping", Name: "ping", InputSchema: []byte(`{"type":"object"}`)}}},
		}
		out, reverse := runPipeline(e, bundles, nil)
		if len(out) != 1 || out[0].SchemaConflict {
			t.Fatalf("空 schema 与默认 object schema 不应标记冲突：out=%+v", out)
		}
		for _, c := range reverse["ping"].Candidates {
			if !c.Compatible {
				t.Fatalf("空 schema 与默认 object schema 来源均应可调用：%+v", reverse["ping"].Candidates)
			}
		}
	})
}
