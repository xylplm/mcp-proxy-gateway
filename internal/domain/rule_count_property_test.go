package domain

import (
	"errors"
	"testing"

	"pgregory.net/rapid"
)

// genFilterCount 生成屏蔽规则数量上限属性所需的「当前规则数」current。
//
// 为兼顾随机覆盖与边界覆盖，采用两类来源混合：
//   - 任意整数（rapid.Int），覆盖大范围正负值；
//   - 围绕上限 MaxFilterRulesPerScope 的关键边界值（0、99、100、101、负数等），
//     确保「恰好低于/等于/高于上限」这些临界点被稳定命中而不依赖随机运气。
func genFilterCount() *rapid.Generator[int] {
	boundaries := []int{
		-1, 0, 1,
		MaxFilterRulesPerScope - 2, // 98
		MaxFilterRulesPerScope - 1, // 99：最大允许新增的当前规则数
		MaxFilterRulesPerScope,     // 100：恰好达到上限，应拒绝
		MaxFilterRulesPerScope + 1, // 101
		MaxFilterRulesPerScope + 50,
	}
	return rapid.OneOf(
		rapid.Int(),
		rapid.SampledFrom(boundaries),
	)
}

// Feature: mcp-proxy-gateway, Property 13: 规则数量上限
//
// Validates: Requirements 9.2, 9.9, 13.2, 13.3
//
// 对任意当前规则数 current（覆盖 0、边界 99/100/101、更大值与负数等），
// ValidateFilterCount(current) 当且仅当 current < MaxFilterRulesPerScope（100）时
// 返回 nil（允许新增）；当 current >= MaxFilterRulesPerScope 时，因新增后将超过
// 上限而拒绝，返回 VALIDATION 类别的 APIError（不持久化任何数据）。
//
// 该上限对上游 MCP 级与 API Key 级屏蔽规则共用同一套计数校验逻辑
// （见 Requirements 9.2/9.9 与 13.2/13.3），故验证 ValidateFilterCount 自身的
// 接受/拒绝语义即可覆盖两处。
func TestProperty13FilterRuleCountLimit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		current := genFilterCount().Draw(t, "current")

		err := ValidateFilterCount(current)

		// 期望：仅当当前规则数严格小于上限时才允许新增（返回 nil）。
		shouldAccept := current < MaxFilterRulesPerScope

		if shouldAccept {
			if err != nil {
				t.Fatalf("current=%d (< %d) 应允许新增，但返回错误：%v",
					current, MaxFilterRulesPerScope, err)
			}
			return
		}

		// 当前规则数已达到或超过上限：必须拒绝，且为 VALIDATION 类别的 APIError。
		if err == nil {
			t.Fatalf("current=%d (>= %d) 应拒绝新增，但返回 nil",
				current, MaxFilterRulesPerScope)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != CodeValidation {
			t.Fatalf("current=%d 被拒绝时应返回 VALIDATION 类别的 APIError，实际：%v",
				current, err)
		}
	})
}
