package xiaozhi

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件提供小智接入服务在「暴露聚合工具」与「转发调用结果」时所需的、与具体传输无关的
// 纯转换辅助：
//   - 领域工具定义 domain.ToolDef → MCP 协议工具定义 *mcp.Tool；
//   - 领域工具结果 domain.ToolResult → MCP 协议调用结果 *mcp.CallToolResult。
//
// 语义与 internal/mcpapi 中的对外暴露保持一致（同一聚合集合的两种暴露方式），但置于本包内
// 以避免跨包耦合，可被小智连接器独立复用与单测。

// defaultInputSchema 为工具未携带入参 Schema 时回退使用的最小合法 JSON Schema。
var defaultInputSchema = json.RawMessage(`{"type":"object"}`)

// toMCPTool 将单个领域工具定义转换为 MCP 协议工具定义。
//
// Name/Description 取对外暴露字段（已由聚合管线完成别名重写与同名去重，Req 15.2）；
// InputSchema 透传原始 JSON Schema 字节，为空时回退到 defaultInputSchema 以保证合法。
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

// toCallToolResult 将领域工具调用结果转换为 MCP 协议调用结果，保留 IsError 标志使
// 成功结果与上游报告的错误结果均原样透传（Req 15.3、10.3）。
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
