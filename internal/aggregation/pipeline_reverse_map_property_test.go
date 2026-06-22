package aggregation

import (
	"fmt"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

var p9NamePool = []string{"read", "write", "list", "search", "exec", "query"}
var p9AliasTargetPool = []string{"common", "shared", "read", "renamed"}
var p9RegexPool = []string{".*", "(read|write)", "[a-z]+", "search", "list|query"}

func p9GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.OneOf(
			rapid.SampledFrom(p9NamePool),
			rapid.SampledFrom(p9NamePool),
			rapid.StringMatching(`[a-z]{1,5}`),
		).Draw(t, "originalName")
		return domain.ToolDef{
			OriginalName: name,
			Name:         name,
			Description:  "原始描述",
			InputSchema:  []byte("{}"),
		}
	})
}

func p9GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(p9RegexPool).Draw(t, "aliasRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p9NamePool),
				rapid.StringMatching(`[a-z]{1,5}`),
			).Draw(t, "aliasExact")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(p9AliasTargetPool).Draw(t, "aliasTarget"),
			TargetDesc: rapid.SampledFrom([]string{"", "新描述"}).Draw(t, "aliasDesc"),
			SortOrder:  rapid.IntRange(0, 5).Draw(t, "aliasSort"),
		}
	})
}

func p9GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(p9RegexPool).Draw(t, "filterRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p9NamePool),
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

func p9GenBundle(i int) *rapid.Generator[upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) upstreamBundle {
		return upstreamBundle{
			upstreamID:   fmt.Sprintf("up-%d", i),
			upstreamName: fmt.Sprintf("上游-%d", i),
			sortOrder:    rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder-%d", i)),
			tools:        rapid.SliceOfN(p9GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools-%d", i)),
			aliases:      rapid.SliceOfN(p9GenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases-%d", i)),
			mcpFilters:   rapid.SliceOfN(p9GenFilter(), 0, 2).Draw(t, fmt.Sprintf("filters-%d", i)),
		}
	})
}

func TestProperty9ReverseMapKeepsAllCandidates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := domain.NewRuleEngine()
		n := rapid.IntRange(1, 4).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := 0; i < n; i++ {
			bundles[i] = p9GenBundle(i).Draw(t, fmt.Sprintf("bundle-%d", i))
		}

		got, reverse := runPipeline(e, bundles, nil)
		if len(reverse) != len(got) {
			t.Fatalf("每个对外工具名应对应一个反向映射条目：reverse=%d got=%d", len(reverse), len(got))
		}
		for _, tool := range got {
			entry, ok := reverse[tool.Name]
			if !ok {
				t.Fatalf("反向映射缺少对外工具名：%q", tool.Name)
			}
			if len(entry.Candidates) == 0 {
				t.Fatalf("工具至少应有一个真实来源：%q", tool.Name)
			}
			if entry.Display.Name != tool.Name {
				t.Fatalf("展示工具名不一致：entry=%q tool=%q", entry.Display.Name, tool.Name)
			}
			for _, c := range entry.Candidates {
				if c.Tool.Name != tool.Name {
					t.Fatalf("候选来源的最终工具名应与对外工具名一致：candidate=%q tool=%q", c.Tool.Name, tool.Name)
				}
				if c.UpstreamID == "" || c.OriginalName == "" {
					t.Fatalf("候选来源必须保留真实上游和原始工具名：%+v", c)
				}
			}
		}
	})
}

func TestProperty9ReverseMapDirected(t *testing.T) {
	e := domain.NewRuleEngine()

	t.Run("别名改名后按对外名可找到真实来源", func(t *testing.T) {
		bundles := []upstreamBundle{{
			upstreamID: "up-x",
			sortOrder:  0,
			tools:      []domain.ToolDef{{OriginalName: "db_query", Name: "db_query"}},
			aliases: []domain.AliasRule{
				{Pattern: "db_query", IsRegex: false, TargetName: "pg_query", SortOrder: 0},
			},
		}}

		got, reverse := runPipeline(e, bundles, nil)
		if len(got) != 1 || got[0].Name != "pg_query" {
			t.Fatalf("别名重写未生效：got=%+v", got)
		}
		entry, ok := reverse["pg_query"]
		if !ok || len(entry.Candidates) != 1 {
			t.Fatalf("pg_query 应有一个候选来源：reverse=%+v", reverse)
		}
		c := entry.Candidates[0]
		if c.UpstreamID != "up-x" || c.OriginalName != "db_query" {
			t.Fatalf("候选来源未还原到正确上游原始名：got=(%q,%q)", c.UpstreamID, c.OriginalName)
		}
	})

	t.Run("同上游两工具改到同名目标：对外一个工具两个来源", func(t *testing.T) {
		bundles := []upstreamBundle{{
			upstreamID: "up-y",
			sortOrder:  0,
			tools: []domain.ToolDef{
				{OriginalName: "a", Name: "a"},
				{OriginalName: "b", Name: "b"},
			},
			aliases: []domain.AliasRule{
				{Pattern: "a", IsRegex: false, TargetName: "common", SortOrder: 0},
				{Pattern: "b", IsRegex: false, TargetName: "common", SortOrder: 1},
			},
		}}

		got, reverse := runPipeline(e, bundles, nil)
		if len(got) != 1 || got[0].Name != "common" || got[0].SourceCount != 2 {
			t.Fatalf("同名别名应归并为一个对外工具且来源数为 2：got=%+v", got)
		}
		origins := map[string]struct{}{}
		for _, c := range reverse["common"].Candidates {
			origins[c.OriginalName] = struct{}{}
		}
		if _, ok := origins["a"]; !ok {
			t.Fatalf("候选来源丢失原始名 a：%+v", reverse["common"].Candidates)
		}
		if _, ok := origins["b"]; !ok {
			t.Fatalf("候选来源丢失原始名 b：%+v", reverse["common"].Candidates)
		}
	})
}
