package domain

import (
	"regexp"
	"testing"

	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 5（屏蔽规则匹配与
// 启停即时性），针对 engine.ApplyFilters 进行属性测试。
//
// 文件内的生成器与辅助函数均使用 filter* / ref* 前缀命名，以避免与同包内
// rule_engine_match_property_test.go 中的 genPattern/genName/genRestricted 等
// 标识符发生冲突。

// filterNamePool 是一组用于生成工具原始名称与「精确相等」屏蔽模式的候选名。
// 让工具名与屏蔽模式取自同一个小集合，可显著提高「命中」概率，使屏蔽语义被充分锻炼。
var filterNamePool = []string{
	"alpha", "beta", "gamma", "delta",
	"search", "read", "delete_file", "list_dir",
	"a", "b", "ab", "abc",
}

// filterRegexPool 是一组「单独合法」的正则模式，覆盖通配、字符类、量词、选择分支等，
// 并与 filterNamePool 中的名称有交集，以便正则完整匹配能够命中部分工具。
var filterRegexPool = []string{
	".*", "a.*", "[a-z]+", `\w+`, "(alpha|beta)",
	"search", "read.*", "ab?c", "gamma|delta", "[a-z]{1,3}",
}

// genFilterToolName 生成工具原始名称：以较高概率取自 filterNamePool，
// 并混入少量任意短串与空串，覆盖边界情况。
func genFilterToolName() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom(filterNamePool),
		rapid.SampledFrom(filterNamePool),
		rapid.StringMatching(`[a-z_]{0,6}`),
	)
}

// genFilterRule 生成单条屏蔽规则（pattern、isRegex、enabled）。
//   - 正则规则：模式取自 filterRegexPool（合法）或任意字符串（可能非法，用于锻炼
//     非法正则的防御性兜底——此时实现与参考逻辑均视为「不匹配」）。
//   - 非正则规则：模式取自 filterNamePool（易命中）或任意字符串。
func genFilterRule() *rapid.Generator[FilterRule] {
	return rapid.Custom(func(t *rapid.T) FilterRule {
		isRegex := rapid.Bool().Draw(t, "isRegex")
		var pattern string
		if isRegex {
			pattern = rapid.OneOf(
				rapid.SampledFrom(filterRegexPool),
				rapid.String(),
			).Draw(t, "regexPattern")
		} else {
			pattern = rapid.OneOf(
				rapid.SampledFrom(filterNamePool),
				rapid.String(),
			).Draw(t, "exactPattern")
		}
		return FilterRule{
			Pattern: pattern,
			IsRegex: isRegex,
			Enabled: rapid.Bool().Draw(t, "enabled"),
		}
	})
}

// genFilterRules 生成一个屏蔽规则集合（含空集合）。
func genFilterRules() *rapid.Generator[[]FilterRule] {
	return rapid.SliceOfN(genFilterRule(), 0, 8)
}

// genFilterTools 生成工具集合：先生成一组原始名称，再为每个工具赋予唯一的 Order
// 作为身份标识，便于在子序列与启停断言中精确追踪单个工具。
func genFilterTools() *rapid.Generator[[]ToolDef] {
	return rapid.Custom(func(t *rapid.T) []ToolDef {
		names := rapid.SliceOfN(genFilterToolName(), 0, 10).Draw(t, "toolNames")
		tools := make([]ToolDef, len(names))
		for i, name := range names {
			tools[i] = ToolDef{
				OriginalName: name,
				Name:         name,
				// Order 在本集合内唯一，作为工具身份标识。
				Order: i,
			}
		}
		return tools
	})
}

// refFilterMatch 是独立于实现的参考匹配逻辑，复刻 engine.Match 的语义：
//   - 非正则：区分大小写的精确相等比较；
//   - 正则：以 `\A(?:pattern)\z` 编译后做完整匹配；非法正则视为「不匹配」
//     （与 ApplyFilters 的防御性兜底一致）。
func refFilterMatch(pattern string, isRegex bool, originalName string) bool {
	if !isRegex {
		return pattern == originalName
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return false
	}
	return re.MatchString(originalName)
}

// refKeep 返回某工具名在给定规则集合下是否应被保留：当且仅当没有任何「启用」规则
// 命中它时保留（停用规则被忽略）。
func refKeep(originalName string, filters []FilterRule) bool {
	for _, f := range filters {
		if !f.Enabled {
			continue
		}
		if refFilterMatch(f.Pattern, f.IsRegex, originalName) {
			return false
		}
	}
	return true
}

// cloneFilterRules 返回规则切片的浅拷贝。FilterRule 为值类型（字段均为可直接复制的
// 基础类型），浅拷贝即可保证修改副本的 Enabled 不影响原切片。
func cloneFilterRules(filters []FilterRule) []FilterRule {
	out := make([]FilterRule, len(filters))
	copy(out, filters)
	return out
}

// orderSetOf 收集工具集合中所有工具的 Order 值，用于按身份判断某工具是否出现在结果中。
func orderSetOf(tools []ToolDef) map[int]struct{} {
	set := make(map[int]struct{}, len(tools))
	for _, tool := range tools {
		set[tool.Order] = struct{}{}
	}
	return set
}

// Feature: mcp-proxy-gateway, Property 5: 屏蔽规则匹配与启停即时性
//
// Validates: Requirements 9.3, 9.4, 9.11, 13.8
//
// 对任意工具集合与屏蔽规则集合：
//   - ApplyFilters 的输出不包含任何被「启用」规则匹配的工具（Req 9.3）；
//   - 「停用」规则在匹配中被忽略（Req 9.4）——通过参考逻辑同样忽略停用规则、
//     并断言输出恰为参考保留集来共同验证；
//   - 输出是输入的子序列（保持相对顺序），且未被任一启用规则命中的工具全部保留；
//   - 启停即时性（Req 9.11、13.8）：翻转某条规则的启用状态后重新构建集合，结果按
//     更新后的状态反映其影响——仅因该规则被命中的工具，在该规则 false→true 后消失、
//     true→false 后重新出现。
func TestProperty5FilterMatchAndToggle(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := NewRuleEngine()

		tools := genFilterTools().Draw(t, "tools")
		filters := genFilterRules().Draw(t, "filters")

		got := e.ApplyFilters(tools, filters)

		// 参考保留集：仅启用规则参与匹配，未被任一启用规则命中者保留，保持输入顺序。
		want := make([]ToolDef, 0, len(tools))
		for _, tool := range tools {
			if refKeep(tool.OriginalName, filters) {
				want = append(want, tool)
			}
		}

		// 断言一：输出中每个工具都不被任一启用规则匹配（Req 9.3）。
		for _, tool := range got {
			if !refKeep(tool.OriginalName, filters) {
				t.Fatalf("输出包含被启用规则匹配的工具：originalName=%q order=%d filters=%+v",
					tool.OriginalName, tool.Order, filters)
			}
		}

		// 断言二：输出恰为参考保留集（长度、顺序、身份一致）。
		// 这一条同时蕴含「停用规则被忽略」「保持相对顺序」「未命中工具全部保留」。
		if len(got) != len(want) {
			t.Fatalf("输出数量与参考保留集不一致：got=%d want=%d filters=%+v",
				len(got), len(want), filters)
		}
		for i := range want {
			if got[i].Order != want[i].Order || got[i].OriginalName != want[i].OriginalName {
				t.Fatalf("输出第 %d 项与参考保留集不一致：got=(name=%q,order=%d) want=(name=%q,order=%d)",
					i, got[i].OriginalName, got[i].Order, want[i].OriginalName, want[i].Order)
			}
		}

		// 断言三（显式子序列校验）：got 中各 Order 在 tools 中以严格递增的下标出现，
		// 即输出严格保持输入的相对顺序。
		searchFrom := 0
		for _, g := range got {
			found := false
			for j := searchFrom; j < len(tools); j++ {
				if tools[j].Order == g.Order {
					searchFrom = j + 1
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("输出不是输入的子序列（顺序被破坏或包含不存在的工具）：order=%d", g.Order)
			}
		}

		// 断言四：启停即时性（Req 9.11、13.8）。
		// 任取一条规则，构造其「停用」与「启用」两个版本（其余规则不变），
		// 对于仅被该规则命中、而不被任何其他启用规则命中的工具：
		//   - 该规则停用时（false）应被保留；
		//   - 该规则启用时（true）应被排除。
		if len(filters) > 0 {
			ri := rapid.IntRange(0, len(filters)-1).Draw(t, "toggleIndex")

			// 其余规则（排除 ri），用于判断工具是否还被别的启用规则命中。
			others := make([]FilterRule, 0, len(filters))
			for i, f := range filters {
				if i == ri {
					continue
				}
				others = append(others, f)
			}

			filtersOff := cloneFilterRules(filters)
			filtersOff[ri].Enabled = false
			filtersOn := cloneFilterRules(filters)
			filtersOn[ri].Enabled = true

			offSet := orderSetOf(e.ApplyFilters(tools, filtersOff))
			onSet := orderSetOf(e.ApplyFilters(tools, filtersOn))

			rule := filters[ri]
			for _, tool := range tools {
				matchedByRule := refFilterMatch(rule.Pattern, rule.IsRegex, tool.OriginalName)
				// 是否被「其它启用规则」命中：refKeep(others) 为 true 表示无其它启用规则命中。
				notMatchedByOthers := refKeep(tool.OriginalName, others)

				if !matchedByRule || !notMatchedByOthers {
					// 仅关注「唯独被该规则命中」的工具，其启停影响才是确定的。
					continue
				}

				if _, ok := offSet[tool.Order]; !ok {
					t.Fatalf("规则停用后，仅被该规则命中的工具应保留：originalName=%q order=%d rule=%+v",
						tool.OriginalName, tool.Order, rule)
				}
				if _, ok := onSet[tool.Order]; ok {
					t.Fatalf("规则启用后，被该规则命中的工具应被排除：originalName=%q order=%d rule=%+v",
						tool.OriginalName, tool.Order, rule)
				}
			}
		}
	})
}
