package aggregation

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 8（API Key 级可见集合为
// 全局集合子集），针对聚合管线纯函数 runPipeline 进行属性测试。
//
// 聚合管线的阶段 6「API Key 级过滤」位于同名去重（阶段 5）之后：它在统一聚合结果之上
// 再应用该 API Key 的启用屏蔽规则，匹配对象是工具的 OriginalName（上游原始名称），
// 而非别名重写后的对外名（Req 13.7）。由于该阶段只删除工具、不改名，因此某 API Key 的
// 可见集合必然是「全局聚合集合（apiKeyFilters 为空时的输出）」的子集，且不包含任何被
// 该 Key 启用规则匹配的工具。
//
// 为什么直接测试 runPipeline 而非 Service.BuildToolSet：
//   - 「子集 + 排除被匹配工具」这一不变量完全位于 runPipeline 的阶段 6；阶段 1
//     （读缓存、读规则、筛选启用上游）只是为管线准备输入数据。
//   - runPipeline 接受「各启用上游的已就绪数据」（upstreamBundle）与「某 API Key 的屏蔽
//     规则集合」作为入参，恰好对应属性陈述中的「任意 API Key 与其启用屏蔽规则集合」，
//     无需构造缓存/数据访问 mock。
//   - 关键观察：同一组 bundles 下，全局集合与某 API Key 集合在阶段 2-5 完全一致，差异
//     仅源于阶段 6 是否传入 apiKeyFilters。因此以同一 bundles 分别调用
//     runPipeline(..., nil) 与 runPipeline(..., apiKeyFilters) 即可严格对照二者关系。
//
// 文件内所有生成器与辅助函数均使用 p8 前缀命名，以避免与同包内其它聚合属性测试文件
//（Property 1 的 agg*、Property 2 的 p2*、Property 7 的 p7*）及 invoker 测试（inv*）中的
// 标识符发生冲突。

// p8NamePool 是工具 OriginalName、非正则屏蔽/别名模式共享的小名称池。
// 收窄取值空间可显著提高「API Key 屏蔽规则命中工具」的概率，从而充分锻炼
// 「排除被匹配工具」这一不变量。集合刻意不含 "__"，以免与去重追加的后缀混淆。
var p8NamePool = []string{
	"alpha", "beta", "gamma", "delta",
	"search", "read", "write", "list_dir",
	"x", "ab", "abc",
}

// p8RegexPool 是一组「单独合法」的正则模式，覆盖通配、字符类、量词与选择分支，
// 并与 p8NamePool 有交集，使正则完整匹配能够命中部分工具名。
var p8RegexPool = []string{
	".*", "a.*", "[a-z_]+", `\w+`, "(alpha|beta)",
	"search", "read.*", "ab?c", "gamma|delta", "[a-z]{1,3}",
}

// p8TargetNames 是别名规则可用的目标名称（空串表示不覆盖对外名称）。
// 别名重写只改写对外 Name/Description、不触及 OriginalName，因此不影响本属性的
// API Key 级匹配（匹配对象始终为 OriginalName），仅用于增加输入多样性。
var p8TargetNames = []string{"", "renamed_pub", "exposed_tool", "friendly_name"}

// p8TargetDescs 是别名规则可用的目标描述（空串表示不覆盖描述）。
var p8TargetDescs = []string{"", "新对外描述 A", "新对外描述 B"}

// p8GenTool 生成单个工具：OriginalName 取自共享名称池以利于与规则命中。
// 不设置 UpstreamID/Order，二者由 runPipeline 按所属上游统一规范化覆盖。
func p8GenTool() *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		name := rapid.SampledFrom(p8NamePool).Draw(t, "originalName")
		return domain.ToolDef{
			OriginalName: name,
			Name:         name,
			Description:  "原始描述",
			InputSchema:  []byte("{}"),
		}
	})
}

// p8GenFilter 生成单条屏蔽规则（pattern、isRegex、enabled）。
// 同时用于上游 MCP 级屏蔽规则与 API Key 级屏蔽规则的生成：
//   - 正则规则：模式取自合法正则池或任意字符串（可能非法，锻炼防御性兜底）；
//   - 非正则规则：模式取自名称池（易命中）或任意字符串。
func p8GenFilter() *rapid.Generator[domain.FilterRule] {
	return rapid.Custom(func(t *rapid.T) domain.FilterRule {
		isRegex := rapid.Bool().Draw(t, "filterIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p8RegexPool),
				rapid.String(),
			).Draw(t, "filterRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p8NamePool),
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

// p8GenAlias 生成单条别名规则。模式取值与屏蔽规则同源，以增加输入多样性；
// 目标名称/描述可空可非空。SortOrder 取较小区间以制造并列。
func p8GenAlias() *rapid.Generator[domain.AliasRule] {
	return rapid.Custom(func(t *rapid.T) domain.AliasRule {
		isRegex := rapid.Bool().Draw(t, "aliasIsRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p8RegexPool),
				rapid.String(),
			).Draw(t, "aliasRegexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(p8NamePool),
				rapid.String(),
			).Draw(t, "aliasExactPattern")
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

// p8GenBundles 生成 1 至 3 个启用上游的输入数据，每个上游含工具、别名规则与 MCP 级
// 屏蔽规则。上游标识按下标取唯一值，便于去重后缀与来源回溯。
func p8GenBundles() *rapid.Generator[[]upstreamBundle] {
	return rapid.Custom(func(t *rapid.T) []upstreamBundle {
		n := rapid.IntRange(1, 3).Draw(t, "numUpstreams")
		bundles := make([]upstreamBundle, n)
		for i := 0; i < n; i++ {
			bundles[i] = upstreamBundle{
				upstreamID: fmt.Sprintf("u%d", i),
				sortOrder:  rapid.IntRange(0, 5).Draw(t, fmt.Sprintf("sortOrder_%d", i)),
				tools:      rapid.SliceOfN(p8GenTool(), 0, 5).Draw(t, fmt.Sprintf("tools_%d", i)),
				aliases:    rapid.SliceOfN(p8GenAlias(), 0, 4).Draw(t, fmt.Sprintf("aliases_%d", i)),
				mcpFilters: rapid.SliceOfN(p8GenFilter(), 0, 4).Draw(t, fmt.Sprintf("filters_%d", i)),
			}
		}
		return bundles
	})
}

// p8RefMatch 是独立于被测实现的参考匹配逻辑，复刻 engine.Match 的语义：
//   - 非正则：区分大小写的精确相等；
//   - 正则：以 `\A(?:pattern)\z` 编译后做完整匹配；非法正则视为「不匹配」
//     （与管线对非法正则的防御性兜底一致）。
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

// p8MatchedByEnabled 判断某原始名称是否被给定屏蔽规则集合中的任一「启用」规则命中。
// 用于参考校验 API Key 级过滤的预期效果。
func p8MatchedByEnabled(originalName string, filters []domain.FilterRule) bool {
	for _, f := range filters {
		if !f.Enabled {
			continue
		}
		if p8RefMatch(f.Pattern, f.IsRegex, originalName) {
			return true
		}
	}
	return false
}

// Feature: mcp-proxy-gateway, Property 8: API Key 级可见集合为全局集合子集
//
// Validates: Requirements 13.7
//
// 对任意启用上游集合（含工具、别名规则与 MCP 级屏蔽规则）与任意 API Key 屏蔽规则集合：
// 以同一 bundles 分别构建「全局聚合集合」（apiKeyFilters 传 nil）与「该 API Key 的可见
// 集合」（传入 apiKeyFilters），二者满足：
//   - 断言一（子集）：API Key 可见集合中的每个工具都出现在全局集合中，且其对外名称、
//     上游原始名与上游归属完全一致——即 API Key 视角只会在全局集合上「做减法」，既不
//     新增工具，也不改写已有工具的名称或来源（Req 13.7）。
//   - 断言二（排除被匹配工具）：API Key 可见集合不包含任何「OriginalName 被该 Key 的
//     启用屏蔽规则命中」的工具（Req 13.7）。
//   - 断言三（互补精确性）：全局集合中的某工具从 API Key 集合中消失，当且仅当其
//     OriginalName 被该 Key 的启用规则命中——既验证「被匹配的必被排除」，也验证「未被
//     匹配的必被保留」，确保 API Key 级过滤不多删也不少删。
//
// 校验经独立参考匹配逻辑（p8RefMatch / p8MatchedByEnabled）完成，不依赖被测实现；
// 且利用「API Key 级过滤匹配 OriginalName、且只删不改名」的事实，按对外名称在两个集合
// 间建立对应关系。
func TestProperty8APIKeyVisibleSetIsSubset(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		engine := domain.NewRuleEngine()
		bundles := p8GenBundles().Draw(t, "bundles")
		apiKeyFilters := rapid.SliceOfN(p8GenFilter(), 0, 5).Draw(t, "apiKeyFilters")

		// 全局集合：不施加任何 API Key 级过滤。
		global, _ := runPipeline(engine, bundles, nil)
		// 该 API Key 视角的可见集合：在同一 bundles 上施加该 Key 的屏蔽规则。
		apiKeySet, _ := runPipeline(engine, bundles, apiKeyFilters)

		// 全局集合按对外名称建立索引，供子集与来源一致性校验。
		globalByName := make(map[string]domain.ToolDef, len(global))
		for _, tool := range global {
			globalByName[tool.Name] = tool
		}

		// 断言一：子集——API Key 集合的每个工具在全局集合中存在且来源一致。
		for _, tool := range apiKeySet {
			g, ok := globalByName[tool.Name]
			if !ok {
				t.Fatalf("API Key 可见集合含全局集合不存在的对外名称：name=%q", tool.Name)
			}
			if g.OriginalName != tool.OriginalName || g.UpstreamID != tool.UpstreamID {
				t.Fatalf("同名工具来源不一致：name=%q apiKey=(%q,%q) global=(%q,%q)",
					tool.Name, tool.UpstreamID, tool.OriginalName, g.UpstreamID, g.OriginalName)
			}
		}

		// 断言二：API Key 可见集合不含任何被启用规则命中的工具。
		for _, tool := range apiKeySet {
			if p8MatchedByEnabled(tool.OriginalName, apiKeyFilters) {
				t.Fatalf("被启用 API Key 规则命中的工具仍出现在可见集合中："+
					"name=%q original=%q", tool.Name, tool.OriginalName)
			}
		}

		// 断言三：互补精确性——全局工具从 API Key 集合中消失当且仅当其被启用规则命中。
		apiKeyNames := make(map[string]struct{}, len(apiKeySet))
		for _, tool := range apiKeySet {
			apiKeyNames[tool.Name] = struct{}{}
		}
		for _, g := range global {
			_, present := apiKeyNames[g.Name]
			matched := p8MatchedByEnabled(g.OriginalName, apiKeyFilters)
			if matched && present {
				t.Fatalf("被启用规则命中的工具未从 API Key 集合中排除：name=%q original=%q",
					g.Name, g.OriginalName)
			}
			if !matched && !present {
				t.Fatalf("未被任何启用规则命中的工具却从 API Key 集合中消失：name=%q original=%q",
					g.Name, g.OriginalName)
			}
		}
	})
}

// TestProperty8APIKeyVisibleSetIsSubsetDirected 是 Property 8 的定向示例：在同一组上游
// 工具之上，分别构建全局集合与施加单条 API Key 屏蔽规则后的可见集合，验证该可见集合
// 恰好是全局集合排除被命中工具后的子集（Req 13.7）。
func TestProperty8APIKeyVisibleSetIsSubsetDirected(t *testing.T) {
	engine := domain.NewRuleEngine()

	bundles := []upstreamBundle{{
		upstreamID: "p8-directed",
		sortOrder:  0,
		tools: []domain.ToolDef{
			{OriginalName: "search", Name: "search"},
			{OriginalName: "read", Name: "read"},
			{OriginalName: "write", Name: "write"},
		},
	}}

	// 启用一条精确屏蔽规则，仅命中 "read"。
	apiKeyFilters := []domain.FilterRule{
		{Pattern: "read", IsRegex: false, Enabled: true},
	}

	global, _ := runPipeline(engine, bundles, nil)
	apiKeySet, _ := runPipeline(engine, bundles, apiKeyFilters)

	// 全局集合应包含全部三个工具。
	if len(global) != 3 {
		t.Fatalf("全局集合应含 3 个工具，实际=%d：%+v", len(global), global)
	}

	// API Key 可见集合应排除 "read"，保留 "search" 与 "write"。
	if len(apiKeySet) != 2 {
		t.Fatalf("API Key 可见集合应含 2 个工具，实际=%d：%+v", len(apiKeySet), apiKeySet)
	}
	for _, tool := range apiKeySet {
		if tool.OriginalName == "read" {
			t.Fatalf("被屏蔽的 read 仍出现在 API Key 可见集合中：%+v", tool)
		}
	}

	// 子集校验：API Key 集合的每个名称都在全局集合中。
	globalNames := make(map[string]struct{}, len(global))
	for _, tool := range global {
		globalNames[tool.Name] = struct{}{}
	}
	for _, tool := range apiKeySet {
		if _, ok := globalNames[tool.Name]; !ok {
			t.Fatalf("API Key 可见集合含全局集合不存在的名称：%q", tool.Name)
		}
	}

	// 停用同一条规则后，可见集合应恢复为与全局集合一致（停用规则在匹配中被忽略）。
	disabledFilters := []domain.FilterRule{
		{Pattern: "read", IsRegex: false, Enabled: false},
	}
	withDisabled, _ := runPipeline(engine, bundles, disabledFilters)
	if len(withDisabled) != len(global) {
		t.Fatalf("停用规则后可见集合应与全局集合一致：got=%d global=%d", len(withDisabled), len(global))
	}
}
