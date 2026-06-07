package mcpapi

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件提供对外 MCP API 服务（MCP_API_Service）在全量模式（任务 13.1）与智能模式
// （任务 13.2）之间共享的、与具体传输无关的转换辅助：
//   - 领域工具定义 domain.ToolDef → MCP 协议工具定义 *mcp.Tool；
//   - 领域工具结果 domain.ToolResult → MCP 协议调用结果 *mcp.CallToolResult。
//
// 这些辅助只做「领域类型 ↔ MCP 协议类型」的纯转换，不感知 SSE/Streamable-HTTP/WebSocket
// 等真实 server transport（其接线属任务 13.4），从而可被两种模式复用并独立单测。
// 为避免与后续智能模式（13.2）的标识符冲突，转换辅助集中在本文件、以 toMCP/toCall 前缀命名。

// defaultInputSchema 为工具未携带入参 Schema 时回退使用的最小合法 JSON Schema。
//
// MCP 协议要求每个工具具备 inputSchema；当领域工具定义的 InputSchema 为空时，
// 以「空对象类型」作为占位，保证对外暴露的工具定义始终为合法 JSON Schema。
var defaultInputSchema = json.RawMessage(`{"type":"object"}`)

// toMCPTool 将单个领域工具定义转换为 MCP 协议工具定义。
//
// 转换约定：
//   - Name/Description 直接取对外暴露字段（已由聚合管线完成别名重写与同名去重）；
//   - InputSchema 透传领域定义中的原始 JSON Schema 字节；为空时回退到 defaultInputSchema。
//
// InputSchema 以 json.RawMessage 形式赋给 mcp.Tool.InputSchema(any)，SDK 序列化时
// 原样输出该 JSON Schema（见 mcp.Tool.InputSchema 文档说明）。
func toMCPTool(t domain.ToolDef) *mcp.Tool {
	schema := t.InputSchema
	if len(schema) == 0 {
		schema = defaultInputSchema
	}
	return &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
	}
}

// toMCPTools 将领域工具定义列表批量转换为 MCP 协议工具定义列表。
//
// 始终返回非 nil 切片：输入为空时返回长度为 0 的切片，保证「无可见工具」时对外
// 暴露的是空工具列表而非 null（Req 10.7、11.2）。
func toMCPTools(tools []domain.ToolDef) []*mcp.Tool {
	out := make([]*mcp.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, toMCPTool(t))
	}
	return out
}

// toCallToolResult 将领域工具调用结果转换为 MCP 协议调用结果。
//
// domain.ToolResult.Content 为 MCP content 数组的原始 JSON 字节（由传输层在拉取上游
// 结果时序列化得到）；此处经 mcp.CallToolResult 的 JSON 反序列化还原出具体 content
// 类型，并保留 IsError 标志，使上游成功结果与上游报告的错误结果均原样透传（Req 10.3）。
//
// Content 为空时以 JSON 空数组占位，避免反序列化得到 nil content。
func toCallToolResult(res domain.ToolResult) (*mcp.CallToolResult, error) {
	content := res.Content
	if len(content) == 0 {
		content = json.RawMessage("[]")
	}
	payload := struct {
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"isError"`
	}{Content: content, IsError: res.IsError}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, domain.NewError(
			domain.CodeUpstreamUnavailable,
			fmt.Sprintf("序列化工具调用结果失败：%v", err),
		)
	}

	var out mcp.CallToolResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, domain.NewError(
			domain.CodeUpstreamUnavailable,
			fmt.Sprintf("解析工具调用结果失败：%v", err),
		)
	}
	return &out, nil
}
