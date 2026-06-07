package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现对外 MCP API 服务（MCP_API_Service）的「全量工具模式」（Req 11.1、11.2）。
//
// 全量模式语义：以 MCP 协议一次性向外部 AI 服务暴露全部聚合工具的定义，工具调用经
// 聚合服务路由到对应上游。本文件聚焦「模式无关」的工具列表/调用编排核心 FullModeHandler，
// 它直接依赖聚合服务接口（domain.Aggregation_Service），不耦合任何真实的 MCP server
// transport——SSE/Streamable-HTTP/WebSocket 的接线属任务 13.4，届时只需把这里的
// ListTools/CallTool 适配为对应传输的 server handler 即可。

// FullModeHandler 是全量模式下的工具列表/调用编排核心。
//
// 它把对外 MCP 请求映射为对聚合服务的两种操作：
//   - ListTools：调用 BuildToolSet 取某 API Key 视角的全部可见聚合工具，一次性转换为
//     MCP 工具定义列表返回（Req 11.2）；
//   - CallTool：调用 InvokeTool 经聚合服务路由到上游（含可见性校验与别名反向映射），
//     并将结果转换为 MCP 调用结果原样返回（Req 10.3、10.4、11.7）。
//
// 该类型不持有任何传输状态，可被多传输端点共享；可见性始终以 BuildToolSet/InvokeTool
// 为唯一来源，保证全量模式与智能模式的可见性一致，差异仅在「暴露方式」。
type FullModeHandler struct {
	agg domain.Aggregation_Service
}

// NewFullModeHandler 构造全量模式处理器，注入聚合服务接口（依赖倒置）。
func NewFullModeHandler(agg domain.Aggregation_Service) *FullModeHandler {
	return &FullModeHandler{agg: agg}
}

// ListTools 返回给定 API Key 视角下的全部可见聚合工具定义（全量模式，Req 11.1、11.2）。
//
//   - apiKeyID 为空：返回全局可见聚合集合；非空：在全局集合上再应用该 Key 的级别过滤。
//   - 当无可见工具时返回长度为 0 的非 nil 切片（空工具列表而非错误，Req 10.7）。
//
// 工具定义由聚合管线产出（已完成屏蔽、别名重写与同名去重），此处仅做领域类型到
// MCP 协议工具定义的纯转换并一次性返回。
func (h *FullModeHandler) ListTools(ctx context.Context, apiKeyID string) ([]*mcp.Tool, error) {
	tools, err := h.agg.BuildToolSet(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	return toMCPTools(tools), nil
}

// CallTool 将一次工具调用经聚合服务路由到上游，并以 MCP 调用结果原样返回。
//
//   - name 为对外暴露的工具名称；args 为原始入参，原样透传给聚合服务（Req 10.3）。
//   - 工具不在当前可见集合内时，聚合服务返回 TOOL_NOT_FOUND 且不向任何上游转发
//     （Req 10.4、11.7）；该错误原样上抛由传输层映射为 MCP 错误响应。
//   - 上游成功结果与上游报告的错误结果均经 toCallToolResult 转换后原样返回（Req 10.3）。
func (h *FullModeHandler) CallTool(ctx context.Context, apiKeyID, name string, args json.RawMessage) (*mcp.CallToolResult, error) {
	res, err := h.agg.InvokeTool(ctx, apiKeyID, name, args)
	if err != nil {
		return nil, err
	}
	return toCallToolResult(res)
}
