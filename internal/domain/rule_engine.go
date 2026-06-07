package domain

// Rule_Engine 是规则引擎接口（别名重命名、描述重写、屏蔽过滤）。
//
// 规则引擎为纯函数式：输入工具列表与规则集合，输出变换后的工具列表，
// 无副作用，便于属性测试。接口名沿用设计文档「关键接口契约」中的命名。
type Rule_Engine interface {
	// ApplyFilters 应用屏蔽规则，返回未被任一启用规则匹配的工具。
	ApplyFilters(tools []ToolDef, filters []FilterRule) []ToolDef
	// ApplyAliases 应用别名/描述重写，每个工具仅命中第一条匹配规则。
	ApplyAliases(tools []ToolDef, aliases []AliasRule) []ToolDef
	// Match 进行名称匹配：正则完整匹配（full match）或区分大小写精确相等。
	Match(pattern string, isRegex bool, originalName string) (bool, error)
	// ValidateAlias 在保存前校验别名规则（正则合法性、模式长度、目标字段非空等）。
	ValidateAlias(r AliasRule) error
	// ValidateFilter 在保存前校验屏蔽规则。
	ValidateFilter(r FilterRule) error
}
