package domain

import "sort"

// ApplyAliases 应用别名/描述重写规则。
//
// 对工具列表中的每个工具，按别名规则的 SortOrder 升序（稳定排序）选出第一条
// 匹配该工具 OriginalName 的别名规则并应用其目标名称/目标描述：
//   - 若该规则的 TargetName 非空，则替换工具对外暴露的 Name（Req 8.2）。
//   - 若该规则的 TargetDesc 非空，则替换工具的 Description（Req 8.3）。
//   - 仅应用首条匹配规则，其余匹配规则一律忽略（Req 8.5）。
//
// 匹配语义：以工具的 OriginalName（上游原始名称）作为匹配对象，复用 Match——
// 正则规则按完整匹配（full match），非正则规则按区分大小写的精确相等
// （与屏蔽规则一致，Req 8.7/8.8）。
//
// 行为约束：
//   - 纯函数：不修改入参 tools 的任何元素，返回新的 ToolDef 切片；工具原有顺序保持不变。
//   - 排序稳定：相同 SortOrder 的别名规则保持其在 aliases 中的原相对顺序；
//     排序在副本上进行，不改动调用方传入的 aliases 切片顺序。
//   - 某条别名规则未匹配任何工具时不视为错误，规则被静默保留（Req 8.4）。
//   - 非法正则规则（Match 返回 error）视为不匹配并跳过，不影响后续规则与其他工具。
func (e *engine) ApplyAliases(tools []ToolDef, aliases []AliasRule) []ToolDef {
	// 复制规则切片后再稳定排序，避免修改调用方传入的 aliases 顺序。
	// SliceStable 保证相同 SortOrder 的规则维持原相对顺序。
	sorted := make([]AliasRule, len(aliases))
	copy(sorted, aliases)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].SortOrder < sorted[j].SortOrder
	})

	// 复制工具切片，保证纯函数语义：不修改入参元素，且保持工具原有顺序。
	// 仅替换副本的 Name/Description（字符串值替换，非原地修改），
	// 不会触及与原切片共享的 InputSchema 底层字节。
	result := make([]ToolDef, len(tools))
	copy(result, tools)

	for i := range result {
		tool := &result[i]
		for _, rule := range sorted {
			matched, err := e.Match(rule.Pattern, rule.IsRegex, tool.OriginalName)
			if err != nil {
				// 非法正则规则视为不匹配，跳过该规则继续尝试后续规则。
				continue
			}
			if !matched {
				continue
			}
			// 命中首条匹配规则：按需替换对外名称与描述。
			if rule.TargetName != "" {
				tool.Name = rule.TargetName
			}
			if rule.TargetDesc != "" {
				tool.Description = rule.TargetDesc
			}
			// 仅应用首条匹配规则，其余匹配规则忽略。
			break
		}
	}

	return result
}
