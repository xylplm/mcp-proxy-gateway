package transport

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 8.4）实现 SSE 传输会话：基于 MCP Go SDK 的 SSEClientTransport
// 完成连接、初始化与工具收发（Req 4.2）。

// sseSession 是 SSE（Server-Sent Events）传输的上游会话。
//
// 它内嵌 *baseSession 复用统一的状态机、超时与 ListTools/CallTool/Close 语义，
// 自身仅实现 Connect——在其中构造 SDK 的 SSEClientTransport 并注入连接。
type sseSession struct {
	*baseSession
}

// newSSESession 构造 SSE 传输会话。连接参数（url）已由 baseSession 解析与校验。
func newSSESession(cfg domain.UpstreamConfig) (UpstreamSession, error) {
	base, err := newBaseSession(cfg, connectTimeoutOf(cfg))
	if err != nil {
		return nil, err
	}
	return &sseSession{baseSession: base}, nil
}

// Connect 与 SSE 上游建立连接并完成 MCP 初始化握手（Req 4.2、4.9）。
//
// dial 回调中：
//   - 用配置的 url 构造 mcp.SSEClientTransport；
//   - 通过 newAuthHTTPClient 注入携带 Bearer 凭证的 HTTP 客户端（Req 4.7），凭证为空时退回默认客户端；
//   - 调用 connectWithTimeout 在已带连接超时的 ctx 下建立连接并完成 initialize 握手。
func (s *sseSession) Connect(ctx context.Context) error {
	return s.establish(ctx, func(dialCtx context.Context) (mcpClientConn, error) {
		transport := &mcp.SSEClientTransport{
			Endpoint:   s.params.url,
			HTTPClient: newAuthHTTPClient(s.credential),
		}
		return connectWithTimeout(dialCtx, transport)
	})
}
