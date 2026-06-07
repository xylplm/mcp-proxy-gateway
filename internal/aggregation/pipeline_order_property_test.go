package aggregation

import (
	"fmt"
	"sort"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 2（上游排序在聚合中保持），
// 针对聚合管线的确定性纯函数 runPipeline 进行属性测试。
//
// 为什么直接测试 runPipeline 而非 Service.BuildToolSet：
//   - 「上游排序保持」这一不变量完全位于 runPipeline 的阶段 2（按 sort_order 稳定排序
//     合并）；阶段 1（读缓存、筛选启用上游）只是为管线准备输入数据，与排序逻辑无关。
//   - runPipeline 接受「各启用上游的已就绪数据」（upstreamBundle）作为输入，恰好对应
//     属性陈述中的「任意上游集合与一个合法排序」，无需构造缓存/数据访问 mock。
//
// 文件内所有生成器与辅助函数均以 p2 前缀命名，避免与同包内其它聚合属性测试文件
//（Property 1 的 agg*、Property 7 的 p7* 等）中的标识符发生冲突。

// p2NamePool 是工具 OriginalName、非正则屏蔽/别名模式共享的小名称池。
// 收窄取值空间可提高规则命中概率，使「部分工具被屏蔽删除」更频繁发生，
// 从而在工具被删减的情况下仍充分验证上游排序保持这一不变量。
var p2NamePool = []string{"read", "write", "list", "search", "exec", "query", "ping"}

// p2RegexPool 是一组单独合法的正则模式，用于偶尔生成正则型规则，与 p2NamePool 有交集
// 以便能命中部分工具。
var p2RegexPool = []string{".*", "(read|write)", "[a-z]+", "search", "list|query"}

// p2AliasTargetPool 是别名规则可用的目标名称池（含空串表示不覆盖对外名称）。
// 别名重写只改写对外 Name/Description、不触及 OriginalName 与上游归属，
// 因此其取值不影响排序不变量，仅用于增加输入多样性。
var p2AliasTargetPool = []string{"", "renamed", "common", "shared"}

// p2GenTool 生成单个工具：OriginalName 取自共享名称池。Name 初始等于 OriginalName
// （别名重写发生在管线内部）。不设置 UpstreamID/Order，二者由 runPipeline 按所属上游
// 统一规范化覆盖。
func p2GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.OneOf(
			rapid.SampledFrom(p2NamePool),
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

// p2GenAlias 生成单条别名规则，用于在管线中重写部分工具的对外名，增加输入多样性。
func p2GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(p2RegexPool).Draw(t, "aliasRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p2NamePool),
				rapid.StringMatching(`[a-z]{1,5}`),
			).Draw(t, "aliasExact")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(p2AliasTargetPool).Draw(t, "aliasTarget"),
			SortOrder:  rapid.IntRange(0, 4).Draw(t, "aliasSort"),
		}
	})
}

// p2GenFilter 生成单条 MCP 级屏蔽规则（可启用或停用），用于在管线中删除部分工具，
// 验证排序保持不变量在工具被删减后依然成立。
func p2GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.SampledFrom(p2RegexPool).Draw(t, "filterRegex")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p2NamePool),
				rapid.StringMatching(`[a-z]{1,5}`),
			).Draw(t, "filterExact")
		}
		return domain.FilterRule{
			Pattern:   pattern,
			IsRegex:   isRegex,
			Enabled:   rapid.Bool().Draw(t, "filterEnabled"),
			SortOrder: rapid.IntRange(0, 4).Draw(t, "filterSort"),
		}
	})
}

// p2GenBundles 生成 1 至 4 个启用上游的输入数据，并为它们赋予「一个合法排序」：
// 对 [0, n) 做 Fisher-Yates 洗牌得到互不相同的 sortOrder。互不相同的 sort_order 正是
// 一个合法排序的语义（每个上游占据唯一位置，对应 Req 3.5 中「恰好一次排列」），使
// 期望的上游先后顺序唯一确定。各上游标识按下标取唯一值，便于按上游回溯其排序。
func p2GenBundles() *rapid.Generator[[]upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) []upstreamBundle {
		n := rapid.IntRange(1, 4).Draw(t, "numUpstreams")

		// 构造合法排序：order 为 [0, n) 的一个排列。
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		for i := n - 1; i > 0; i-- {
			j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("swap_%d", i))
			order[i], order[j] = order[j], order[i]
		}

		bundles := make([]upstreamBundle, n)
		for i := 0; i < n; i++ {
			bundles[i] = upstreamBundle{
				upstreamID: fmt.Sprintf("u%d", i),
				sortOrder:  order[i],
				tools:      rapid.SliceOfN(p2GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools_%d", i)),
				aliases:    rapid.SliceOfN(p2GenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases_%d", i)),
				mcpFilters: rapid.SliceOfN(p2GenFilter(), 0, 2).Draw(t, fmt.Sprintf("filters_%d", i)),
			}
		}
		return bundles
	})
}

// p2Pair 记录输出中一个工具的「上游标识 + 上游原始名」，用于对照期望的合并顺序。
// 选用 OriginalName 而非对外 Name 作为对照键，是因为别名重写与同名去重都会改写对外
// Name，但都不会改写 OriginalName，也不会改变工具相对位置——因此 (UpstreamID,
// OriginalName) 的出现序列在去重前后保持一致，能精确表达「排序是否保持」。
type p2Pair struct {
	upstreamID   string
	originalName string
}

// p2Reconstruct 独立复刻管线在「排序合并 + 先屏蔽后重写」之后、按合并顺序排列的
// (UpstreamID, OriginalName) 序列，作为期望基准。
//
// 它按管线规定复刻阶段 2（按 sort_order 稳定升序排列上游）与阶段 3-4（对每个上游先
// 屏蔽后重写），并复用已被独立属性测试覆盖的可信构件 ApplyFilters（Property 5）与
// ApplyAliases（Property 6）。它刻意不复刻阶段 5 去重与阶段 6 API Key 过滤——
//   - 去重只改写对外 Name、不增删元素也不改变相对位置；
//   - 本测试传入空 API Key 规则，过滤不删除任何元素；
//
// 因此期望序列与 runPipeline 输出在长度、下标与 (UpstreamID, OriginalName) 上应逐位
// 一致。任何「排序错误（如降序、未按 sort_order、上游块交错或不稳定）」都会使二者
// 出现差异，从而被本测试捕获。
func p2Reconstruct(e domain.Rule_Engine, bundles []upstreamBundle) []p2Pair {
	sorted := make([]upstreamBundle, len(bundles))
	copy(sorted, bundles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].sortOrder < sorted[j].sortOrder
	})

	pairs := make([]p2Pair, 0)
	for _, b := range sorted {
		// 与管线一致：先按所属上游规范化归属信息，再依次屏蔽、重写。
		tools := make([]domain.ToolDef, len(b.tools))
		copy(tools, b.tools)
		for i := range tools {
			tools[i].UpstreamID = b.upstreamID
			tools[i].Order = b.sortOrder
		}
		tools = e.ApplyFilters(tools, b.mcpFilters)
		tools = e.ApplyAliases(tools, b.aliases)
		for _, tl := range tools {
			pairs = append(pairs, p2Pair{upstreamID: tl.UpstreamID, originalName: tl.OriginalName})
		}
	}
	return pairs
}

// Feature: mcp-proxy-gateway, Property 2: 上游排序在聚合中保持
//
// Validates: Requirements 3.4, 10.1
//
// 对任意上游集合与一个合法排序（互不相同的 sort_order），聚合管线输出的可见工具集合
// 满足：各上游的工具均按该排序由前到后出现。具体以三个互相印证的断言表达：
//   - 断言一（逐位一致）：输出的 (UpstreamID, OriginalName) 序列与按排序合并、先屏蔽后
//     重写得到的期望序列逐位相等——既保证跨上游块按 sort_order 由前到后排列，也保证
//     每个上游块内工具维持其原有相对顺序（Req 3.4、10.1）。
//   - 断言二（Order 非递减）：输出中各工具继承的上游排序值 Order 沿输出方向非递减，
//     直观体现排序在前的上游其工具先出现。
//   - 断言三（上游块连续且按序）：输出中同一上游的工具相邻成块、各块按 sort_order 升序
//     排列，不存在交错。
//
// 为隔离「排序合并」这一不变量，本属性不施加 API Key 级过滤（apiKeyFilters 传 nil）。
func TestProperty2UpstreamOrderPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := domain.NewRuleEngine()
		bundles := p2GenBundles().Draw(t, "bundles")

		// 被测黑盒：执行聚合管线阶段 2-6（apiKeyFilters 传 nil，聚焦排序合并）。
		got, _ := runPipeline(e, bundles, nil)

		// 期望基准：按排序合并、先屏蔽后重写后的 (UpstreamID, OriginalName) 序列。
		expected := p2Reconstruct(e, bundles)

		// 断言一：长度与逐位 (UpstreamID, OriginalName) 一致。
		if len(got) != len(expected) {
			t.Fatalf("输出工具数与期望序列长度不一致：got=%d expected=%d", len(got), len(expected))
		}
		for i := range got {
			if got[i].UpstreamID != expected[i].upstreamID || got[i].OriginalName != expected[i].originalName {
				t.Fatalf("第 %d 个工具的上游/原始名与期望不一致：got=(%q,%q) expected=(%q,%q)",
					i, got[i].UpstreamID, got[i].OriginalName, expected[i].upstreamID, expected[i].originalName)
			}
		}

		// upstreamID -> sortOrder，用于断言二/三。
		sortOrderByUpstream := make(map[string]int, len(bundles))
		for _, b := range bundles {
			sortOrderByUpstream[b.upstreamID] = b.sortOrder
		}

		// 断言二：输出中 Order 字段非递减（排序在前的上游其工具先出现）。
		for i := 1; i < len(got); i++ {
			if got[i].Order < got[i-1].Order {
				t.Fatalf("输出中上游排序未保持（Order 出现递减）：idx %d 的 Order=%d < idx %d 的 Order=%d",
					i, got[i].Order, i-1, got[i-1].Order)
			}
		}

		// 断言三：同一上游的工具相邻成块，各块按 sort_order 升序、互不交错。
		runOrder := make([]string, 0)     // 各上游块按出现顺序排列的 upstreamID
		seen := make(map[string]struct{}) // 已出现过（其块已结束）的上游
		var prev string
		for i, tl := range got {
			if i == 0 || tl.UpstreamID != prev {
				if _, dup := seen[tl.UpstreamID]; dup {
					t.Fatalf("上游 %q 的工具未相邻成块（出现交错）", tl.UpstreamID)
				}
				if prev != "" {
					seen[prev] = struct{}{}
				}
				runOrder = append(runOrder, tl.UpstreamID)
				prev = tl.UpstreamID
			}
		}
		for i := 1; i < len(runOrder); i++ {
			if sortOrderByUpstream[runOrder[i]] < sortOrderByUpstream[runOrder[i-1]] {
				t.Fatalf("上游块未按 sort_order 升序排列：%q(order=%d) 出现在 %q(order=%d) 之后",
					runOrder[i], sortOrderByUpstream[runOrder[i]],
					runOrder[i-1], sortOrderByUpstream[runOrder[i-1]])
			}
		}
	})
}

// TestProperty2UpstreamOrderPreservedDirected 是 Property 2 的定向示例：三个上游以
// 「乱序的 sort_order」与「乱序的输入次序」给出，验证聚合输出严格按 sort_order 升序
// 排列各上游的工具块，且块内工具保持原相对顺序（Req 3.4、10.1）。
func TestProperty2UpstreamOrderPreservedDirected(t *testing.T) {
	e := domain.NewRuleEngine()

	// 输入次序刻意与 sort_order 不一致：u-mid(1)、u-last(2)、u-first(0)。
	bundles := []upstreamBundle{
		{
			upstreamID: "u-mid",
			sortOrder:  1,
			tools: []domain.ToolDef{
				{OriginalName: "m1", Name: "m1"},
				{OriginalName: "m2", Name: "m2"},
			},
		},
		{
			upstreamID: "u-last",
			sortOrder:  2,
			tools: []domain.ToolDef{
				{OriginalName: "l1", Name: "l1"},
			},
		},
		{
			upstreamID: "u-first",
			sortOrder:  0,
			tools: []domain.ToolDef{
				{OriginalName: "f1", Name: "f1"},
				{OriginalName: "f2", Name: "f2"},
			},
		},
	}

	got, _ := runPipeline(e, bundles, nil)

	// 期望严格按 sort_order 升序：u-first(f1,f2) → u-mid(m1,m2) → u-last(l1)。
	want := []p2Pair{
		{"u-first", "f1"},
		{"u-first", "f2"},
		{"u-mid", "m1"},
		{"u-mid", "m2"},
		{"u-last", "l1"},
	}
	if len(got) != len(want) {
		t.Fatalf("输出工具数与期望不一致：got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i].UpstreamID != want[i].upstreamID || got[i].OriginalName != want[i].originalName {
			t.Fatalf("第 %d 个工具与期望不一致：got=(%q,%q) want=(%q,%q)",
				i, got[i].UpstreamID, got[i].OriginalName, want[i].upstreamID, want[i].originalName)
		}
	}
}
