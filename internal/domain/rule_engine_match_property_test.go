package domain

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// safeRegexAtoms 是一组「单独合法」的正则片段，将任意多个片段顺序拼接后仍是
// 合法的正则表达式。用于生成更有意义的合法正则模式（覆盖字符类、量词、分组、
// 选择分支等），从而让正则完整匹配的语义得到充分锻炼。
var safeRegexAtoms = []string{
	"a", "b", "c", "A", "B", "1", "2",
	"[a-z]", "[0-9]", "[A-Za-z]", `\d`, `\w`, ".",
	"a*", "b+", "c?", "(?:xy)", "x|y", "foo", "bar",
}

// genSafeRegex 生成由 safeRegexAtoms 顺序拼接而成的合法正则模式（含空模式）。
func genSafeRegex() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		atoms := rapid.SliceOfN(rapid.SampledFrom(safeRegexAtoms), 0, 4).Draw(t, "atoms")
		return strings.Join(atoms, "")
	})
}

// genRestricted 生成限定字母表（含大小写与数字）上的短字符串，
// 以提高与精确相等模式或简单正则发生「命中」的概率。
func genRestricted() *rapid.Generator[string] {
	alphabet := []string{"a", "b", "c", "A", "B", "1", "2", ""}
	return rapid.Custom(func(t *rapid.T) string {
		parts := rapid.SliceOfN(rapid.SampledFrom(alphabet), 0, 5).Draw(t, "chars")
		return strings.Join(parts, "")
	})
}

// genPattern 生成匹配模式：混合任意字符串（可能含正则元字符、可能非法）、
// 合法正则片段、以及限定字母表短串，覆盖正则/精确、大小写差异、空串等边界。
func genPattern() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.String(),
		genSafeRegex(),
		genRestricted(),
	)
}

// genName 基于给定模式生成工具原始名称：以一定概率直接等于模式（用于触发
// 精确相等为真、以及字面量模式的正则命中），其余为任意字符串或限定字母表短串。
func genName(pattern string) *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(pattern),
		rapid.String(),
		genRestricted(),
	)
}

// Feature: mcp-proxy-gateway, Property 4: 名称匹配语义一致
//
// Validates: Requirements 8.7, 8.8, 9.5, 9.6, 13.5, 13.6
//
// 对任意匹配模式与工具原始名称：
//   - 关闭正则时，Match 的结果等价于「区分大小写的精确相等」(pattern == name)，
//     且不返回错误；
//   - 开启正则时，Match 的结果等价于以 `\A(?:pattern)\z` 编译的正则对 name 的
//     完整匹配（full match）；若模式非法，则 Match 返回 VALIDATION 类别的 APIError
//     且结果为 false（与参考实现编译失败保持一致）。
//
// 由于别名规则、MCP 级屏蔽规则、API Key 级屏蔽规则三处共用同一个 Match 函数，
// 验证 Match 自身的语义一致即可覆盖三处的一致性（见 Requirements 8.7/8.8、
// 9.5/9.6、13.5/13.6）。
func TestProperty4NameMatchSemantics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := NewRuleEngine()

		pattern := genPattern().Draw(t, "pattern")
		name := genName(pattern).Draw(t, "name")
		isRegex := rapid.Bool().Draw(t, "isRegex")

		got, err := e.Match(pattern, isRegex, name)

		if !isRegex {
			// 非正则：等价于区分大小写的精确相等，且永不报错。
			if err != nil {
				t.Fatalf("非正则匹配不应返回错误：pattern=%q name=%q err=%v", pattern, name, err)
			}
			want := pattern == name
			if got != want {
				t.Fatalf("非正则匹配语义不一致：pattern=%q name=%q got=%v want=%v",
					pattern, name, got, want)
			}
			return
		}

		// 正则：以独立的参考实现（同样的完整匹配包裹）作为对照。
		ref, refErr := regexp.Compile(`\A(?:` + pattern + `)\z`)
		if refErr != nil {
			// 模式非法：Match 必须返回错误、结果为 false，且为 VALIDATION 类 APIError。
			if err == nil {
				t.Fatalf("非法正则应返回错误：pattern=%q name=%q got=%v", pattern, name, got)
			}
			if got {
				t.Fatalf("非法正则的匹配结果应为 false：pattern=%q name=%q", pattern, name)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != CodeValidation {
				t.Fatalf("非法正则应返回 VALIDATION 类别的 APIError：pattern=%q err=%v", pattern, err)
			}
			return
		}

		// 模式合法：Match 不应报错，且其结果与参考实现的完整匹配一致。
		if err != nil {
			t.Fatalf("合法正则不应返回错误：pattern=%q name=%q err=%v", pattern, name, err)
		}
		want := ref.MatchString(name)
		if got != want {
			t.Fatalf("正则完整匹配语义不一致：pattern=%q name=%q got=%v want=%v",
				pattern, name, got, want)
		}
	})
}
