package domain

// ApplyFilters 应用屏蔽规则，返回未被任一启用规则匹配的工具列表。
//
// 行为约定（对应聚合管线第 3 阶段「MCP 级屏蔽」与第 6 阶段「API Key 级过滤」，
// 两者共用同一套匹配语义）：
//   - 匹配对象始终是工具的 OriginalName（上游原始名称），而非别名重写后的对外名
//     （Req 9.3、13.7）。
//   - 仅启用（Enabled == true）的规则参与匹配；停用（Enabled == false）的规则在
//     匹配过程中被忽略（Req 9.4、13.8）。
//   - 某个工具只要命中任一启用规则即被排除；未被任何启用规则命中的工具予以保留
//     （Req 9.3）。
//   - 输出保持输入工具的相对顺序不变（管线确定性要求）。
//   - 规则启停的影响在「该次调用」即时体现：本方法按传入规则集合的当前 Enabled
//     状态进行匹配，调用方在状态更新后重新调用即可获得更新后的结果（Req 9.11、13.8）。
//
// 健壮性：若某条启用规则的匹配模式为非法正则（Match 返回 error），按「该规则视为
// 不匹配」处理并继续检查其余规则，不会 panic、也不会因此让整体失败。非法正则本应在
// 保存时由 ValidateFilter 拒绝，此处的容错仅作为防御性兜底。
//
// 该方法为纯函数：不修改入参、无副作用，便于属性测试（任务 3.7）。
func (e *engine) ApplyFilters(tools []ToolDef, filters []FilterRule) []ToolDef {
	// 预筛出启用规则，避免在工具循环内重复判断 Enabled。
	enabled := make([]FilterRule, 0, len(filters))
	for _, f := range filters {
		if f.Enabled {
			enabled = append(enabled, f)
		}
	}

	// 无任何启用规则时，所有工具均保留。
	if len(enabled) == 0 {
		// 返回一个新切片副本，避免调用方持有的底层数组被后续修改影响，保持纯函数语义。
		out := make([]ToolDef, len(tools))
		copy(out, tools)
		return out
	}

	out := make([]ToolDef, 0, len(tools))
	for _, tool := range tools {
		if e.matchedByAny(tool.OriginalName, enabled) {
			// 命中任一启用规则，排除该工具。
			continue
		}
		out = append(out, tool)
	}
	return out
}

// matchedByAny 判断 originalName 是否被任一启用屏蔽规则命中。
//
// 对每条规则调用 Match：正则模式做完整匹配（full match），非正则做区分大小写的
// 精确相等比较。若某条规则的正则非法（Match 返回 error），视该规则为不匹配并跳过，
// 继续检查后续规则。
func (e *engine) matchedByAny(originalName string, enabled []FilterRule) bool {
	for _, f := range enabled {
		ok, err := e.Match(f.Pattern, f.IsRegex, originalName)
		if err != nil {
			// 非法正则：视为不匹配，跳过该规则（防御性兜底，正常应在保存时被拒绝）。
			continue
		}
		if ok {
			return true
		}
	}
	return false
}
