package domain

import (
	"regexp"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// aliasNamePool 是工具原始名称与「非正则」别名匹配模式共享的小字母表名称池。
// 刻意收窄取值空间，以显著提高「多条别名规则同时匹配同一工具」的出现概率，
// 从而让「仅应用首条」的语义得到充分锻炼。空串用于覆盖空名称/空精确模式边界。
var aliasNamePool = []string{"foo", "bar", "baz", "tool_a", "tool_b", "abc", "x", ""}

// aliasRegexAtoms 是一组可与名称池产生「完整匹配」的合法正则片段，
// 用于在开启正则的别名规则上覆盖字面量、字符类、量词、通配与选择分支等。
var aliasRegexAtoms = []string{
	"foo", "bar", "baz", "tool_a", "tool_b", "abc", "x",
	".*", "[a-z_]+", ".", "fo+", "ba.", "a|b", "",
}

// genAliasName 从共享名称池采样，作为工具原始名称或非正则模式。
func genAliasName() *rapid.Generator[string] {
	return rapid.SampledFrom(aliasNamePool)
}

// genAliasRegexPattern 生成正则模式：多数取自可命中名称池的合法正则片段，
// 同时混入任意字符串（可能为非法正则），以覆盖「非法正则视为不匹配并跳过」的路径。
func genAliasRegexPattern() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom(aliasRegexAtoms),
		rapid.String(),
	)
}

// genAliasTargetName 生成目标名称：以一定概率为空（表示不覆盖名称），
// 其余为可区分的非空名称。
func genAliasTargetName() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(""),
		rapid.SampledFrom([]string{"newName1", "newName2", "renamed"}),
	)
}

// genAliasTargetDesc 生成目标描述：以一定概率为空（表示不覆盖描述），
// 其余为可区分的非空描述。
func genAliasTargetDesc() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(""),
		rapid.SampledFrom([]string{"new desc 1", "new desc 2"}),
	)
}

// genAliasToolDef 生成工具定义；OriginalName 取自名称池以利于命中。
func genAliasToolDef() *rapid.Generator[ToolDef] {
	return rapid.Custom(func(t *rapid.T) ToolDef {
		return ToolDef{
			OriginalName: genAliasName().Draw(t, "originalName"),
			Name:         rapid.SampledFrom(aliasNamePool).Draw(t, "name"),
			Description:  rapid.String().Draw(t, "description"),
			InputSchema:  []byte(rapid.SampledFrom([]string{"{}", `{"type":"object"}`, "null"}).Draw(t, "schema")),
			UpstreamID:   rapid.SampledFrom([]string{"u1", "u2"}).Draw(t, "upstreamID"),
			Order:        rapid.IntRange(0, 5).Draw(t, "order"),
		}
	})
}

// genAliasRule 生成别名规则：
//   - 非正则规则模式取自名称池（提高与多个工具/多条规则同时命中的概率）；
//   - 正则规则模式取自合法正则片段或任意字符串（含非法）；
//   - 目标名称/描述可空可非空（至少其一非空并非本属性关注点，故不强制）；
//   - SortOrder 取较小区间以制造大量相同 SortOrder，锻炼「稳定排序」语义。
func genAliasRule() *rapid.Generator[AliasRule] {
	return rapid.Custom(func(t *rapid.T) AliasRule {
		isRegex := rapid.Bool().Draw(t, "isRegex")
		var pattern string
		if isRegex {
			pattern = genAliasRegexPattern().Draw(t, "pattern")
		} else {
			pattern = genAliasName().Draw(t, "pattern")
		}
		return AliasRule{
			Pattern:    pattern,
			IsRegex:    isRegex,
			TargetName: genAliasTargetName().Draw(t, "targetName"),
			TargetDesc: genAliasTargetDesc().Draw(t, "targetDesc"),
			SortOrder:  rapid.IntRange(-2, 3).Draw(t, "sortOrder"),
		}
	})
}

// refAliasMatch 是与被测实现解耦的独立名称匹配参考：
//   - 非正则：区分大小写的精确相等；
//   - 正则：以 `\A(?:pattern)\z` 完整匹配；模式非法时视为不匹配（与 ApplyAliases
//     对 Match 报错时「跳过该规则」的处理一致）。
func refAliasMatch(pattern string, isRegex bool, name string) bool {
	if !isRegex {
		return pattern == name
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// refApplyAliases 是别名/描述重写的独立参考实现：
// 在副本上按 SortOrder 稳定排序，对每个工具选出首条匹配规则，按需覆盖
// Name（TargetName 非空）/Description（TargetDesc 非空），其余匹配规则忽略。
// 保持工具原有顺序，不修改入参。
func refApplyAliases(tools []ToolDef, aliases []AliasRule) []ToolDef {
	sorted := make([]AliasRule, len(aliases))
	copy(sorted, aliases)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].SortOrder < sorted[j].SortOrder
	})

	out := make([]ToolDef, len(tools))
	copy(out, tools)
	for i := range out {
		tool := &out[i]
		for _, r := range sorted {
			if !refAliasMatch(r.Pattern, r.IsRegex, tool.OriginalName) {
				continue
			}
			if r.TargetName != "" {
				tool.Name = r.TargetName
			}
			if r.TargetDesc != "" {
				tool.Description = r.TargetDesc
			}
			break
		}
	}
	return out
}

// Feature: mcp-proxy-gateway, Property 6: 多别名规则仅应用首条
//
// Validates: Requirements 8.2, 8.3, 8.5
//
// 对任意工具列表与别名规则集合：ApplyAliases 的输出必须与独立参考实现一致——
// 对每个工具，按 sort_order 稳定升序仅采用第一条匹配规则的目标名称/描述
// （TargetName 非空才覆盖 Name —— Req 8.2；TargetDesc 非空才覆盖 Description —— Req 8.3），
// 其余匹配规则一律不生效（Req 8.5）。同时验证纯函数性质：工具顺序不变，
// OriginalName/InputSchema/UpstreamID/Order 等字段不被改写。
//
// 此外构造「两条都匹配同一工具但目标名称不同」的对照场景，故意以逆序提供规则，
// 断言结果恒取 sort_order 更小者的目标名称，直接锤炼「仅首条生效 + 稳定排序」。
func TestProperty6AliasFirstMatchOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := NewRuleEngine()

		tools := rapid.SliceOfN(genAliasToolDef(), 0, 6).Draw(t, "tools")
		aliases := rapid.SliceOfN(genAliasRule(), 0, 8).Draw(t, "aliases")

		got := e.ApplyAliases(tools, aliases)
		want := refApplyAliases(tools, aliases)

		// 长度必须与输入工具数一致（每个工具映射为一个输出）。
		if len(got) != len(tools) {
			t.Fatalf("输出长度应等于输入工具数：got=%d tools=%d", len(got), len(tools))
		}
		if len(got) != len(want) {
			t.Fatalf("输出长度与参考不一致：got=%d want=%d", len(got), len(want))
		}

		for i := range got {
			g := got[i]
			w := want[i]
			src := tools[i]

			// 纯函数：不可改写的字段必须与原工具一致，且顺序保持。
			if g.OriginalName != src.OriginalName {
				t.Fatalf("OriginalName 不应被改写：i=%d got=%q src=%q", i, g.OriginalName, src.OriginalName)
			}
			if g.UpstreamID != src.UpstreamID || g.Order != src.Order {
				t.Fatalf("UpstreamID/Order 不应被改写：i=%d got=(%q,%d) src=(%q,%d)",
					i, g.UpstreamID, g.Order, src.UpstreamID, src.Order)
			}
			if string(g.InputSchema) != string(src.InputSchema) {
				t.Fatalf("InputSchema 不应被改写：i=%d got=%q src=%q", i, string(g.InputSchema), string(src.InputSchema))
			}

			// 仅首条匹配规则生效——Name/Description 必须与独立参考一致。
			if g.Name != w.Name {
				t.Fatalf("仅首条规则的目标名称应生效：i=%d originalName=%q got=%q want=%q\naliases=%+v",
					i, src.OriginalName, g.Name, w.Name, aliases)
			}
			if g.Description != w.Description {
				t.Fatalf("仅首条规则的目标描述应生效：i=%d originalName=%q got=%q want=%q\naliases=%+v",
					i, src.OriginalName, g.Description, w.Description, aliases)
			}
		}

		// 定向对照：两条都匹配同一工具、目标名称不同，结果恒取 sort_order 更小者。
		if len(tools) > 0 {
			idx := rapid.IntRange(0, len(tools)-1).Draw(t, "pickIdx")
			picked := tools[idx]

			lo := rapid.IntRange(0, 5).Draw(t, "loOrder")
			hi := lo + rapid.IntRange(1, 5).Draw(t, "orderDelta") // hi > lo

			winner := AliasRule{
				Pattern:    picked.OriginalName,
				IsRegex:    false,
				TargetName: "winnerName",
				TargetDesc: "winnerDesc",
				SortOrder:  lo,
			}
			loser := AliasRule{
				Pattern:    picked.OriginalName,
				IsRegex:    false,
				TargetName: "loserName",
				TargetDesc: "loserDesc",
				SortOrder:  hi,
			}

			// 故意逆序提供（loser 在前），稳定排序后 winner（sort_order 更小）应排在首位。
			single := []ToolDef{picked}
			res := e.ApplyAliases(single, []AliasRule{loser, winner})
			if len(res) != 1 {
				t.Fatalf("单工具应得到单输出：got=%d", len(res))
			}
			if res[0].Name != "winnerName" {
				t.Fatalf("应取 sort_order 更小者的目标名称：originalName=%q lo=%d hi=%d got=%q want=%q",
					picked.OriginalName, lo, hi, res[0].Name, "winnerName")
			}
			if res[0].Description != "winnerDesc" {
				t.Fatalf("应取 sort_order 更小者的目标描述：originalName=%q lo=%d hi=%d got=%q want=%q",
					picked.OriginalName, lo, hi, res[0].Description, "winnerDesc")
			}
		}
	})
}
