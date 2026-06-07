package domain

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// 本文件实现规则校验的属性测试（任务 3.4，Property 12）。
//
// 复用 rule_engine_match_property_test.go 中已有的 genSafeRegex / genRestricted
// 生成器，并补充若干面向「校验边界」的生成器（空模式、长度边界、超长、非法正则、
// 目标字段边界等），再以独立的参考判定（不依赖被测实现）推导期望结果，
// 从而对生成的任意规则同时验证「合法→放行、非法→拒绝」两个方向。

// invalidRegexAtoms 是一组「确定非法」的正则片段：作为正则编译时必然失败。
// 用于让正则非法分支得到充分锻炼（与合法正则、空模式、超长模式互补）。
var invalidRegexAtoms = []string{
	"[", "(", "*", "+", "?", "a)", "(?P<", `\`, "[a-", "(?", "x{2,1}",
}

// genMultiByteRun 生成由单个字符重复 n 次构成的字符串，字符集混入多字节
// 中文字符，用于验证长度按「字符数（rune）」而非「字节数」计量。
// 选用的字符均为正则字面量，重复后仍是合法正则，从而让长度成为唯一判定因素。
func genMultiByteRun(minRunes, maxRunes int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(minRunes, maxRunes).Draw(t, "runeCount")
		char := rapid.SampledFrom([]string{"a", "x", "好", "中", "字", "测"}).Draw(t, "char")
		return strings.Repeat(char, n)
	})
}

// genValidationPattern 生成用于校验测试的匹配模式，覆盖以下空间：
//   - 空模式（长度 0，必非法）；
//   - 任意字符串（可能含正则元字符、可能为非法正则）；
//   - 合法正则片段拼接（合法且通常长度合法）；
//   - 确定非法的正则片段；
//   - 长度边界附近（195~205 个字符，含多字节）以精确探测上限 200；
//   - 明确超长（201~400 个字符，含多字节，必因长度非法）。
func genValidationPattern() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(""),
		rapid.String(),
		genSafeRegex(),
		rapid.SampledFrom(invalidRegexAtoms),
		genMultiByteRun(195, 205),
		genMultiByteRun(201, 400),
	)
}

// genAliasTargetField 生成别名规则的目标字段（名称或描述），覆盖：
//   - 空串（表示未提供该字段）；
//   - 任意字符串与限定字母表短串；
//   - 上限边界附近与超长（含多字节），用于探测长度上限 maxRunes。
func genAliasTargetField(maxRunes int) *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(""),
		rapid.String(),
		genRestricted(),
		genMultiByteRun(maxRunes-3, maxRunes+5),
	)
}

// patternValidRef 是模式校验的独立参考判定：模式长度须落在 [1, 200] 个字符内，
// 且当 isRegex 为 true 时须能以与实现一致的「完整匹配」包裹形式编译为合法正则。
// 该参考不调用被测实现，作为期望结果的独立对照。
func patternValidRef(pattern string, isRegex bool) bool {
	n := len([]rune(pattern))
	if n < 1 || n > 200 {
		return false
	}
	if isRegex {
		if _, err := regexp.Compile(`\A(?:` + pattern + `)\z`); err != nil {
			return false
		}
	}
	return true
}

// aliasRuleValidRef 是别名规则校验的独立参考判定：模式须合法，目标名称与目标
// 描述至少提供其一；若提供名称其长度须 ≤100，若提供描述其长度须 ≤1024。
func aliasRuleValidRef(r AliasRule) bool {
	if !patternValidRef(r.Pattern, r.IsRegex) {
		return false
	}
	hasName := r.TargetName != ""
	hasDesc := r.TargetDesc != ""
	if !hasName && !hasDesc {
		return false
	}
	if hasName && len([]rune(r.TargetName)) > 100 {
		return false
	}
	if hasDesc && len([]rune(r.TargetDesc)) > 1024 {
		return false
	}
	return true
}

// filterRuleValidRef 是屏蔽规则（单规则字段校验）的独立参考判定：仅取决于模式合法性。
func filterRuleValidRef(r FilterRule) bool {
	return patternValidRef(r.Pattern, r.IsRegex)
}

// assertValidationResult 断言校验返回值与期望一致：
//   - 期望合法（wantValid=true）时，必须返回 nil；
//   - 期望非法时，必须返回 Code=VALIDATION 且携带字段级说明的 *APIError。
func assertValidationResult(t *rapid.T, err error, wantValid bool, desc string) {
	t.Helper()
	if wantValid {
		if err != nil {
			t.Fatalf("合法规则不应被拒绝：%s err=%v", desc, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("非法规则必须被拒绝且不持久化：%s", desc)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("非法规则应返回 *APIError：%s err=%v", desc, err)
	}
	if apiErr.Code != CodeValidation {
		t.Fatalf("非法规则应返回 VALIDATION 类错误：%s code=%s err=%v", desc, apiErr.Code, err)
	}
	if len(apiErr.Fields) == 0 {
		t.Fatalf("校验失败应携带字段级说明：%s err=%v", desc, err)
	}
}

// Feature: mcp-proxy-gateway, Property 12: 规则校验拒绝非法规则
//
// Validates: Requirements 8.9, 9.7, 9.8, 13.4
//
// 对任意生成的别名规则与屏蔽规则：
//   - 若其模式为空、模式长度超过 200 个字符、开启正则但模式非合法正则，或（别名）
//     目标名称与目标描述均缺失、目标名称超长（>100）、目标描述超长（>1024），则
//     ValidateAlias / ValidateFilter 必须返回 Code=VALIDATION 的字段级校验错误
//     （语义上拒绝保存、不持久化任何数据）；
//   - 否则（规则完全合法）必须返回 nil。
//
// 期望结果由不依赖被测实现的独立参考判定（patternValidRef / aliasRuleValidRef /
// filterRuleValidRef）推导，长度统一按字符数（rune）计量以覆盖多字节场景。
// 屏蔽规则在 MCP 级（Req 9.7/9.8）与 API Key 级（Req 13.4）共用同一套字段校验，
// 故验证 ValidateFilter 自身即可覆盖两处。
func TestProperty12RuleValidationRejectsInvalid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := NewRuleEngine()

		// 别名规则：模式 + 目标名称 + 目标描述 三个维度独立生成，覆盖各类合法/非法组合。
		alias := AliasRule{
			Pattern:    genValidationPattern().Draw(t, "aliasPattern"),
			IsRegex:    rapid.Bool().Draw(t, "aliasIsRegex"),
			TargetName: genAliasTargetField(aliasTargetNameMaxRunes).Draw(t, "targetName"),
			TargetDesc: genAliasTargetField(aliasTargetDescMaxRunes).Draw(t, "targetDesc"),
		}
		assertValidationResult(t, e.ValidateAlias(alias), aliasRuleValidRef(alias),
			"别名规则="+aliasDesc(alias))

		// 屏蔽规则：仅模式参与字段校验（数量上限由独立函数负责，不在本属性范围内）。
		filter := FilterRule{
			Pattern: genValidationPattern().Draw(t, "filterPattern"),
			IsRegex: rapid.Bool().Draw(t, "filterIsRegex"),
			Enabled: rapid.Bool().Draw(t, "filterEnabled"),
		}
		assertValidationResult(t, e.ValidateFilter(filter), filterRuleValidRef(filter),
			"屏蔽规则="+filterDesc(filter))
	})
}

// aliasDesc / filterDesc 生成简洁的规则摘要（仅长度与关键标志），避免在失败信息中
// 打印超长模式的完整内容。
func aliasDesc(r AliasRule) string {
	return "{patternLen=" + itoa(len([]rune(r.Pattern))) +
		" isRegex=" + btoa(r.IsRegex) +
		" nameLen=" + itoa(len([]rune(r.TargetName))) +
		" descLen=" + itoa(len([]rune(r.TargetDesc))) + "}"
}

func filterDesc(r FilterRule) string {
	return "{patternLen=" + itoa(len([]rune(r.Pattern))) +
		" isRegex=" + btoa(r.IsRegex) + "}"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func btoa(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
