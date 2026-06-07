package domain

import (
	"fmt"
	"regexp"
	"sync"
)

// engine 是 Rule_Engine 接口的具体实现。
//
// 设计上规则引擎为纯函数式、无业务副作用（详见设计文档「规则引擎」边界说明），
// 仅持有一个用于缓存已编译正则的 sync.Map，以避免重复编译相同模式带来的开销。
//
// 本类型由多个任务分阶段实现：
//   - 任务 3.1（当前）：名称匹配 Match 与构造函数 NewRuleEngine。
//   - 任务 3.3：规则校验 ValidateAlias / ValidateFilter（后续在本文件补充）。
//   - 任务 3.6：屏蔽规则应用 ApplyFilters（后续在本文件补充）。
//   - 任务 3.8：别名/描述重写 ApplyAliases（后续在本文件补充）。
//
// 待上述方法全部补齐后，可在本文件加入编译期断言
// `var _ Rule_Engine = (*engine)(nil)` 以确保完整实现 Rule_Engine 接口。
type engine struct {
	// regexCache 缓存已编译的正则，键为原始 pattern 字符串、值为 *regexp.Regexp。
	// 缓存的是「完整匹配」语义下包裹后的正则（见 compileFullMatch）。
	regexCache sync.Map
}

// NewRuleEngine 构造一个规则引擎实例。
//
// 返回具体类型 *engine：当前阶段仅实现了 Match 方法，待后续任务补齐其余接口
// 方法后，调用方即可将其作为 Rule_Engine 接口使用。
func NewRuleEngine() *engine {
	return &engine{}
}

// Match 进行名称匹配。
//
//   - 当 isRegex 为 true 时，将 pattern 作为正则表达式对 originalName 进行完整匹配
//     （full match）：整个 originalName 必须被 pattern 完全覆盖，而非部分匹配。
//     若 pattern 不是合法的正则表达式，返回 VALIDATION 类别的 APIError。
//   - 当 isRegex 为 false 时，将 pattern 与 originalName 进行区分大小写的精确相等
//     比较（pattern == originalName）。
//
// 该匹配语义在别名规则、MCP 级屏蔽规则、API Key 级屏蔽规则三处保持一致
// （Req 8.7/8.8、9.5/9.6、13.5/13.6）。
func (e *engine) Match(pattern string, isRegex bool, originalName string) (bool, error) {
	if !isRegex {
		// 非正则：区分大小写的精确相等比较。
		return pattern == originalName, nil
	}

	re, err := e.compileFullMatch(pattern)
	if err != nil {
		return false, err
	}
	// 正则已被包裹为 \A(?:pattern)\z，MatchString 即表示完整匹配。
	return re.MatchString(originalName), nil
}

// compileFullMatch 返回 pattern 对应的「完整匹配」正则，并对编译结果做缓存。
//
// 实现要点：
//   - 将用户模式包裹为 `\A(?:pattern)\z` 以保证完整匹配。使用非捕获分组 (?:...)
//     避免诸如 `a|b` 这类含顶层选择分支的模式与锚点结合后语义错误；使用 \A 与 \z
//     而非 ^ 与 $，可避免多行模式下的歧义（始终锚定整段文本的首尾）。
//   - 以原始 pattern 作为缓存键，命中则直接复用，避免重复编译。
//   - 非法正则返回 VALIDATION 类别的 APIError，并在字段级错误中携带具体原因。
func (e *engine) compileFullMatch(pattern string) (*regexp.Regexp, error) {
	if v, ok := e.regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}

	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return nil, NewValidationError(
			fmt.Sprintf("非法的正则表达式: %q", pattern),
			map[string]string{"pattern": err.Error()},
		)
	}

	// 并发场景下可能有多个 goroutine 同时编译同一模式，使用 LoadOrStore 保证
	// 缓存中最终只保留一份并返回实际生效的实例。
	actual, _ := e.regexCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}
