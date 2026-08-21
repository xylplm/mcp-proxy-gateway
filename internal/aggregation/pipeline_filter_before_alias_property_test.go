package aggregation

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 7（管线顺序——屏蔽先于
// 重写），针对聚合管线纯函数 runPipeline 进行属性测试。
//
// 管线执行顺序固定（设计文档「工具聚合管线」）：阶段 3「MCP 级屏蔽」先于阶段 4
// 「别名/描述重写」。屏蔽规则匹配工具的 OriginalName（上游原始名称），别名重写仅改写
// 对外 Name/Description 而不触及 OriginalName。因此「先屏蔽后重写」意味着：任何被启用
// 屏蔽规则命中的工具在重写之前即被排除，且不会因别名重写产生的新对外名称而重新出现
// （Req 10.2）。
//
// 文件内所有生成器与辅助函数均使用 p7 前缀命名，以避免与同包内其它并发新增的聚合
// 属性测试文件（Property 1/2/8 等）中的标识符发生冲突。

// p7NamePool 是工具 OriginalName、非正则屏蔽模式、非正则别名模式共享的小名称池。
// 刻意收窄取值空间以显著提高「同一工具同时命中屏蔽规则与别名规则」的概率，
// 从而充分锻炼「屏蔽先于重写」这一顺序不变量。
var p7NamePool = []string{
	"alpha", "beta", "gamma", "delta",
	"search", "read", "delete_file", "list_dir",
	"x", "ab", "abc",
}

// p7RegexPool 是一组「单独合法」的正则模式，覆盖通配、字符类、量词与选择分支，
// 并与 p7NamePool 有交集，使正则完整匹配能够命中部分工具名。
var p7RegexPool = []string{
	".*", "a.*", "[a-z_]+", `\w+`, "(alpha|beta)",
	"search", "read.*", "ab?c", "gamma|delta", "[a-z]{1,3}",
}

// p7TargetNames 是别名规则可用的目标名称（空串表示不覆盖对外名称）。
var p7TargetNames = []string{"", "renamed_pub", "exposed_tool", "friendly_name"}

// p7TargetDescs 是别名规则可用的目标描述（空串表示不覆盖描述）。
var p7TargetDescs = []string{"", "新对外描述 A", "新对外描述 B"}

// p7GenTool 生成单个工具：OriginalName 取自共享名称池以利于与规则命中。
// 不设置 UpstreamID/Order，二者由 runPipeline 按所属上游统一规范化覆盖。
func p7GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.SampledFrom(p7NamePool).Draw(t, "originalName")
		return domain.ToolDef{
			OriginalName: name,
			Name:         name,
			Description:  "原始描述",
			InputSchema:  []byte("{}"),
		}
	})
}

// p7GenFilter 生成单条 MCP 级屏蔽规则（pattern、isRegex、enabled）。
//   - 正则规则：模式取自合法正则池或任意字符串（可能非法，锻炼防御性兜底）；
//   - 非正则规则：模式取自名称池（易命中）或任意字符串。
func p7GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p7RegexPool),
				rapid.String(),
			).Draw(t, "filterRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p7NamePool),
				rapid.String(),
			).Draw(t, "filterExactPattern")
		}
		return domain.FilterRule{
			Pattern: pattern,
			IsRegex: isRegex,
			Enabled: rapid.Bool().Draw(t, "filterEnabled"),
		}
	})
}

// p7GenAlias 生成单条别名规则。模式取值与屏蔽规则同源，以制造「同一工具同时命中
// 屏蔽与别名」的场景；目标名称/描述可空可非空。SortOrder 取较小区间以制造并列。
func p7GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p7RegexPool),
				rapid.String(),
			).Draw(t, "aliasRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p7NamePool),
				rapid.String(),
			).Draw(t, "aliasExactPattern")
		}
		return domain.AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: rapid.SampledFrom(p7TargetNames).Draw(t, "aliasTargetName"),
			TargetDesc: rapid.SampledFrom(p7TargetDescs).Draw(t, "aliasTargetDesc"),
			SortOrder:  rapid.IntRange(0, 4).Draw(t, "aliasSortOrder"),
		}
	})
}

// p7GenBundles 生成 1 至 3 个启用上游的输入数据，每个上游含工具、别名规则与屏蔽规则。
// 上游标识按下标取唯一值，便于结果按上游回溯其屏蔽规则。
func p7GenBundles() *rapid.Generator[[]upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) []upstreamBundle {
		n := rapid.IntRange(1, 3).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := range n {
			bundles[i] = upstreamBundle{
				upstreamID: fmt.Sprintf("u%d", i),
				sortOrder:  rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder_%d", i)),
				tools:      rapid.SliceOfN(p7GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools_%d", i)),
				aliases:    rapid.SliceOfN(p7GenAlias(), 0, 4).Draw(t, fmt.Sprintf("aliases_%d", i)),
				mcpFilters: rapid.SliceOfN(p7GenFilter(), 0, 4).Draw(t, fmt.Sprintf("filters_%d", i)),
			}
		}
		return bundles
	})
}

// p7RefMatch 是独立于被测实现的参考匹配逻辑，复刻 engine.Match 的语义：
//   - 非正则：区分大小写的精确相等；
//   - 正则：以 `\A(?:pattern)\z` 编译后做完整匹配；非法正则视为「不匹配」
//     （与管线对非法正则的防御性兜底一致）。
func p7RefMatch(pattern string, isRegex bool, originalName string) bool {
	if !isRegex {
		return pattern == originalName
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return false
	}
	return re.MatchString(originalName)
}

// p7BlockedByMCP 判断某原始名称是否被给定屏蔽规则集合中的任一「启用」规则命中。
func p7BlockedByMCP(originalName string, filters []domain.FilterRule) bool {
	for _, f := range filters {
		if !f.Enabled {
			continue
		}
		if p7RefMatch(f.Pattern, f.IsRegex, originalName) {
			return true
		}
	}
	return false
}

// Feature: mcp-proxy-gateway, Property 7: 管线顺序——屏蔽先于重写
//
// Validates: Requirements 10.2
//
// 对任意启用上游集合（含工具、别名规则与屏蔽规则）：聚合管线输出的可见集合中，
// 不存在任何「其 OriginalName 被所属上游的启用屏蔽规则命中」的工具。也就是说，
// 凡是同时匹配启用屏蔽规则与别名规则的工具，都在重写之前即被排除，绝不会因别名重写
// 改名后重新出现（Req 10.2）。
//
// 为隔离「MCP 级屏蔽（阶段 3）先于别名重写（阶段 4）」这一顺序关系，本属性不施加
// API Key 级过滤（apiKeyFilters 传 nil）。校验经独立参考匹配逻辑完成，不依赖被测实现，
// 且利用「别名重写不改写 OriginalName」的事实，按输出工具的 (UpstreamID, OriginalName)
// 回溯其上游屏蔽规则进行判定。
func TestProperty7FilterBeforeAlias(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		engine := domain.NewRuleEngine()
		bundles := p7GenBundles().Draw(t, "bundles")

		got, _ := runPipeline(engine, bundles, nil)

		// upstreamID -> 该上游的 MCP 级屏蔽规则，用于回溯校验。
		filtersByUpstream := make(map[string][]domain.FilterRule, len(bundles))
		for _, b := range bundles {
			filtersByUpstream[b.upstreamID] = b.mcpFilters
		}

		for _, tool := range got {
			if p7BlockedByMCP(tool.OriginalName, filtersByUpstream[tool.UpstreamID]) {
				t.Fatalf("被启用屏蔽规则命中的工具出现在最终可见集合中（屏蔽未先于重写）："+
					"upstream=%q original=%q exposedName=%q", tool.UpstreamID, tool.OriginalName, tool.Name)
			}
		}
	})
}

// TestProperty7FilterBeforeAliasDirected 是 Property 7 的定向示例：单个工具同时命中
// 一条启用屏蔽规则与一条会将其改名的别名规则。屏蔽先于重写时，该工具被排除，且不会
// 以别名重写后的对外名称重新出现（Req 10.2）。
//
// 对照项：同一上游内另有一个未被屏蔽、但同样命中别名规则的工具，它应当保留并以重写后
// 的对外名称出现——以此确认「未重新出现」并非因为别名重写本身失效（即别名重写确实在
// 对存活工具生效）。
func TestProperty7FilterBeforeAliasDirected(t *testing.T) {
	engine := domain.NewRuleEngine()

	blocked := domain.ToolDef{OriginalName: "p7_blocked", Name: "p7_blocked"}
	survivor := domain.ToolDef{OriginalName: "p7_survivor", Name: "p7_survivor"}

	bundles := []upstreamBundle{{
		upstreamID: "p7-directed",
		sortOrder:  0,
		tools:      []domain.ToolDef{blocked, survivor},
		// 屏蔽规则只命中 p7_blocked。
		mcpFilters: []domain.FilterRule{{Pattern: "p7_blocked", IsRegex: false, Enabled: true}},
		// 两条别名规则分别把两个工具改名为各自的对外名。
		aliases: []domain.AliasRule{
			{Pattern: "p7_blocked", IsRegex: false, TargetName: "p7_blocked_friendly", SortOrder: 0},
			{Pattern: "p7_survivor", IsRegex: false, TargetName: "p7_survivor_friendly", SortOrder: 0},
		},
	}}

	got, _ := runPipeline(engine, bundles, nil)

	// 被屏蔽工具不得出现：既不以原始名也不以别名重写后的名称出现。
	for _, tool := range got {
		if tool.OriginalName == "p7_blocked" {
			t.Fatalf("被屏蔽工具仍出现在输出中：exposedName=%q", tool.Name)
		}
		if tool.Name == "p7_blocked_friendly" {
			t.Fatalf("被屏蔽工具因别名重写以新名称重新出现（违反屏蔽先于重写）：exposedName=%q", tool.Name)
		}
	}

	// 对照：未被屏蔽的工具应保留，并以别名重写后的对外名称出现，确认别名重写确实生效。
	var survivorFound bool
	for _, tool := range got {
		if tool.OriginalName == "p7_survivor" {
			survivorFound = true
			if tool.Name != "p7_survivor_friendly" {
				t.Fatalf("存活工具的别名重写未生效：exposedName=%q want=%q", tool.Name, "p7_survivor_friendly")
			}
		}
	}
	if !survivorFound {
		t.Fatalf("未被屏蔽的工具应保留在输出中，但未找到：got=%+v", got)
	}

	if len(got) != 1 {
		t.Fatalf("输出应仅含 1 个工具（屏蔽 1 个、保留 1 个），实际=%d：%+v", len(got), got)
	}
}
