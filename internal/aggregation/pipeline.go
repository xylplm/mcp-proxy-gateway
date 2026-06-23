package aggregation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

var defaultCompatibleInputSchema = []byte(`{"type":"object"}`)

// ToolCandidate 是某个对外工具名背后的一个真实来源。
type ToolCandidate struct {
	// UpstreamID 为该工具所属上游 MCP 的标识。
	UpstreamID string
	// UpstreamName 为调用时的上游名称快照，仅用于统计展示。
	UpstreamName string
	// OriginalName 为该工具在上游 MCP 中的原始名称（调用转发依据）。
	OriginalName string
	// Tool 为该候选在完整管线处理后的工具定义快照。
	Tool domain.ToolDef
	// Compatible 表示该候选的入参 schema 与对外展示 schema 一致，只有兼容候选参与默认路由。
	Compatible bool
	// SchemaConflict 表示同名来源之间存在 schema 不一致。
	SchemaConflict bool
}

// ReverseEntry 是「对外名称 → 候选来源集合」反向映射的值。
type ReverseEntry struct {
	Name           string
	Display        domain.ToolDef
	Candidates     []ToolCandidate
	SchemaConflict bool
}

// upstreamBundle 是单个启用上游在聚合管线中的输入数据。
//
// 将「已从数据源读取好的数据」与「纯逻辑管线」解耦：编排层（Service）负责读缓存、
// 读规则并填充该结构，runPipeline 仅对这些已就绪的数据做确定性变换，便于属性测试
// 直接构造输入而无需依赖真实缓存/数据库。
type upstreamBundle struct {
	// upstreamID 为该上游的标识。
	upstreamID string
	// upstreamName 为该上游的名称快照，仅用于统计展示。
	upstreamName string
	// sortOrder 为该上游的排序顺序，决定其工具在聚合结果中的相对位置（Req 3.4、10.1）。
	sortOrder int
	// tools 为从工具缓存读取的该上游工具列表（仅启用上游，Req 6.2、10.1）。
	tools []domain.ToolDef
	// aliases 为绑定在该上游上的别名/描述重写规则（Req 8）。
	aliases []domain.AliasRule
	// mcpFilters 为绑定在该上游上的 MCP 级屏蔽规则（Req 9）。
	mcpFilters []domain.FilterRule
}

// runPipeline 执行聚合管线的第 2 至 6 阶段，是一个确定性纯函数。
//
// 入参 bundles 为各启用上游的已就绪数据（对应阶段 1「缓存读取，仅启用上游」的产物，
// 由编排层完成），apiKeyFilters 为某 API Key 的屏蔽规则（apiKeyID 为空即无 API Key
// 级过滤时传入 nil/空切片）。
//
// 执行顺序固定，不可调换（Req 10.2、设计文档「工具聚合管线」）：
//  1. 阶段 2 排序合并：按 upstream.sort_order 升序（稳定）拼接各上游工具（Req 3.4、10.1）。
//  2. 阶段 3 MCP 级屏蔽：对每个上游的工具应用其绑定的启用屏蔽规则，命中即排除（Req 9.3）。
//  3. 阶段 4 别名/描述重写：对保留工具按规则 sort_order 应用首条匹配的别名规则（Req 8.2/8.3/8.5）。
//     注意「先屏蔽后重写」：被屏蔽的工具不会因别名重写而重新出现（Req 10.2）。
//  4. 阶段 5 API Key 级过滤：在每个真实来源之上应用该 API Key 的启用屏蔽规则，
//     匹配对象为工具的 OriginalName（上游原始名称），而非别名重写后的对外名（Req 13.7）。
//  5. 阶段 6 同名来源归并：按最终对外 Name 分组，对外只保留一条工具定义；调用时再
//     从该工具名背后的候选来源中选择真实上游。
//
// 返回变换后的对外工具集合（始终为非 nil 切片，无工具时为空切片）与「对外名称 → 候选来源」
// 反向映射。
//
// 关键不变量：
//   - 输出集合中所有对外 Name 全局唯一。
//   - API Key 级过滤只删除候选来源；同名工具仍可由未被过滤的其他来源承接。
func runPipeline(engine domain.Rule_Engine, bundles []upstreamBundle, apiKeyFilters []domain.FilterRule) ([]domain.ToolDef, map[string]ReverseEntry) {
	// 阶段 2：排序合并。复制后稳定排序，不修改调用方传入的切片顺序；
	// 相同 sort_order 的上游维持其原相对顺序。
	sorted := make([]upstreamBundle, len(bundles))
	copy(sorted, bundles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].sortOrder < sorted[j].sortOrder
	})

	merged := make([]domain.ToolDef, 0)
	upstreamNames := make(map[string]string, len(sorted))
	for _, b := range sorted {
		upstreamNames[b.upstreamID] = b.upstreamName
		// 复制该上游的工具，并规范化工具的归属信息：
		// 强制 UpstreamID 与 Order 与所读取的上游一致，保证后续去重后缀与反向映射正确，
		// 不依赖缓存内工具自带字段的准确性（防御性，且保持纯函数不修改入参元素）。
		tools := make([]domain.ToolDef, len(b.tools))
		copy(tools, b.tools)
		for i := range tools {
			tools[i].UpstreamID = b.upstreamID
			tools[i].Order = b.sortOrder
		}

		// 阶段 3：MCP 级屏蔽（仅对该上游的工具应用其绑定的屏蔽规则）。
		tools = engine.ApplyFilters(tools, b.mcpFilters)
		// 阶段 4：别名/描述重写（仅对该上游的工具应用其绑定的别名规则，首条匹配生效）。
		tools = engine.ApplyAliases(tools, b.aliases)
		// 阶段 5：API Key 级过滤。过滤的是候选来源，不能让某个被屏蔽来源导致同名工具整体消失。
		tools = engine.ApplyFilters(tools, apiKeyFilters)

		merged = append(merged, tools...)
	}

	return groupToolsByName(merged, upstreamNames)
}

func groupToolsByName(tools []domain.ToolDef, upstreamNames map[string]string) ([]domain.ToolDef, map[string]ReverseEntry) {
	order := make([]string, 0)
	reverse := make(map[string]ReverseEntry)
	for _, t := range tools {
		if t.Name == "" {
			continue
		}
		entry, exists := reverse[t.Name]
		if !exists {
			display := t
			display.SourceCount = 0
			display.SchemaConflict = false
			entry = ReverseEntry{Name: t.Name, Display: display}
			order = append(order, t.Name)
		}
		entry.Candidates = append(entry.Candidates, ToolCandidate{
			UpstreamID:   t.UpstreamID,
			UpstreamName: upstreamNames[t.UpstreamID],
			OriginalName: t.OriginalName,
			Tool:         t,
			Compatible:   true,
		})
		reverse[t.Name] = entry
	}

	out := make([]domain.ToolDef, 0, len(order))
	for _, name := range order {
		entry := reverse[name]
		if len(entry.Candidates) == 0 {
			continue
		}
		display := entry.Display
		for i := range entry.Candidates {
			compatible := schemaCompatible(display.InputSchema, entry.Candidates[i].Tool.InputSchema)
			entry.Candidates[i].Compatible = compatible
			if !compatible {
				entry.SchemaConflict = true
			}
		}
		for i := range entry.Candidates {
			entry.Candidates[i].SchemaConflict = entry.SchemaConflict
		}
		display.SourceCount = len(entry.Candidates)
		display.SchemaConflict = entry.SchemaConflict
		entry.Display = display
		reverse[name] = entry
		out = append(out, display)
	}
	return out, reverse
}

func schemaCompatible(base, candidate []byte) bool {
	base = normalizeComparableSchema(base)
	candidate = normalizeComparableSchema(candidate)
	var baseJSON any
	var candidateJSON any
	if json.Unmarshal(base, &baseJSON) == nil && json.Unmarshal(candidate, &candidateJSON) == nil {
		return reflect.DeepEqual(baseJSON, candidateJSON)
	}
	return bytes.Equal(base, candidate)
}

func normalizeComparableSchema(schema []byte) []byte {
	schema = bytes.TrimSpace(schema)
	if len(schema) == 0 {
		return defaultCompatibleInputSchema
	}
	return schema
}
