package transport

import (
	"context"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 8.6）实现 WebSocket 传输会话（Req 4.4）。
//
// 官方 MCP Go SDK 未内置 WebSocket 客户端传输，因此这里基于 coder/websocket
//（即 nhooyr.io/websocket 的官方继任者）自封装一个实现 mcp.Transport 的适配，
// 复用 SDK 的 MCP/JSON-RPC 协议处理（initialize、tools/list、tools/call 等）。
//
// 实现要点：
//   - wsClientTransport 实现 mcp.Transport：在 Connect 中完成 WebSocket 握手并携带鉴权凭证（Req 4.7）；
//   - wsConn 实现 mcp.Connection：以文本帧承载 JSON-RPC 消息，借助 SDK 公开的 jsonrpc 包
//     做消息的编解码，从而与 SDK 的 JSON-RPC 引擎对接；
//   - 读写均使用「连接生命周期上下文」而非单次调用上下文，避免单个调用超时/取消把整条连接拆掉
//    （coder/websocket 在传入上下文取消时会关闭连接）；RPC 级的取消仍由 SDK 的 jsonrpc 层负责。

// wsReadLimit 为单条 WebSocket 消息的最大字节数。
// coder/websocket 默认仅 32KiB，而工具列表/调用结果可能较大，这里放宽到 32MiB 以兼顾安全与可用。
const wsReadLimit = 32 << 20

// wsSession 是 WebSocket 传输的上游会话。
//
// 它内嵌 *baseSession 复用统一的状态机、超时与 ListTools/CallTool/Close 语义，
// 自身仅实现 Connect——在其中构造自封装的 wsClientTransport 并注入连接。
type wsSession struct {
	*baseSession
}

// newWebSocketSession 构造 WebSocket 传输会话。连接参数（url）已由 baseSession 解析与校验。
func newWebSocketSession(cfg domain.UpstreamConfig) (UpstreamSession, error) {
	base, err := newBaseSession(cfg, connectTimeoutOf(cfg))
	if err != nil {
		return nil, err
	}
	return &wsSession{baseSession: base}, nil
}

// Connect 与 WebSocket 上游建立连接并完成 MCP 初始化握手（Req 4.4、4.9）。
func (s *wsSession) Connect(ctx context.Context) error {
	return s.establish(ctx, func(dialCtx context.Context) (mcpClientConn, error) {
		transport := &wsClientTransport{
			endpoint:   s.params.url,
			credential: s.credential,
		}
		return connectWithTimeout(dialCtx, transport)
	})
}

// wsClientTransport 是基于 coder/websocket 的 mcp.Transport 实现。
type wsClientTransport struct {
	// endpoint 为 ws:// 或 wss:// 的上游服务地址。
	endpoint string
	// credential 为鉴权凭证明文，握手时以 Authorization: Bearer 头携带（Req 4.7）。
	credential string
}

// Connect 完成 WebSocket 握手并返回承载 JSON-RPC 的连接（mcp.Connection）。
//
// 握手受传入的 ctx 约束（连接建立超时阶段）；握手成功后另建一条「连接生命周期上下文」
// 用于驱动后续读写，使其不受连接建立超时的影响。
func (t *wsClientTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	opts := &websocket.DialOptions{}
	if t.credential != "" {
		// 以 Bearer 形式在握手 HTTP 请求中携带鉴权凭证（Req 4.7）。
		opts.HTTPHeader = http.Header{}
		opts.HTTPHeader.Set("Authorization", "Bearer "+t.credential)
	}

	conn, resp, err := websocket.Dial(ctx, t.endpoint, opts)
	if err != nil {
		return nil, err
	}
	// 握手响应体无需保留（coder/websocket 已处理），显式关闭以避免句柄泄漏。
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	conn.SetReadLimit(wsReadLimit)

	// 连接生命周期上下文：仅在 wsConn.Close 时取消，用于解除阻塞的 Read 并终止连接。
	connCtx, cancel := context.WithCancel(context.Background())
	return &wsConn{conn: conn, ctx: connCtx, cancel: cancel}, nil
}

// wsConn 将一条 WebSocket 连接适配为 mcp.Connection，以文本帧收发 JSON-RPC 消息。
type wsConn struct {
	conn   *websocket.Conn
	ctx    context.Context    // 连接生命周期上下文，驱动 Read/Write
	cancel context.CancelFunc // 在 Close 时取消 ctx

	closeOnce sync.Once
	closeErr  error
}

// Read 读取下一条消息并解码为 jsonrpc.Message。
//
// 使用连接生命周期上下文（而非传入的 ctx）：一方面 SDK 读循环传入的上下文不带取消语义，
// 另一方面这样可保证 Close 取消 ctx 时能解除本次阻塞的 Read（满足 mcp.Connection 的并发约定）。
func (c *wsConn) Read(_ context.Context) (jsonrpc.Message, error) {
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(data)
}

// Write 将 jsonrpc.Message 编码后以文本帧写出。
//
// 同样使用连接生命周期上下文，避免单次调用的超时/取消把整条连接关闭；
// RPC 级别的取消由 SDK 的 jsonrpc 层独立处理。
func (c *wsConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

// Close 关闭 WebSocket 连接并取消连接生命周期上下文。重复调用安全（幂等）。
func (c *wsConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		c.cancel()
	})
	return c.closeErr
}

// SessionID 实现 mcp.Connection 接口。WebSocket 传输无独立会话 ID，返回空串。
func (c *wsConn) SessionID() string { return "" }
