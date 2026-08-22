package mcpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现对外 MCP API 服务（MCP_API_Service）的「智能模式」（Req 11.3-11.7）。
//
// 智能模式语义：对外仅暴露少量「网关工具」（gateway tools），而不一次性暴露全部聚合
// 工具定义，以节省外部 AI 客户端（如 Claude Code）的上下文窗口。客户端通过网关工具按需
// 发现、获取入参结构并调用具体聚合工具：
//   - list_tools：分页返回可见聚合工具的「名称 + 简述」概览，不含完整 inputSchema；
//   - search_tools：按关键字过滤（名称或描述命中），返回数量默认取配置 smart_discovery_limit
//     （默认 50，范围 1-200），无匹配返回空列表而非错误（Req 11.4、11.5）；
//   - get_tool：返回单个工具的完整定义（含 inputSchema），不可见工具返回工具不存在（Req 11.7）；
//   - call_tool：经聚合服务路由到具体聚合工具，不可见返回工具不存在且不发起调用（Req 11.6、11.7）。
//
// 与全量模式一致，所有可见性判定都以聚合服务 BuildToolSet/InvokeTool 为唯一来源（已过完整
// 管线，含 API Key 级过滤），保证两种模式的可见性一致，差异仅在「暴露方式」。
//
// 本文件聚焦「模式无关」的网关工具编排核心 SmartModeHandler，不耦合任何真实的 MCP server
// transport——SSE/Streamable-HTTP/WebSocket 的接线属任务 13.4；届时只需把这里的网关工具
// 定义（GatewayTools）与四个处理方法适配为对应传输的 server handler 即可。
// 为避免与 fullmode.go 的标识符冲突，本文件类型/方法统一以 SmartMode/Gateway 前缀命名，
// 并复用 tools.go 中的共享转换辅助（toMCPTool/toCallToolResult），不重复定义。

// 智能模式工具发现返回数的取值边界（Req 11.4）。
const (
	minDiscoveryLimit     = 1
	maxDiscoveryLimit     = 200
	defaultDiscoveryLimit = 50
)

// 智能模式对外暴露的四个网关工具名称。
const (
	GatewayToolListTools   = "list_tools"
	GatewayToolSearchTools = "search_tools"
	GatewayToolGetTool     = "get_tool"
	GatewayToolCallTool    = "call_tool"
)

// 智能模式网关工具描述。描述会直接暴露给 AI 客户端，需强调使用顺序与入参约束。
const (
	GatewayToolListToolsDescription   = "【第一步·发现】分页浏览所有可用工具的名称和简述。当用户提出需求时，先调用此工具了解有哪些工具可用，再根据需要调用 get_tool 获取完整定义，最后通过 call_tool 执行。"
	GatewayToolSearchToolsDescription = "【发现】按关键词在工具名称和描述中检索，返回匹配的工具名称和简述。适合在已知需求但不清楚具体工具名时缩小范围。找到目标工具后，用 get_tool 获取完整参数定义，再用 call_tool 调用。"
	GatewayToolGetToolDescription     = "【第二步·获取定义】根据工具名称获取完整定义，包括 inputSchema（参数结构、必填字段和类型约束）。在调用 call_tool 之前必须先通过此工具确认参数格式。"
	GatewayToolCallToolDescription    = "【第三步·执行】调用指定的真实聚合工具。name 必须来自 list_tools 或 search_tools 返回的结果，arguments 必须符合 get_tool 返回的 inputSchema。请确保先发现并获取定义再调用。"
)

// 各网关工具的入参 JSON Schema。这些 Schema 在智能模式 tools/list 中作为网关工具定义对外
// 暴露，供客户端构造网关工具调用入参。
var (
	listToolsInputSchema = json.RawMessage(`{"type":"object","properties":{"cursor":{"type":"string","description":"上一页返回的分页游标，省略则从头开始"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"本页返回条数，范围 1-200，省略则使用服务端默认值"}}}`)

	searchToolsInputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"检索关键字，匹配工具名称或描述"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"返回条数，范围 1-200，默认 50"}},"required":["query"]}`)

	getToolInputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"目标工具的对外名称"}},"required":["name"]}`)

	callToolInputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"目标工具的对外名称"},"arguments":{"type":"object","description":"传递给目标工具的原始入参"}},"required":["name"]}`)
)

// ToolSummary 是工具的轻量摘要（仅名称与简述），用于 list_tools / search_tools 的返回。
//
// 智能模式刻意只返回名称 + 描述以节省上下文，不携带完整 inputSchema；客户端选定目标工具后
// 再用 get_tool 取回该工具的完整定义。
type ToolSummary struct {
	// Name 为工具对外暴露名称（已由聚合管线完成别名重写与同名去重）。
	Name string `json:"name"`
	// Description 为工具对外暴露描述（简述）。
	Description string `json:"description"`
}

// ToolPage 是 list_tools 的分页结果。
type ToolPage struct {
	// Tools 为本页工具摘要列表，始终非 nil（无数据时为空切片）。
	Tools []ToolSummary `json:"tools"`
	// NextCursor 为下一页游标；为空表示已无更多数据。
	NextCursor string `json:"nextCursor,omitempty"`
}

// SmartModeHandler 是智能模式下的网关工具编排核心。
//
// 它把对外的网关工具调用映射为对聚合服务的操作：list_tools/search_tools/get_tool 基于
// BuildToolSet 求得某 API Key 视角的可见聚合工具集合后在内存中分页/过滤/查找；call_tool
// 经 InvokeTool 路由到上游（含可见性校验与别名反向映射）。
//
// 该类型不持有任何传输状态，可被多传输端点共享；可见性始终以 BuildToolSet/InvokeTool 为
// 唯一来源，保证智能模式与全量模式的可见性一致。
type SmartModeHandler struct {
	agg domain.Aggregation_Service
	// discoveryLimit 为工具发现返回数默认值（来自配置 smart_discovery_limit）。
	// 构造时已保证落在 [minDiscoveryLimit, maxDiscoveryLimit] 内。
	discoveryLimit int
}

// NewSmartModeHandler 构造智能模式处理器，注入聚合服务接口与工具发现返回数默认值。
//
// discoveryLimit 取自配置 mcp_api.smart_discovery_limit；若传入值越界（非 1-200），
// 则回退到默认值 50，保证后续返回数始终合法（Req 11.4）。
func NewSmartModeHandler(agg domain.Aggregation_Service, discoveryLimit int) *SmartModeHandler {
	if discoveryLimit < minDiscoveryLimit || discoveryLimit > maxDiscoveryLimit {
		discoveryLimit = defaultDiscoveryLimit
	}
	return &SmartModeHandler{agg: agg, discoveryLimit: discoveryLimit}
}

// GatewayTools 返回智能模式下对外暴露的四个网关工具定义（供 tools/list 一次性返回，Req 11.3）。
//
// 智能模式 tools/list 只暴露这四个工具，客户端上下文仅占用四个工具定义，而非全部聚合工具。
// 始终返回固定顺序的非 nil 切片。
func (h *SmartModeHandler) GatewayTools() []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        GatewayToolListTools,
			Description: GatewayToolListToolsDescription,
			InputSchema: listToolsInputSchema,
		},
		{
			Name:        GatewayToolSearchTools,
			Description: GatewayToolSearchToolsDescription,
			InputSchema: searchToolsInputSchema,
		},
		{
			Name:        GatewayToolGetTool,
			Description: GatewayToolGetToolDescription,
			InputSchema: getToolInputSchema,
		},
		{
			Name:        GatewayToolCallTool,
			Description: GatewayToolCallToolDescription,
			InputSchema: callToolInputSchema,
		},
	}
}

// ListTools 实现网关工具 list_tools：分页返回可见聚合工具的「名称 + 简述」（Req 11.3）。
//
//   - apiKeyID 为空：基于全局可见聚合集合；非空：基于该 Key 视角的可见集合（含 API Key 级过滤）。
//   - cursor 为上一页返回的游标（本实现以十进制偏移量编码）；为空表示从头开始；非法游标返回
//     校验错误。
//   - limit 为本页条数，<=0 表示使用配置默认值，越界则收敛到 [1,200]。
//
// 返回的 ToolPage.Tools 始终非 nil；当还有后续数据时 NextCursor 指向下一偏移量，否则为空。
func (h *SmartModeHandler) ListTools(ctx context.Context, apiKeyID, cursor string, limit int) (ToolPage, error) {
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return ToolPage{}, err
	}

	offset := 0
	if cursor != "" {
		parsed, perr := strconv.Atoi(cursor)
		if perr != nil || parsed < 0 {
			return ToolPage{}, domain.NewValidationError(
				"分页游标非法",
				map[string]string{"cursor": "游标必须为非负整数"},
			)
		}
		offset = parsed
	}

	total := len(tools)
	// 偏移量越界时收敛到末尾，返回空页（不报错）。
	if offset > total {
		offset = total
	}

	end := min(offset+h.resolveLimit(limit), total)

	page := ToolPage{Tools: toToolSummaries(tools[offset:end])}
	if end < total {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// SearchTools 实现网关工具 search_tools：按关键字过滤可见聚合工具（Req 11.4、11.5）。
//
//   - 关键字与工具名称、描述做去除首尾空白、不区分大小写的子串包含匹配，命中任一即返回。
//   - limit <=0 表示使用配置默认值（smart_discovery_limit），越界则收敛到 [1,200]。
//   - 命中数量超过有效返回数时截断；无匹配时返回空切片而非错误（Req 11.5）。
//
// 返回的切片始终非 nil。
func (h *SmartModeHandler) SearchTools(ctx context.Context, apiKeyID, query string, limit int) ([]ToolSummary, error) {
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}

	kw := strings.ToLower(strings.TrimSpace(query))
	effLimit := h.resolveLimit(limit)

	out := make([]ToolSummary, 0)
	for _, t := range tools {
		if len(out) >= effLimit {
			break
		}
		if strings.Contains(strings.ToLower(t.Name), kw) ||
			strings.Contains(strings.ToLower(t.Description), kw) {
			out = append(out, ToolSummary{Name: t.Name, Description: t.Description})
		}
	}
	return out, nil
}

// GetTool 实现网关工具 get_tool：返回单个可见工具的完整定义（含 inputSchema，Req 11.7）。
//
// 在该 API Key 视角的可见聚合集合中按对外名称查找：命中则转换为 MCP 工具定义返回；
// 不可见（不在集合内）则返回 TOOL_NOT_FOUND，不泄露被过滤工具的存在。
func (h *SmartModeHandler) GetTool(ctx context.Context, apiKeyID, name string) (*mcp.Tool, error) {
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name == name {
			return toMCPTool(t), nil
		}
	}
	return nil, domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
}

// CallTool 实现网关工具 call_tool：将一次调用经聚合服务路由到具体聚合工具（Req 11.6、11.7）。
//
//   - name 为对外暴露的工具名称；args 为原始入参，原样透传给聚合服务（Req 10.3）。
//   - 工具不在当前可见集合内时，聚合服务 InvokeTool 返回 TOOL_NOT_FOUND 且不向任何上游
//     转发（Req 11.7）；该错误原样上抛由传输层映射为 MCP 错误响应。
//   - 上游成功结果与上游报告的错误结果均经 toCallToolResult 转换后原样返回（Req 10.3）。
func (h *SmartModeHandler) CallTool(ctx context.Context, apiKeyID, name string, args json.RawMessage) (*mcp.CallToolResult, error) {
	res, err := h.agg.InvokeTool(ctx, apiKeyID, name, args)
	if err != nil {
		return nil, err
	}
	return toCallToolResult(res)
}

// resolveLimit 将请求的返回数收敛到合法返回数：<=0 使用配置默认值，否则收敛到 [1,200]。
func (h *SmartModeHandler) resolveLimit(requested int) int {
	if requested <= 0 {
		return h.discoveryLimit
	}
	if requested < minDiscoveryLimit {
		return minDiscoveryLimit
	}
	if requested > maxDiscoveryLimit {
		return maxDiscoveryLimit
	}
	return requested
}

// toToolSummaries 将领域工具定义列表转换为轻量摘要列表（仅名称 + 描述）。
//
// 始终返回非 nil 切片，保证「无工具」时返回空列表而非 null。
func toToolSummaries(tools []domain.ToolDef) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolSummary{Name: t.Name, Description: t.Description})
	}
	return out
}
