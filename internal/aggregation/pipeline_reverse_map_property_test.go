package aggregation

import (
	"fmt"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 9（别名反向映射可逆），
// 针对聚合管线纯函数 runPipeline 进行属性测试。
//
// 为什么直接测试 runPipeline 而非 Service.InvokeTool：
//   - 反向映射的「可逆性」完全由 runPipeline 阶段 2-6 的输出决定——它返回
//     (tools, reverse)，其中 reverse 为「对外名称 → ReverseEntry{UpstreamID, OriginalName}」。
//     InvokeTool 只是在该 reverse 上做一次查表路由（任务 4.6），可逆性的根在管线本身。
//   - runPipeline 接受「已就绪的各启用上游数据」（upstreamBundle）作为输入，恰好对应
//     属性陈述中的「任意经别名重写后的可见工具」，无需构造缓存/数据访问 mock。
//   - 别名重写仅改写工具对外 Name/Description，绝不触及 OriginalName；因此 reverse 把
//     去重后唯一的对外名称映射回上游原始名是「天然可逆」的，本测试正是验证这一点。
//
// 文件内所有生成器与辅助函数均使用 p9 前缀命名，以避免与同包内其它聚合属性测试文件
// （agg* 的 Property 1、p2* 的 Property 2、p7* 的 Property 7、p8* 的 Property 8、
// p10* 的 Property 10、inv* 的 invoker_test）中的标识符发生冲突。

// p9NamePool 是工具 OriginalName、非正则别名/屏蔽模式共享的小名称池。
// 刻意收窄取值空间以显著提高「跨上游同名」与「同一上游内多工具被别名改到同名目标」
// 的概率，从而充分锻炼去重后仍需保持可逆这一不变量。集合中不含 "__"，保证去重追加的
// 可区分后缀（形如 name__shortId）不会与任何原始/别名后名称相等。
var p9NamePool = []string{"read", "write", "list", "search", "exec", "query"}

// p9AliasTargetPool 是别名规则的目标名称候选。多条别名规则把不同工具改名到同一目标，
// 会制造「别名后同名」的冲突场景（去重必须区分、reverse 必须仍能逐一可逆）。集合不含
// "__"，且与工具名有交集（"read"），以制造「改名工具」与「未改名工具」之间的冲突。
var p9AliasTargetPool = []string{"common", "shared", "read", "renamed"}

// p9RegexPool 是一组「单独合法」的正则模式，用于偶尔生成正则型别名/屏蔽规则，
// 并与 p9NamePool 有交集以便能命中部分工具。
var p9RegexPool = []string{".*", "(read|write)", "[a-z]+", "search", "list|query"}

// p9GenTool 生成单个工具：OriginalName 以较高概率取自 p9NamePool（提高同名概率），
// 并混入少量任意小写短串（不含下划线）。模型上游同步得到的工具其对外 Name 初始等于
// OriginalName，别名重写发生在管线内部，故此处令二者相等。
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

// p9GenAlias 生成单条别名规则，TargetName 必填——确保每条规则一旦命中即会改写对外名称，
// 从而让「经别名重写后的可见工具」成为输入主体（充分覆盖可逆性）。
//   - 正则规则：模式取自合法正则池；
//   - 非正则规则：模式取自名称池（易命中）或任意小写短串。
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

// p9GenFilter 生成单条 MCP 级屏蔽规则（可启用或停用），用于在管线中删除部分工具，
// 增加输入多样性；停用规则在匹配中被忽略。屏蔽不影响可逆性这一不变量，但能产出
// 「部分工具被删除」后剩余集合仍需逐一可逆的场景。
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

// p9GenBundle 生成单个启用上游的管线输入数据。upstreamID 取自调用方传入的索引以保证
// 各上游标识互不相同（便于去重后缀生效，并让 reverse 能区分不同上游的同名工具）。
func p9GenBundle(i int) *rapid.Generator[upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) upstreamBundle {
		return upstreamBundle{
			upstreamID: fmt.Sprintf("up-%d", i),
			sortOrder:  rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder-%d", i)),
			tools:      rapid.SliceOfN(p9GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools-%d", i)),
			aliases:    rapid.SliceOfN(p9GenAlias(), 0, 3).Draw(t, fmt.Sprintf("aliases-%d", i)),
			mcpFilters: rapid.SliceOfN(p9GenFilter(), 0, 2).Draw(t, fmt.Sprintf("filters-%d", i)),
		}
	})
}

// Feature: mcp-proxy-gateway, Property 9: 别名反向映射可逆
//
// Validates: Requirements 10.6
//
// 对任意经别名重写后的可见工具集合（含跨上游同名、别名改名到同名目标等去重场景），
// 聚合管线 runPipeline 返回的反向映射 reverse 满足：
//   - 断言一（一一对应/可逆）：reverse 的条目数等于输出工具数。由于 reverse 以对外
//     名称为键，条目数等于工具数即表明每个工具的对外名称都各占一个唯一键、无相互覆盖，
//     等价于对外名称全局唯一且每个名称都能被收录。
//   - 断言二（唯一还原且与该工具一致）：对每个输出工具，按其对外名称查 reverse 必能命中，
//     且还原出的 (UpstreamID, OriginalName) 恰为该工具自身的上游标识与上游原始名称
//     （别名只改写对外名而不动 OriginalName，故反向映射必然指回正确的上游原始名，Req 10.6）。
//   - 断言三（无多余键）：reverse 不含任何不对应输出工具的对外名称——由断言一与断言二
//     共同保证（键集合恰为输出工具的对外名称集合）。
func TestProperty9AliasReverseMapReversible(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := domain.NewRuleEngine()

		// 生成 1-4 个启用上游，每个上游标识互不相同。
		n := rapid.IntRange(1, 4).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := 0; i < n; i++ {
			bundles[i] = p9GenBundle(i).Draw(t, fmt.Sprintf("bundle-%d", i))
		}

		// 被测黑盒：执行聚合管线阶段 2-6（apiKeyFilters 传 nil 表示无 API Key 级过滤，
		// 使全部工具都进入去重与反向映射，聚焦于可逆性本身）。
		got, reverse := runPipeline(e, bundles, nil)

		// 断言一：一一对应——反向映射条目数等于输出工具数。
		if len(reverse) != len(got) {
			t.Fatalf("反向映射不可逆：reverse 条目数=%d 与输出工具数=%d 不相等（存在对外名称冲突或丢失）",
				len(reverse), len(got))
		}

		// 断言二：对每个输出工具，按对外名称查 reverse 必能唯一还原且与该工具一致。
		seenNames := make(map[string]struct{}, len(got))
		for _, tl := range got {
			// 对外名称应在输出集合内全局唯一（与断言一互为印证）。
			if _, dup := seenNames[tl.Name]; dup {
				t.Fatalf("输出存在重复对外名称，反向映射无法唯一区分：%q", tl.Name)
			}
			seenNames[tl.Name] = struct{}{}

			entry, ok := reverse[tl.Name]
			if !ok {
				t.Fatalf("反向映射缺少可见工具的对外名称，无法还原来源：exposedName=%q", tl.Name)
			}
			if entry.UpstreamID != tl.UpstreamID || entry.OriginalName != tl.OriginalName {
				t.Fatalf("反向映射还原的来源与该工具不一致：exposedName=%q reverse=(%q,%q) 期望=(%q,%q)",
					tl.Name, entry.UpstreamID, entry.OriginalName, tl.UpstreamID, tl.OriginalName)
			}
		}

		// 断言三：无多余键。由断言一（条目数相等）与断言二（每个工具名都在 reverse 中且
		// 名称两两不同）可知 reverse 的键集合恰为输出工具的对外名称集合，此处显式复核。
		for name := range reverse {
			if _, ok := seenNames[name]; !ok {
				t.Fatalf("反向映射含多余对外名称（不对应任何输出工具）：%q", name)
			}
		}
	})
}

// TestProperty9ReverseMapDirected 以具体示例补充验证 Property 9（与属性测试互补），
// 直接锚定「别名改名后仍可由对外名反向还原上游原始名」「同上游多工具改到同名目标经去重后
// 各自可逆」两个关键场景（Req 10.6）。
func TestProperty9ReverseMapDirected(t *testing.T) {
	e := domain.NewRuleEngine()

	t.Run("别名改名后按对外名可逆还原上游原始名", func(t *testing.T) {
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
		if !ok {
			t.Fatalf("反向映射缺少改名后的对外名 pg_query：reverse=%+v", reverse)
		}
		if entry.UpstreamID != "up-x" || entry.OriginalName != "db_query" {
			t.Fatalf("反向映射未还原到正确上游原始名：got=(%q,%q) 期望=(up-x,db_query)",
				entry.UpstreamID, entry.OriginalName)
		}
	})

	t.Run("同上游两工具改到同名目标：去重后各自可逆", func(t *testing.T) {
		bundles := []upstreamBundle{{
			upstreamID: "up-y",
			sortOrder:  0,
			tools: []domain.ToolDef{
				{OriginalName: "a", Name: "a"},
				{OriginalName: "b", Name: "b"},
			},
			// 两条别名规则把 a、b 都改名为 "common"，触发去重。
			aliases: []domain.AliasRule{
				{Pattern: "a", IsRegex: false, TargetName: "common", SortOrder: 0},
				{Pattern: "b", IsRegex: false, TargetName: "common", SortOrder: 1},
			},
		}}

		got, reverse := runPipeline(e, bundles, nil)
		if len(got) != 2 {
			t.Fatalf("应保留 2 个工具（去重改名而非删除），实际=%d：%+v", len(got), got)
		}
		if len(reverse) != 2 {
			t.Fatalf("反向映射应有 2 个条目（一一对应），实际=%d：%+v", len(reverse), reverse)
		}
		// 每个输出工具都应能按其对外名反向还原回各自的上游原始名（a 或 b）。
		for _, tl := range got {
			entry, ok := reverse[tl.Name]
			if !ok {
				t.Fatalf("反向映射缺少对外名：%q", tl.Name)
			}
			if entry.UpstreamID != tl.UpstreamID || entry.OriginalName != tl.OriginalName {
				t.Fatalf("反向映射与工具不一致：exposedName=%q reverse=(%q,%q) 期望=(%q,%q)",
					tl.Name, entry.UpstreamID, entry.OriginalName, tl.UpstreamID, tl.OriginalName)
			}
		}
		// 还原出的上游原始名集合应恰为 {a, b}。
		origins := map[string]struct{}{}
		for _, entry := range reverse {
			origins[entry.OriginalName] = struct{}{}
		}
		if _, ok := origins["a"]; !ok {
			t.Fatalf("反向映射丢失上游原始名 a：reverse=%+v", reverse)
		}
		if _, ok := origins["b"]; !ok {
			t.Fatalf("反向映射丢失上游原始名 b：reverse=%+v", reverse)
		}
	})
}
