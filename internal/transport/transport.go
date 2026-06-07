// Package transport 定义传输适配层（Transport_Adapter）的接口契约与骨架实现。
//
// 传输适配层屏蔽 stdio/SSE/Streamable-HTTP/WebSocket 等传输类型的差异，
// 向上仅暴露统一的 MCP 会话语义；只负责单条连接的协议收发，
// 不负责重试与生命周期（由 MCP_Manager 编排）。
package transport

import (
	"context"
	"encoding/json"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// UpstreamSession 是单条上游 MCP 会话的统一抽象。
type UpstreamSession interface {
	// Connect 建立连接并完成 MCP initialize 握手，受连接建立超时约束。
	Connect(ctx context.Context) error
	// ListTools 拉取工具列表（由 Sync_Service 使用）。
	ListTools(ctx context.Context) ([]domain.ToolDef, error)
	// CallTool 转发工具调用，原始参数透传。
	CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error)
	// Close 关闭会话并释放资源。
	Close() error
}

// TransportFactory 按传输类型与连接参数构造会话，并校验必填参数。
type TransportFactory interface {
	// NewSession 构造会话；参数校验失败返回字段级校验错误，且不建立连接。
	NewSession(cfg domain.UpstreamConfig) (UpstreamSession, error)
	// Supports 报告是否支持给定的传输类型。
	Supports(t domain.TransportType) bool
}
