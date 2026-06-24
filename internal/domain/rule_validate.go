package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// 规则校验相关的边界常量，集中定义以避免散落的魔法数字，并便于规则管理任务
// （Req 8、9、13）与属性测试（任务 3.4、3.5）复用。
const (
	// patternMinRunes 为匹配模式的最小长度（字符数），即模式不能为空（Req 8.9、9.8、13.4）。
	patternMinRunes = 1
	// patternMaxRunes 为匹配模式的最大长度（字符数）上限（Req 8.1/8.9、9.1/9.8、13.1/13.4）。
	patternMaxRunes = 200
	// aliasTargetNameMaxRunes 为别名目标名称的最大长度（字符数）上限（Req 8.1）。
	aliasTargetNameMaxRunes = 100
	// aliasTargetDescMaxRunes 为别名目标描述的最大长度（字符数）上限（Req 8.1）。
	aliasTargetDescMaxRunes = 1024
	// MaxFilterRulesPerScope 为同一作用域（单个上游 MCP 或单个 API Key）允许维护的
	// 屏蔽规则数量上限（Req 9.2、9.9、13.2、13.3）。导出以供规则管理任务（9.x、14.3）复用。
	MaxFilterRulesPerScope = 100
	// MaxToolPolicyRules 为工具策略规则总量上限。策略按每次调用读取并匹配，保持小而可控。
	MaxToolPolicyRules = 100
	// MaxToolPolicyRiskTags 为单条工具策略允许携带的自定义风险标签数量。
	MaxToolPolicyRiskTags = 8
	// MaxToolPolicyRiskTagRunes 为单个自定义风险标签的最大字符数。
	MaxToolPolicyRiskTagRunes = 24
	// MaxToolPolicyCacheTTLSeconds 为调用结果短 TTL 缓存上限，避免误配置造成陈旧数据长期驻留。
	MaxToolPolicyCacheTTLSeconds = 3600
)

// ValidateAlias 在保存前校验别名规则（Req 8.1、8.9）。
//
// 校验项：
//   - 匹配模式长度：字符数须落在 [1, 200] 内（为空或超长均拒绝）。
//   - 正则合法性：当 IsRegex 为 true 时，Pattern 须可编译为合法正则（按与 Match
//     一致的「完整匹配」包裹形式校验，保证校验通过的模式在匹配阶段不会再报错）。
//   - 目标字段：目标名称与目标描述至少提供其一；若提供目标名称，其长度须不超过
//     100 个字符；若提供目标描述，其长度须不超过 1024 个字符。
//
// 校验通过返回 nil；否则返回携带字段级说明的校验类 APIError（Code=VALIDATION），
// 调用方据此拒绝保存、不持久化任何数据（语义上不持久化由调用方保证）。
func (e *engine) ValidateAlias(r AliasRule) error {
	fields := make(map[string]string)

	// 模式长度与正则合法性校验（与屏蔽规则共用同一套逻辑）。
	e.validatePattern(r.Pattern, r.IsRegex, fields)

	// 目标名称与目标描述至少提供其一（Req 8.9）。
	hasName := r.TargetName != ""
	hasDesc := r.TargetDesc != ""
	if !hasName && !hasDesc {
		fields["targetName"] = "目标名称与目标描述至少提供其一"
		fields["targetDesc"] = "目标名称与目标描述至少提供其一"
	}

	// 若提供目标名称，则其长度须不超过 100 个字符（Req 8.1）。
	// 目标名称为非空字符串时其字符数天然不小于 1，故下限无需单独校验。
	if hasName {
		if n := utf8.RuneCountInString(r.TargetName); n > aliasTargetNameMaxRunes {
			fields["targetName"] = fmt.Sprintf(
				"目标名称长度 %d 超过上限 %d 个字符", n, aliasTargetNameMaxRunes,
			)
		}
	}

	// 若提供目标描述，则其长度须不超过 1024 个字符（Req 8.1）。
	if hasDesc {
		if n := utf8.RuneCountInString(r.TargetDesc); n > aliasTargetDescMaxRunes {
			fields["targetDesc"] = fmt.Sprintf(
				"目标描述长度 %d 超过上限 %d 个字符", n, aliasTargetDescMaxRunes,
			)
		}
	}

	if len(fields) > 0 {
		return NewValidationError("别名规则校验失败", fields)
	}
	return nil
}

// ValidateFilter 在保存前对单条屏蔽规则做字段级校验（Req 9.7、9.8、13.4）。
//
// 校验项：
//   - 匹配模式长度：字符数须落在 [1, 200] 内（为空或超长均拒绝）。
//   - 正则合法性：当 IsRegex 为 true 时，Pattern 须可编译为合法正则。
//
// 注意：屏蔽规则「数量上限 100」属于上下文计数校验（需结合已有规则数量），不在本
// 单规则字段校验内处理，由独立的 ValidateFilterCount 负责（Req 9.2、9.9、13.2、13.3）。
//
// 校验通过返回 nil；否则返回携带字段级说明的校验类 APIError（Code=VALIDATION）。
func (e *engine) ValidateFilter(r FilterRule) error {
	fields := make(map[string]string)

	e.validatePattern(r.Pattern, r.IsRegex, fields)

	if len(fields) > 0 {
		return NewValidationError("屏蔽规则校验失败", fields)
	}
	return nil
}

// ValidateToolPolicy 在保存前校验工具策略规则。
func (e *engine) ValidateToolPolicy(r ToolPolicyRule) error {
	fields := make(map[string]string)

	e.validatePattern(r.Pattern, r.IsRegex, fields)

	if r.RoutingStrategy != "" && !ValidToolRoutingStrategy(r.RoutingStrategy) {
		fields["routingStrategy"] = "路由策略只能是 priority_fill 或 round_robin"
	}

	if r.CacheEnabled {
		if r.CacheTTLSeconds <= 0 {
			fields["cacheTtlSeconds"] = "启用缓存时 TTL 必须大于 0 秒"
		} else if r.CacheTTLSeconds > MaxToolPolicyCacheTTLSeconds {
			fields["cacheTtlSeconds"] = fmt.Sprintf("缓存 TTL 不能超过 %d 秒", MaxToolPolicyCacheTTLSeconds)
		}
	} else if r.CacheTTLSeconds < 0 {
		fields["cacheTtlSeconds"] = "缓存 TTL 不能为负数"
	}

	if len(r.RiskTags) > MaxToolPolicyRiskTags {
		fields["riskTags"] = fmt.Sprintf("自定义风险标签不能超过 %d 个", MaxToolPolicyRiskTags)
	}
	for i, tag := range r.RiskTags {
		trimmed := strings.TrimSpace(tag)
		key := fmt.Sprintf("riskTags[%d]", i)
		if trimmed == "" {
			fields[key] = "风险标签不能为空"
			continue
		}
		if n := utf8.RuneCountInString(trimmed); n > MaxToolPolicyRiskTagRunes {
			fields[key] = fmt.Sprintf("风险标签长度 %d 超过上限 %d 个字符", n, MaxToolPolicyRiskTagRunes)
		}
	}

	if len(fields) > 0 {
		return NewValidationError("工具策略规则校验失败", fields)
	}
	return nil
}

// ValidateFilterCount 校验在某作用域（单个上游 MCP 或单个 API Key）内新增一条屏蔽规则
// 是否会使该作用域的屏蔽规则总数超过上限（Req 9.2、9.9、13.2、13.3）。
//
// 参数 current 为该作用域当前已存在的屏蔽规则数量。由于本次校验针对「新增一条」规则，
// 当 current 已达到或超过上限（即新增后将超过 100 条）时返回校验类 APIError 予以拒绝、
// 不持久化任何数据；否则返回 nil。
//
// 该函数为独立导出的纯函数，供 MCP 级与 API Key 级屏蔽规则管理任务（9.x、14.3）复用。
func ValidateFilterCount(current int) error {
	if current >= MaxFilterRulesPerScope {
		return NewValidationError(
			fmt.Sprintf("屏蔽规则数量已达上限 %d 条，无法继续新增", MaxFilterRulesPerScope),
			map[string]string{
				"count": fmt.Sprintf(
					"当前规则数 %d，新增后将超过上限 %d", current, MaxFilterRulesPerScope,
				),
			},
		)
	}
	return nil
}

// validatePattern 校验匹配模式的长度与（必要时）正则合法性，将字段级错误写入 fields。
//
// 别名规则与屏蔽规则共用同一套模式校验规则，故抽取为私有方法：
//   - 模式长度按字符数（rune）计，须落在 [patternMinRunes, patternMaxRunes] 内；
//     为空（长度为 0）或超长均记为字段级错误（Req 8.9、9.8、13.4）。
//   - 仅当模式长度合法且 isRegex 为 true 时才尝试编译正则，避免对超长/空模式做无谓编译；
//     编译复用 engine 的 compileFullMatch（与 Match 的完整匹配语义一致），保证校验通过的
//     模式在后续匹配阶段不会再因正则非法而报错（Req 8.9、9.7、13.4）。
func (e *engine) validatePattern(pattern string, isRegex bool, fields map[string]string) {
	n := utf8.RuneCountInString(pattern)
	switch {
	case n < patternMinRunes:
		fields["pattern"] = "匹配模式不能为空"
		return
	case n > patternMaxRunes:
		fields["pattern"] = fmt.Sprintf(
			"匹配模式长度 %d 超出合法范围 [%d, %d]", n, patternMinRunes, patternMaxRunes,
		)
		return
	}

	// 长度合法后，正则模式还须可编译为合法正则。
	if isRegex {
		if _, err := e.compileFullMatch(pattern); err != nil {
			// compileFullMatch 已返回 VALIDATION 类 APIError，其字段级原因位于 Fields["pattern"]；
			// 此处统一改写为面向保存场景的简明说明，避免泄露内部包裹细节。
			fields["pattern"] = fmt.Sprintf("匹配模式不是合法的正则表达式: %q", pattern)
		}
	}
}
