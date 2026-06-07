package syncsvc

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现 cron 表达式校验的属性测试（任务 10.2，Property 18）。
//
// 通过「按字段合法范围构造的合法表达式」与「由空串/错误字段数/字段越界/非法 token
// 构造的非法表达式」分别覆盖双向判定。生成器按构造保证期望结果确定，不依赖被测
// 实现，从而独立验证 ValidateCron 的「合法→放行、非法→拒绝」两个方向。
//
// 所有标识符统一使用 p18 前缀，避免与 scheduler_test.go 中的辅助函数（asAPIError 等）
// 命名冲突。

// p18FieldRange 描述单个 cron 字段的合法取值范围与一个明确越界值。
type p18FieldRange struct {
	min  int // 合法取值下限（用于构造合法字段）
	max  int // 合法取值上限（用于构造合法字段）
	over int // 一个明确高于解析器接受上限的取值（用于构造越界字段）
}

// 各 cron 字段（含可选秒）的合法范围与越界基准值。
//
// 注意：robfig/cron 的星期字段同时接受 7 表示周日，因此其越界基准取 8 起，
// 以保证「越界值确定非法」与解析器实际行为一致。
var (
	p18Second = p18FieldRange{min: 0, max: 59, over: 60}
	p18Minute = p18FieldRange{min: 0, max: 59, over: 60}
	p18Hour   = p18FieldRange{min: 0, max: 23, over: 24}
	p18Dom    = p18FieldRange{min: 1, max: 31, over: 32}
	p18Month  = p18FieldRange{min: 1, max: 12, over: 13}
	p18Dow    = p18FieldRange{min: 0, max: 6, over: 8}
)

// p18ValidDescriptors 是一组合法的预定义描述符（含 @every 形式），均应通过校验。
var p18ValidDescriptors = []string{
	"@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly",
	"@every 1h", "@every 30m", "@every 1h30m", "@every 90s",
}

// p18IllegalTokens 是一组在任意 cron 字段中都不合法的 token：含非法字符、残缺的
// 步长/区间语法等，放入任一字段都会使解析失败。
var p18IllegalTokens = []string{
	"!!", "1.5", "@@", "%", "()", "1..2", "abc!", "*/", "/5", "1-", "-3", "1#2",
}

// p18IllegalExprs 是一组整体非法的 cron 字符串：非法描述符、步长为 0、缺失时长、
// 无法识别的字面量等，均应被拒绝。
var p18IllegalExprs = []string{
	"not-a-cron",
	"hello world",
	"*/0 * * * *",
	"@unknown",
	"@every",
	"@every abc",
	"@every 1h /",
}

// p18GenValidField 在字段合法范围 [min, max] 内构造一个合法的字段表达式，
// 覆盖通配符、单值、步长、区间四种常见写法。
func p18GenValidField(min, max int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		switch rapid.IntRange(0, 3).Draw(t, "fieldKind") {
		case 0:
			return "*"
		case 1:
			return strconv.Itoa(rapid.IntRange(min, max).Draw(t, "single"))
		case 2:
			step := rapid.IntRange(1, max).Draw(t, "step")
			return "*/" + strconv.Itoa(step)
		default:
			lo := rapid.IntRange(min, max).Draw(t, "rangeLo")
			hi := rapid.IntRange(lo, max).Draw(t, "rangeHi")
			return strconv.Itoa(lo) + "-" + strconv.Itoa(hi)
		}
	})
}

// p18OrderedRanges 返回与 cron 字段顺序一致的范围切片（可选是否含秒字段）。
func p18OrderedRanges(withSeconds bool) []p18FieldRange {
	ranges := []p18FieldRange{p18Minute, p18Hour, p18Dom, p18Month, p18Dow}
	if withSeconds {
		ranges = append([]p18FieldRange{p18Second}, ranges...)
	}
	return ranges
}

// p18GenValidCron 构造一个合法的 cron 表达式：约 1/5 概率为预定义描述符，否则按
// 字段顺序生成 5 段或带秒的 6 段表达式，并可能附加首尾空白（应被裁剪后仍通过）。
func p18GenValidCron() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		if rapid.IntRange(0, 4).Draw(t, "useDescriptor") == 0 {
			return rapid.SampledFrom(p18ValidDescriptors).Draw(t, "descriptor")
		}
		ranges := p18OrderedRanges(rapid.Bool().Draw(t, "withSeconds"))
		fields := make([]string, len(ranges))
		for i, r := range ranges {
			fields[i] = p18GenValidField(r.min, r.max).Draw(t, "validField")
		}
		expr := strings.Join(fields, " ")
		// 随机添加首尾空白，验证 ValidateCron 会裁剪后仍判定为合法（Req 7.3）。
		if rapid.Bool().Draw(t, "pad") {
			expr = "  " + expr + "  "
		}
		return expr
	})
}

// p18GenWrongFieldCount 构造字段数量不为 5 或 6 的表达式（必非法，非法性仅来源于
// 字段数量）：使用确定合法的简单 token，字段数取自 {1,2,3,4,7,8}。
func p18GenWrongFieldCount() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.SampledFrom([]int{1, 2, 3, 4, 7, 8}).Draw(t, "fieldCount")
		fields := make([]string, n)
		for i := range fields {
			fields[i] = rapid.SampledFrom([]string{"*", "0", "1", "*/2", "1-2"}).Draw(t, "token")
		}
		return strings.Join(fields, " ")
	})
}

// p18GenOutOfRangeField 构造字段数量合法但某一字段取值明确越界的表达式（必非法）。
func p18GenOutOfRangeField() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		ranges := p18OrderedRanges(rapid.Bool().Draw(t, "withSeconds"))
		fields := make([]string, len(ranges))
		for i, r := range ranges {
			fields[i] = p18GenValidField(r.min, r.max).Draw(t, "validField")
		}
		idx := rapid.IntRange(0, len(ranges)-1).Draw(t, "corruptIdx")
		over := rapid.IntRange(ranges[idx].over, ranges[idx].over+40).Draw(t, "overValue")
		fields[idx] = strconv.Itoa(over)
		return strings.Join(fields, " ")
	})
}

// p18GenIllegalTokenField 构造字段数量合法但某一字段含非法 token 的表达式（必非法）。
func p18GenIllegalTokenField() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		ranges := p18OrderedRanges(rapid.Bool().Draw(t, "withSeconds"))
		fields := make([]string, len(ranges))
		for i, r := range ranges {
			fields[i] = p18GenValidField(r.min, r.max).Draw(t, "validField")
		}
		idx := rapid.IntRange(0, len(ranges)-1).Draw(t, "corruptIdx")
		fields[idx] = rapid.SampledFrom(p18IllegalTokens).Draw(t, "illegalToken")
		return strings.Join(fields, " ")
	})
}

// p18GenInvalidCron 汇总各类非法表达式：空/纯空白、字段数错误、字段越界、含非法
// token、以及整体非法的字符串。
func p18GenInvalidCron() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{"", " ", "   ", "\t", " \t "}),
		p18GenWrongFieldCount(),
		p18GenOutOfRangeField(),
		p18GenIllegalTokenField(),
		rapid.SampledFrom(p18IllegalExprs),
	)
}

// p18AssertValid 断言合法 cron 表达式通过校验（返回 nil）。
func p18AssertValid(t *rapid.T, expr string) {
	t.Helper()
	if err := ValidateCron(expr); err != nil {
		t.Fatalf("合法 cron 表达式应通过校验：expr=%q err=%v", expr, err)
	}
}

// p18AssertInvalid 断言非法 cron 表达式被拒绝，且返回 Code=VALIDATION、携带字段级
// 说明 sync.cron 的 *domain.APIError。
func p18AssertInvalid(t *rapid.T, expr string) {
	t.Helper()
	err := ValidateCron(expr)
	if err == nil {
		t.Fatalf("非法 cron 表达式应被拒绝，却通过了校验：expr=%q", expr)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("非法 cron 应返回 *domain.APIError：expr=%q err=%v", expr, err)
	}
	if apiErr.Code != domain.CodeValidation {
		t.Fatalf("非法 cron 应返回 VALIDATION 错误：expr=%q code=%q", expr, apiErr.Code)
	}
	if _, ok := apiErr.Fields["sync.cron"]; !ok {
		t.Fatalf("非法 cron 的校验错误应包含字段级说明 sync.cron：expr=%q fields=%v", expr, apiErr.Fields)
	}
}

// Feature: mcp-proxy-gateway, Property 18: cron 表达式校验
//
// Validates: Requirements 7.3, 7.4
//
// 对任意字符串：当且仅当其为标准 cron 格式（5 段、带秒的 6 段，或预定义描述符）
// 且每个字段取值落在合法范围内时，ValidateCron 通过校验并返回 nil（语义上允许
// 持久化，Req 7.3）；否则（空、字段数量不符、字段取值越界、含非法 token）必须被
// 拒绝并返回 Code=VALIDATION 且携带字段级说明 sync.cron 的 *domain.APIError（Req 7.4）。
//
// 合法方向与非法方向分别由按构造保证期望结果的生成器驱动，期望结果不依赖被测实现。
func TestProperty18CronExpressionValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// 合法方向：构造的合法 cron 必须通过校验。
		validExpr := p18GenValidCron().Draw(t, "validCron")
		p18AssertValid(t, validExpr)

		// 非法方向：构造的非法 cron 必须被拒绝且返回 VALIDATION 字段级错误。
		invalidExpr := p18GenInvalidCron().Draw(t, "invalidCron")
		p18AssertInvalid(t, invalidExpr)
	})
}
