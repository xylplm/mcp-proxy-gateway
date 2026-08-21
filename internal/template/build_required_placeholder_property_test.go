package template

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 26（模板必填占位参数校验），
// 针对 Market.BuildUpstream 进行属性测试，验证需求 14.10。
//
// 文件内的生成器与辅助标识符均使用 p26 前缀命名，以避免与同包其它测试
// （build_test.go、market_test.go、market_search_property_test.go）中的标识符冲突。

// p26TemplateIDsWithRequired 返回内置模板中至少含一个必填占位参数的模板 ID，
// 作为属性测试的取样空间（无占位参数的模板不在本属性的关注范围内）。
func p26TemplateIDsWithRequired(m *Market) []string {
	ids := make([]string, 0)
	for _, t := range m.List() {
		for _, ph := range t.Placeholders {
			if ph.Required {
				ids = append(ids, t.ID)
				break
			}
		}
	}
	return ids
}

// p26ValidValue 为某占位参数生成一个满足其校验规则的合法取值，
// 使「已提供的参数」不会因自身非法而干扰对「缺失必填参数」的断言。
//
// 内置模板的占位规则仅涉及 Kind（url/secret/int/string）与长度约束（无正则），
// 因此此处只需按类别与长度生成即可。
func p26ValidValue(t *rapid.T, ph Placeholder, label string) string {
	r := ph.Rule
	switch r.Kind {
	case ParamURL:
		// 合法绝对 URL（含协议与主机），长度远小于任何 MaxLen 约束。
		return "https://example.com/mcp"
	case ParamInt:
		return strconv.Itoa(rapid.IntRange(0, 100000).Draw(t, label))
	default: // ParamString / ParamSecret
		// 命名避开内置 min/max，否则同作用域内无法再调用这两个内置函数。
		minLen := max(r.MinLen, 1)
		maxLen := r.MaxLen
		if maxLen == 0 || maxLen > 32 {
			maxLen = 32
		}
		maxLen = max(maxLen, minLen)
		n := rapid.IntRange(minLen, maxLen).Draw(t, label)
		return strings.Repeat("a", n)
	}
}

// Feature: mcp-proxy-gateway, Property 26: 模板必填占位参数校验
//
// Validates: Requirements 14.10
//
// 对任意基于快捷模板的创建请求，若缺失任一必填占位参数（未提供该键或仅填空白），
// 则：
//   - 创建被拒绝（返回非 nil 错误，且为 VALIDATION 类 APIError）；
//   - 不持久化任何配置（返回的 UpstreamConfig 为零值）；
//   - 错误指明每个缺失的必填参数名称（错误字段以占位参数名为键）。
//
// 已提供的其余必填参数均生成合法取值，确保错误字段恰为被故意省略的那些必填参数，
// 既验证「指明缺失参数」（不漏），也验证「合法参数不被误报」（不多）。
func TestProperty26TemplateRequiredPlaceholderValidation(t *testing.T) {
	market := New()
	ids := p26TemplateIDsWithRequired(market)
	if len(ids) == 0 {
		t.Fatal("内置模板中应存在含必填占位参数的模板，测试前提不成立")
	}

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.SampledFrom(ids).Draw(t, "templateID")
		tmpl, err := market.Get(id)
		if err != nil {
			t.Fatalf("Get(%q) 不应出错：%v", id, err)
		}

		// 收集必填占位参数索引，并决定哪些「省略」（缺失）哪些「提供合法值」。
		requiredIdx := make([]int, 0, len(tmpl.Placeholders))
		for i, ph := range tmpl.Placeholders {
			if ph.Required {
				requiredIdx = append(requiredIdx, i)
			}
		}

		// 对每个必填参数抽取是否省略；保证至少省略一个（否则强制省略一个）。
		omit := make([]bool, len(requiredIdx))
		anyOmitted := false
		for k := range requiredIdx {
			omit[k] = rapid.Bool().Draw(t, fmt.Sprintf("omit-%d", k))
			if omit[k] {
				anyOmitted = true
			}
		}
		if !anyOmitted {
			forced := rapid.IntRange(0, len(requiredIdx)-1).Draw(t, "forcedOmit")
			omit[forced] = true
		}

		// 构造取值：省略的必填参数以「缺键」或「空白值」两种方式之一表达；
		// 其余必填参数与可选参数均填入合法值。
		values := make(map[string]string)
		wantMissing := make(map[string]bool)
		for k, idx := range requiredIdx {
			ph := tmpl.Placeholders[idx]
			if omit[k] {
				wantMissing[ph.Name] = true
				// 50% 概率显式置空白（含空格），50% 概率根本不提供该键。
				if rapid.Bool().Draw(t, "blankVal-"+ph.Name) {
					blanks := []string{"", "   ", "\t", " \n "}
					values[ph.Name] = rapid.SampledFrom(blanks).Draw(t, "blank-"+ph.Name)
				}
				continue
			}
			values[ph.Name] = p26ValidValue(t, ph, "val-"+ph.Name)
		}
		// 为可选占位参数（若有）也填合法值，避免无关噪声。
		for i, ph := range tmpl.Placeholders {
			if !ph.Required {
				values[ph.Name] = p26ValidValue(t, ph, fmt.Sprintf("optval-%d", i))
			}
		}

		cfg, err := market.BuildUpstream(id, BuildInput{
			Name:   "属性测试上游",
			Values: values,
		})

		// 断言 1：创建被拒绝（返回错误）。
		if err == nil {
			t.Fatalf("缺失必填参数 %v 时应被拒绝，但成功返回：%+v", keysOf(wantMissing), cfg)
		}

		// 断言 2：不持久化任何配置（返回零值 UpstreamConfig）。
		if !p26IsZeroConfig(cfg) {
			t.Fatalf("缺失必填参数时不应生成任何配置，但返回了：%+v", cfg)
		}

		// 断言 3：错误为 VALIDATION 类 APIError。
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
		}
		if apiErr.Code != domain.CodeValidation {
			t.Fatalf("期望 VALIDATION，got=%s", apiErr.Code)
		}

		// 断言 4：每个被省略的必填参数名都出现在错误字段中（指明缺失参数名称，不漏）。
		for name := range wantMissing {
			if _, ok := apiErr.Fields[name]; !ok {
				t.Fatalf("缺失必填参数 %q 未在错误字段中指明，fields=%v", name, apiErr.Fields)
			}
		}

		// 断言 5：已提供合法值的必填参数不应被误报为缺失/非法（不多）。
		for k, idx := range requiredIdx {
			if omit[k] {
				continue
			}
			name := tmpl.Placeholders[idx].Name
			if _, ok := apiErr.Fields[name]; ok {
				t.Fatalf("合法的必填参数 %q 不应报错，fields=%v", name, apiErr.Fields)
			}
		}
	})
}

// keysOf 返回 map 的键集合，用于错误信息展示。
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// p26IsZeroConfig 判定上游配置是否为零值（即未生成任何可持久化配置）。
//
// UpstreamConfig 含 map 字段无法直接用 == 比较，故逐字段判定。
func p26IsZeroConfig(cfg domain.UpstreamConfig) bool {
	return cfg.Name == "" &&
		cfg.Transport == "" &&
		cfg.ConnParams == nil &&
		cfg.Credential == "" &&
		!cfg.Enabled &&
		cfg.SortOrder == 0 &&
		!cfg.AutoSync
}
