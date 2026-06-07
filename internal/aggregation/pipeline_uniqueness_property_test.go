package aggregation

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 1（聚合工具名称全局唯一），
// 针对聚合管线的确定性纯函数 runPipeline 进行属性测试。
//
// 为什么直接测试 runPipeline 而非 Service.BuildToolSet：
//   - 名称去重与全局唯一性这一不变量完全位于 runPipeline 的阶段 2-6 内；阶段 1
//     （读缓存、筛选启用上游）只是为管线准备输入数据。
//   - runPipeline 接受「已就绪的各启用上游数据」（upstreamBundle）作为输入，恰好对应
//     属性陈述中的「任意启用上游集合及其工具列表」，无需构造缓存/数据访问 mock。
//   - 因此本测试以 runPipeline 作为被测黑盒，输入直接构造，输出断言其名称不变量。
//
// 文件内的生成器与辅助函数统一使用 agg* 前缀，避免与同包其它测试标识符冲突。

// aggToolNamePool 是一组工具原始名称候选。让多个上游的工具名取自同一小集合，可显著
// 提高「跨上游同名」概率，从而充分锻炼同名去重逻辑。集合中刻意不含 "__"，以保证去重
// 追加的可区分后缀（形如 name__shortId）永远不会与任何原始/别名后的名称相等。
var aggToolNamePool = []string{"read", "write", "list", "search", "exec", "query"}

// aggAliasTargetPool 是一组别名目标名称候选。多条别名规则把不同工具重命名到同一目标，
// 会制造「会造成冲突的别名规则」（属性陈述明确要求覆盖）。集合同样不含 "__"。其中
// "read" 与工具名重合，可制造「别名后的工具」与「未改名工具」之间的冲突。
var aggAliasTargetPool = []string{"common", "shared", "read", "tool"}

// aggRegexPool 是一组单独合法的正则模式，用于偶尔生成正则型别名/屏蔽规则，
// 与 aggToolNamePool 有交集以便能命中部分工具。
var aggRegexPool = []string{".*", "(read|write)", "[a-z]+", "search", "list|query"}

// aggGenTool 生成单个工具：名称以较高概率取自 aggToolNamePool，并混入少量任意短串
// （仅小写字母，不含下划线）。模型上游同步得到的工具其对外 Name 初始等于 OriginalName，
// 别名重写发生在管线内部，故此处令二者相等。
func aggGenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.OneOf(
			rapid.SampledFrom(aggToolNamePool),
			rapid.SampledFrom(aggToolNamePool),
			rapid.StringMatching(`[a-z]{1,5}`),
		).Draw(t, "toolName")
		return domain.ToolDef{
			OriginalName: name,
			Name:         name,
		}
	})
}

// aggGenAlias 生成单条别名规则（TargetName 必填，以确保会改写对外名称）。
//   - 正则规则：模式取自 aggRegexPool（合法）。
//   - 非正则规则：模式取自 aggToolNamePool（易命中）或任意小写短串。
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

// aggGenFilter 生成单条 MCP 级屏蔽规则（可启用或停用），用于在管线中删除部分工具，
// 增加输入多样性。停用规则在匹配中被忽略，这不影响名称唯一性这一不变量。
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

// aggGenBundle 生成单个启用上游的管线输入数据。upstreamID 取自调用方传入的索引以保证
// 各上游标识互不相同（便于可区分后缀生效）；sortOrder 允许跨上游相等，以验证相同
// sort_order 时维持稳定相对顺序。
func aggGenBundle(i int) *rapid.Generator[upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) upstreamBundle {
		return upstreamBundle{
			upstreamID: fmt.Sprintf("up-%d", i),
			sortOrder:  rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder-%d", i)),
			tools:      rapid.SliceOfN(aggGenTool(), 0, 5).Draw(t, fmt.Sprintf("tools-%d", i)),
			aliases:    rapid.SliceOfN(aggGenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases-%d", i)),
			mcpFilters: rapid.SliceOfN(aggGenFilter(), 0, 2).Draw(t, fmt.Sprintf("filters-%d", i)),
		}
	})
}

// aggReconstructPreDedup 复刻管线在「去重之前」的对外名称序列（按合并顺序）。
//
// 它复用规则引擎已被独立属性测试覆盖的 ApplyFilters（Property 5）与 ApplyAliases
// （Property 6）作为可信构件，并按管线规定的执行顺序（稳定排序合并 → 先屏蔽后重写）
// 重建去重前的名称序列。它刻意不复刻去重逻辑本身——去重正是本属性要验证的对象。
//
// 由于去重与后续 API Key 级过滤（本测试传入空规则）均为「保序且不增删元素位置」的变换，
// 重建序列与 runPipeline 输出在长度与下标上一一对应。
func aggReconstructPreDedup(e domain.Rule_Engine, bundles []upstreamBundle) []string {
	sorted := make([]upstreamBundle, len(bundles))
	copy(sorted, bundles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].sortOrder < sorted[j].sortOrder
	})

	names := make([]string, 0)
	for _, b := range sorted {
		tools := e.ApplyFilters(b.tools, b.mcpFilters)
		tools = e.ApplyAliases(tools, b.aliases)
		for _, tl := range tools {
			names = append(names, tl.Name)
		}
	}
	return names
}

// Feature: mcp-proxy-gateway, Property 1: 聚合工具名称全局唯一
//
// Validates: Requirements 3.6, 8.6
//
// 对任意启用上游集合及其工具列表（含跨上游同名工具与会造成冲突的别名规则），聚合管线
// 输出的可见工具集合满足：
//   - 断言一（全局唯一）：所有对外 Name 互不相同（Req 3.6、8.6）。
//   - 断言二（前者保留、后者加标记）：把去重前按合并顺序排列的对外名称序列与去重后输出
//     逐位对应，则某名称首次出现的位置在输出中原样保留该名称；之后再次出现的同名工具
//     在输出中获得可区分后缀（形如 name__shortId，与原名不同）。
//   - 断言三（反向映射一致）：反向映射的条目数等于输出工具数，且每个对外名称都在其中，
//     体现「对外名称唯一即可由名称唯一还原来源」。
func TestProperty1AggregatedNamesGloballyUnique(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := domain.NewRuleEngine()

		// 生成 1-4 个启用上游，每个上游标识互不相同。
		n := rapid.IntRange(1, 4).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := 0; i < n; i++ {
			bundles[i] = aggGenBundle(i).Draw(t, fmt.Sprintf("bundle-%d", i))
		}

		// 被测黑盒：执行聚合管线阶段 2-6（apiKeyFilters 传 nil 表示无 API Key 级过滤，
		// 使全部工具都流经去重，从而聚焦于名称唯一性）。
		got, reverse := runPipeline(e, bundles, nil)

		// 重建去重前（合并顺序）的对外名称序列，用于断言二。
		preDedup := aggReconstructPreDedup(e, bundles)

		// 去重与空 API Key 过滤均保序不增删，故输出与重建序列长度一致、下标对应。
		if len(got) != len(preDedup) {
			t.Fatalf("输出工具数与去重前序列长度不一致：got=%d preDedup=%d", len(got), len(preDedup))
		}

		// 断言一：全局唯一——对外 Name 集合大小等于输出工具数。
		nameSet := make(map[string]struct{}, len(got))
		for _, tl := range got {
			if _, dup := nameSet[tl.Name]; dup {
				t.Fatalf("输出存在重复对外名称：%q", tl.Name)
			}
			nameSet[tl.Name] = struct{}{}
		}
		if len(nameSet) != len(got) {
			t.Fatalf("对外名称未全局唯一：unique=%d total=%d", len(nameSet), len(got))
		}

		// 断言二：首次出现者保留原名，后续同名者获得可区分标记。
		seen := make(map[string]struct{}, len(preDedup))
		for i, base := range preDedup {
			if _, repeated := seen[base]; !repeated {
				// 首次出现：去重应原样保留该名称（排序在前的上游保留其名称）。
				if got[i].Name != base {
					t.Fatalf("名称首次出现却被改名：idx=%d base=%q got=%q", i, base, got[i].Name)
				}
			} else {
				// 后续同名：必须与原名不同，且带有去重追加的可区分后缀前缀 base+"__"。
				if got[i].Name == base {
					t.Fatalf("同名工具未被区分：idx=%d name=%q", i, base)
				}
				if !strings.HasPrefix(got[i].Name, base+"__") {
					t.Fatalf("后续同名工具未获得可区分后缀：idx=%d base=%q got=%q", i, base, got[i].Name)
				}
			}
			seen[base] = struct{}{}
		}

		// 断言三：反向映射与输出一致——条目数相等且每个对外名称均可还原来源。
		if len(reverse) != len(got) {
			t.Fatalf("反向映射条目数与输出不一致：reverse=%d got=%d", len(reverse), len(got))
		}
		for _, tl := range got {
			if _, ok := reverse[tl.Name]; !ok {
				t.Fatalf("反向映射缺少对外名称：%q", tl.Name)
			}
		}
	})
}

// TestDedupNamesExamples 以具体示例补充验证 Property 1 的去重行为（与属性测试互补）。
// 这些示例直接锚定「前者保留原名、后者加可区分后缀、全局唯一」的预期。
func TestDedupNamesExamples(t *testing.T) {
	t.Run("跨上游同名：前者保留原名后者加后缀", func(t *testing.T) {
		in := []domain.ToolDef{
			{OriginalName: "read", Name: "read", UpstreamID: "up-a"},
			{OriginalName: "read", Name: "read", UpstreamID: "up-b"},
		}
		out := dedupNames(in)
		if out[0].Name != "read" {
			t.Fatalf("排序在前者应保留原名，got=%q", out[0].Name)
		}
		if out[1].Name == "read" || !strings.HasPrefix(out[1].Name, "read__") {
			t.Fatalf("排序在后者应获得可区分后缀，got=%q", out[1].Name)
		}
	})

	t.Run("三个同名且短标识相同：递增序号兜底保证唯一", func(t *testing.T) {
		in := []domain.ToolDef{
			{OriginalName: "x", Name: "dup", UpstreamID: "same"},
			{OriginalName: "y", Name: "dup", UpstreamID: "same"},
			{OriginalName: "z", Name: "dup", UpstreamID: "same"},
		}
		out := dedupNames(in)
		names := map[string]struct{}{}
		for _, tl := range out {
			if _, ok := names[tl.Name]; ok {
				t.Fatalf("去重后仍存在重复名称：%q", tl.Name)
			}
			names[tl.Name] = struct{}{}
		}
		if out[0].Name != "dup" {
			t.Fatalf("首个应保留原名，got=%q", out[0].Name)
		}
	})

	t.Run("无同名：全部保持原名", func(t *testing.T) {
		in := []domain.ToolDef{
			{OriginalName: "a", Name: "a", UpstreamID: "up-a"},
			{OriginalName: "b", Name: "b", UpstreamID: "up-b"},
		}
		out := dedupNames(in)
		if out[0].Name != "a" || out[1].Name != "b" {
			t.Fatalf("无冲突时不应改名，got=%q,%q", out[0].Name, out[1].Name)
		}
	})
}
