package mcpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 13.4）实现对外 MCP API 服务（MCP_API_Service）的「传输无关」装配核心 Service：
// 它依据配置 mcp_api.mode（全量/智能）为每一条对外连接构建一个 MCP 协议服务端实例
// （*mcp.Server），其工具集合与调用路由复用全量模式（fullmode.go）与智能模式（smartmode.go）
// 的编排核心，从而保证三种对外传输（SSE/Streamable-HTTP/WebSocket）暴露同一套聚合能力。
//
// 与具体传输的接线（HTTP handler、WebSocket 升级、API Key 校验中间件）见 endpoints.go；
// 本文件只负责「按 API Key 视角与模式构建 server」，不感知任何 HTTP/WS 细节，可被多传输共享并独立单测。

// 对外模式取值常量（Req 11.2、11.3）。与配置 mcp_api.mode 对齐（见 config.ModeSmart/ModeFull）。
const (
	// ModeSmart 为智能模式：仅暴露四个网关工具，按需发现/调用具体聚合工具。
	ModeSmart = "smart"
	// ModeFull 为全量模式：一次性暴露全部聚合工具定义。
	ModeFull = "full"
)

// 网关系统作为对外 MCP API 服务端时上报的实现标识。
const (
	apiServerName    = "mcp-proxy-gateway"
	apiServerVersion = "0.1.0"
)

// Service 是对外 MCP API 服务的装配核心：按配置模式与 API Key 视角构建 MCP 服务端实例。
//
// 它不持有任何连接状态，可被 SSE/Streamable-HTTP/WebSocket 三种传输的每条连接共享调用
// BuildServer 各自构建一次性的 *mcp.Server。可见性始终以聚合服务 BuildToolSet/InvokeTool
// 为唯一来源，保证三种传输与两种模式的可见性一致，差异仅在「暴露方式」。
type Service struct {
	mu sync.RWMutex

	// agg 为聚合服务接口，重载对外模式时复用它重新构造模式处理器。
	agg domain.Aggregation_Service
	// mode 为对外模式（ModeSmart/ModeFull）；非 ModeFull 一律按智能模式处理（默认智能）。
	mode string
	// full 为全量模式编排核心，仅在全量模式下用于构建工具集合与路由调用。
	full *FullModeHandler
	// smart 为智能模式编排核心，仅在智能模式下用于网关工具发现与路由调用。
	smart *SmartModeHandler
	// logger 用于记录构建期异常；为空时回退到 slog.Default()。
	logger *slog.Logger
}

// NewService 构造对外 MCP API 服务。
//
//   - agg 为聚合服务接口（依赖倒置），全量/智能两种模式的可见性与调用路由均经它求得；
//   - mode 取自配置 mcp_api.mode：等于 ModeFull 时为全量模式，否则按智能模式处理（默认智能）；
//   - discoveryLimit 取自配置 mcp_api.smart_discovery_limit，越界时由智能模式构造器收敛到默认值；
//   - logger 为空时回退到默认 logger。
func NewService(agg domain.Aggregation_Service, mode string, discoveryLimit int, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		agg:    agg,
		logger: logger,
	}
	s.Reconfigure(mode, discoveryLimit)
	return s
}

// Mode 返回当前对外模式（ModeSmart/ModeFull）。
func (s *Service) Mode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// Reconfigure 在运行时切换对外 MCP 模式与智能发现上限。
//
// 新建连接会立即使用新的模式；已建立连接是否感知变化取决于对应传输的生命周期。
// 处理器本身不持有传输状态，因此可在管理端保存配置后安全调用。
func (s *Service) Reconfigure(mode string, discoveryLimit int) {
	if mode != ModeFull {
		mode = ModeSmart
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	s.full = NewFullModeHandler(s.agg)
	s.smart = NewSmartModeHandler(s.agg, discoveryLimit)
}

func (s *Service) snapshot() (string, *FullModeHandler, *SmartModeHandler) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode, s.full, s.smart
}

// BuildServer 据当前模式与 API Key 视角构建一个对外 MCP 服务端实例（Req 11.2、11.3）。
//
//   - 智能模式：注册四个网关工具（list_tools/search_tools/get_tool/call_tool），其工具集合
//     固定不依赖聚合结果，故构建本身不会失败；可见聚合工具的发现/调用在网关工具被调用时
//     才经聚合服务求得（反映调用时刻的可见集合）。
//   - 全量模式：取该 API Key 视角的全部可见聚合工具（BuildToolSet），逐个注册为低层工具
//     处理器，其调用经聚合服务路由到上游（反映本次连接建立时刻的可见集合快照）。
//
// apiKeyID 为已鉴权 API Key 的标识（由传输层在 API Key 校验通过后传入）；为空表示全局视角。
// 每条对外连接应各调用一次本方法，得到独立的 *mcp.Server。
func (s *Service) BuildServer(ctx context.Context, apiKeyID string) (*mcp.Server, error) {
	mode, full, smart := s.snapshot()
	srv := mcp.NewServer(
		&mcp.Implementation{Name: apiServerName, Version: apiServerVersion},
		// 始终通告 tools 能力，即使当前可见集合为空（空集合是合法状态，Req 10.7）。
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}},
	)

	if mode == ModeFull {
		if err := s.registerFullTools(ctx, srv, apiKeyID, full); err != nil {
			return nil, err
		}
		return srv, nil
	}

	s.registerGatewayTools(srv, apiKeyID, smart)
	return srv, nil
}

// registerFullTools 在全量模式下把该 API Key 视角的全部可见聚合工具注册到 server（Req 11.2）。
//
// 工具定义由聚合管线产出（已完成屏蔽、别名重写与同名去重）；每个工具注册的低层处理器以原始
// 字节透传入参、经全量模式编排核心路由到上游并原样回传结果（Req 10.3、10.4、11.7）。
func (s *Service) registerFullTools(ctx context.Context, srv *mcp.Server, apiKeyID string, full *FullModeHandler) error {
	tools, err := full.ListTools(ctx, apiKeyID)
	if err != nil {
		return fmt.Errorf("构建全量工具集合失败：%w", err)
	}
	for _, t := range tools {
		srv.AddTool(t, s.fullCallHandler(apiKeyID, t.Name, full))
	}
	return nil
}

// fullCallHandler 返回把指定对外工具名的调用经全量模式编排核心路由到上游的低层处理器。
func (s *Service) fullCallHandler(apiKeyID, exposedName string, full *FullModeHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return full.CallTool(ctx, apiKeyID, exposedName, callArguments(req))
	}
}

// callArguments 从工具调用请求中安全提取原始入参字节；请求或参数缺失时返回 nil。
func callArguments(req *mcp.CallToolRequest) json.RawMessage {
	if req == nil || req.Params == nil {
		return nil
	}
	return req.Params.Arguments
}

// registerGatewayTools 在智能模式下把四个网关工具注册到 server（Req 11.3）。
//
// 智能模式对外只暴露这四个工具，客户端通过它们按需发现/获取/调用具体聚合工具：
//   - list_tools/search_tools/get_tool 经智能模式编排核心在可见集合上分页/过滤/查找；
//   - call_tool 经聚合服务路由到具体聚合工具（含可见性校验，Req 11.6、11.7）。
//
// 各网关工具的发现型结果（工具摘要/分页/单工具定义）以 JSON 文本 content 回传；call_tool
// 直接透传聚合服务返回的 MCP 调用结果（Req 10.3）。
func (s *Service) registerGatewayTools(srv *mcp.Server, apiKeyID string, smart *SmartModeHandler) {
	for _, gt := range smart.GatewayTools() {
		srv.AddTool(gt, s.gatewayHandler(apiKeyID, gt.Name, smart))
	}
}

// gatewayHandler 返回处理指定网关工具调用的低层处理器，按网关工具名分派到智能模式编排核心。
func (s *Service) gatewayHandler(apiKeyID, gatewayName string, smart *SmartModeHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := callArguments(req)
		switch gatewayName {
		case GatewayToolListTools:
			return s.handleGatewayListTools(ctx, apiKeyID, args, smart)
		case GatewayToolSearchTools:
			return s.handleGatewaySearchTools(ctx, apiKeyID, args, smart)
		case GatewayToolGetTool:
			return s.handleGatewayGetTool(ctx, apiKeyID, args, smart)
		case GatewayToolCallTool:
			return s.handleGatewayCallTool(ctx, apiKeyID, args, smart)
		default:
			// 仅注册了四个网关工具，正常不会到达此分支；防御性返回工具不存在。
			return nil, domain.NewError(domain.CodeToolNotFound, "未知的网关工具")
		}
	}
}

// listToolsArgs 为网关工具 list_tools 的入参。
type listToolsArgs struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

// searchToolsArgs 为网关工具 search_tools 的入参。
type searchToolsArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// nameArg 为网关工具 get_tool 的入参（仅目标工具名）。
type nameArg struct {
	Name string `json:"name"`
}

// callToolArgs 为网关工具 call_tool 的入参：目标工具名 + 透传给该工具的原始入参。
type callToolArgs struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleGatewayListTools 处理 list_tools：解析分页入参 → 智能模式分页 → JSON 结果回传。
func (s *Service) handleGatewayListTools(ctx context.Context, apiKeyID string, raw json.RawMessage, smart *SmartModeHandler) (*mcp.CallToolResult, error) {
	args, err := decodeGatewayArgs[listToolsArgs](raw)
	if err != nil {
		return nil, err
	}
	page, err := smart.ListTools(ctx, apiKeyID, args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	return jsonResult(page)
}

// handleGatewaySearchTools 处理 search_tools：解析检索入参 → 智能模式过滤 → JSON 结果回传。
func (s *Service) handleGatewaySearchTools(ctx context.Context, apiKeyID string, raw json.RawMessage, smart *SmartModeHandler) (*mcp.CallToolResult, error) {
	args, err := decodeGatewayArgs[searchToolsArgs](raw)
	if err != nil {
		return nil, err
	}
	tools, err := smart.SearchTools(ctx, apiKeyID, args.Query, args.Limit)
	if err != nil {
		return nil, err
	}
	// 与 list_tools 返回结构对齐：包一层 tools 字段，无匹配时为空数组（Req 11.5）。
	return jsonResult(struct {
		Tools []ToolSummary `json:"tools"`
	}{Tools: tools})
}

// handleGatewayGetTool 处理 get_tool：解析目标名 → 智能模式查找 → 完整定义 JSON 回传。
func (s *Service) handleGatewayGetTool(ctx context.Context, apiKeyID string, raw json.RawMessage, smart *SmartModeHandler) (*mcp.CallToolResult, error) {
	args, err := decodeGatewayArgs[nameArg](raw)
	if err != nil {
		return nil, err
	}
	tool, err := smart.GetTool(ctx, apiKeyID, args.Name)
	if err != nil {
		return nil, err
	}
	return jsonResult(tool)
}

// handleGatewayCallTool 处理 call_tool：解析目标名与原始入参 → 经聚合服务路由 → 原样回传结果。
//
// 不可见工具由聚合服务返回 TOOL_NOT_FOUND 且不向上游转发（Req 11.6、11.7）；该错误原样上抛
// 由 SDK 映射为 MCP 错误响应。
func (s *Service) handleGatewayCallTool(ctx context.Context, apiKeyID string, raw json.RawMessage, smart *SmartModeHandler) (*mcp.CallToolResult, error) {
	args, err := decodeGatewayArgs[callToolArgs](raw)
	if err != nil {
		return nil, err
	}
	return smart.CallTool(ctx, apiKeyID, args.Name, args.Arguments)
}

// decodeGatewayArgs 将网关工具的原始入参字节反序列化为类型化入参 T。
//
// 入参为空（客户端未提供 arguments）时返回零值 T，由各编排方法按零值语义处理（如 limit<=0
// 用默认值、cursor 为空从头开始）；JSON 非法时返回字段级校验错误。
func decodeGatewayArgs[T any](raw json.RawMessage) (T, error) {
	var args T
	if len(raw) == 0 {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, domain.NewValidationError(
			"网关工具入参非法",
			map[string]string{"arguments": "必须为合法 JSON 对象"},
		)
	}
	return args, nil
}

// jsonResult 把发现型网关工具的结果序列化为带单条 JSON 文本 content 的 MCP 调用结果。
//
// 智能模式的发现/获取结果（工具摘要、分页、单工具定义）以 JSON 文本回传，便于客户端解析；
// 工具调用类结果（call_tool）则不经此处，直接透传聚合服务返回的原始结果。
func jsonResult(payload any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, domain.NewError(
			domain.CodeUpstreamUnavailable,
			fmt.Sprintf("序列化网关工具结果失败：%v", err),
		)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil
}
