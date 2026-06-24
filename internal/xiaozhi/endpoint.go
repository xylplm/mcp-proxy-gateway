package xiaozhi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
)

// 本文件（任务 21.1）提供 EndpointConnector 的生产实现 wsConnector：以出站 WebSocket 客户端
// 连接小智 MCP 接入点，并在该连接之上以 MCP「服务端」身份服务工具列表与调用请求。
//
// 协议方向说明：网关主动 Dial 连出（WebSocket 客户端），但在 MCP 协议层扮演服务端——小智
// 是 MCP 客户端，发起 initialize / tools/list / tools/call。因此这里用 mcp.Server 驱动连接。
//
// 官方 MCP Go SDK 未内置 WebSocket 传输，故复用与 internal/transport 相同的思路：基于
// coder/websocket 自封装实现 mcp.Connection 的适配，借助 SDK 公开的 jsonrpc 包做消息编解码。
//
// 工具暴露策略：每次连接建立后，调用 EndpointHandler.ListTools 取当前可见聚合集合（Req 15.2），
// 并为每个工具在临时 mcp.Server 上注册一个低层 ToolHandler——其调用经 EndpointHandler.CallTool
// 路由到聚合服务并原样回传结果（Req 15.3）。每条连接使用独立的 server 实例，反映该次连接建立
// 时的可见集合快照。

// xzReadLimit 为单条 WebSocket 消息的最大字节数（与传输层一致，放宽到 32MiB）。
const xzReadLimit = 32 << 20

// serverName/serverVersion 为网关作为小智接入服务端时上报的实现标识。
const (
	serverName    = "mcp-proxy-gateway-xiaozhi"
	serverVersion = "0.1.0"
)

// wsConnector 是基于 coder/websocket 的 EndpointConnector 生产实现。
type wsConnector struct{}

// newWSConnector 构造默认的 WebSocket 连接驱动器。
func newWSConnector() *wsConnector { return &wsConnector{} }

// 编译期断言：wsConnector 满足 EndpointConnector 契约。
var _ EndpointConnector = (*wsConnector)(nil)

// Serve 连接到小智接入点并在其上服务 handler，阻塞至连接断开或 ctx 取消（Req 15.1/15.5）。
//
// 步骤：
//  1. Dial 连出到 endpoint（WebSocket 握手受 ctx 约束）；
//  2. 将连接适配为 mcp.Connection；
//  3. 据 handler.ListTools 构建当前可见工具集合并注册到临时 mcp.Server（Req 15.2）；
//  4. 以该连接驱动 server 会话，阻塞至小智断开或 ctx 取消后返回。
func (c *wsConnector) Serve(ctx context.Context, endpoint string, handler EndpointHandler) error {
	srv, err := BuildServer(ctx, handler)
	if err != nil {
		return err
	}
	return serveServer(ctx, endpoint, srv)
}

// serveServer 连接到小智接入点，在 WebSocket 连接上驱动 mcp.Server 提供服务，
// 阻塞至连接断开或 ctx 取消。
func serveServer(ctx context.Context, endpoint string, srv *mcp.Server) error {
	conn, resp, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("连接小智接入点失败：%w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	conn.SetReadLimit(xzReadLimit)

	// 连接生命周期上下文：在 Serve 返回（ctx 取消或会话结束）时取消，解除阻塞的 Read。
	connCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	mcpConn := &wsServerConn{conn: conn, ctx: connCtx, cancel: cancel}

	session, err := srv.Connect(connCtx, &fixedConnTransport{conn: mcpConn}, nil)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "mcp connect failed")
		return fmt.Errorf("初始化小智 MCP 会话失败：%w", err)
	}

	// 当 ctx 被取消（Stop/父上下文结束）时主动关闭会话，使 Wait 返回（Req 15.5）。
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				safego.LogRecovered(nil, "小智 MCP 会话关闭 panic 已恢复", recovered)
			}
		}()
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-stop:
		}
	}()

	// 阻塞至小智断开连接或会话被关闭。
	return session.Wait()
}

// BuildServer 据 handler 当前可见工具集合构建一个 MCP 服务端实例（Req 15.2、15.3）。
//
// 该函数对外导出，便于单元测试在不经过真实 WebSocket 的前提下，用 in-memory 传输验证
// 「工具列表暴露」与「调用路由」逻辑。每个工具注册的 ToolHandler 都把调用经 handler.CallTool
// 路由到聚合服务，并将 domain.ToolResult 原样转换为 mcp.CallToolResult 回传。
func BuildServer(ctx context.Context, handler EndpointHandler) (*mcp.Server, error) {
	tools, err := handler.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取聚合工具集合失败：%w", err)
	}

	srv := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		// 始终通告 tools 能力，即使当前集合为空（空集合是合法状态，Req 10.7）。
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}},
	)

	for _, td := range tools {
		srv.AddTool(toMCPTool(td), newToolHandler(handler, td.Name))
	}
	return srv, nil
}

// newToolHandler 返回一个把指定对外工具名的调用路由到聚合服务的低层 ToolHandler。
//
// 使用低层 ToolHandler（而非泛型 AddTool）以原始字节透传入参、原样回传结果，贴合
// 「原始参数透传、结果原样返回」的契约（Req 15.3、10.3）。
func newToolHandler(handler EndpointHandler, exposedName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				safego.LogRecovered(nil, "小智 MCP 工具处理 panic 已恢复", recovered, "tool", exposedName)
				result = nil
				err = domain.NewError(domain.CodeInternal, "服务器内部错误")
			}
		}()

		var args json.RawMessage
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		res, err := handler.CallTool(ctx, exposedName, args)
		if err != nil {
			// 调用路由错误（如工具不可见、上游不可用）作为协议错误上抛，由 SDK 映射为错误响应。
			return nil, err
		}
		return toCallToolResult(res)
	}
}

// fixedConnTransport 是一个返回预置 mcp.Connection 的 mcp.Transport，
// 用于把已建立（或 in-memory）的连接交给 mcp.Server.Connect 驱动。
type fixedConnTransport struct {
	conn mcp.Connection
}

// Connect 返回预置连接（仅被 Server.Connect 调用一次）。
func (t *fixedConnTransport) Connect(_ context.Context) (mcp.Connection, error) {
	return t.conn, nil
}

// wsServerConn 将一条 WebSocket 连接适配为 mcp.Connection，以文本帧收发 JSON-RPC 消息。
//
// 读写均使用「连接生命周期上下文」而非单次调用上下文，使 Close 取消 ctx 时能解除阻塞的 Read。
type wsServerConn struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closeErr  error
}

// Read 读取下一条消息并解码为 jsonrpc.Message。
func (c *wsServerConn) Read(_ context.Context) (jsonrpc.Message, error) {
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(data)
}

// Write 将 jsonrpc.Message 编码后以文本帧写出。
func (c *wsServerConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

// Close 关闭 WebSocket 连接并取消连接生命周期上下文。重复调用安全（幂等）。
func (c *wsServerConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		c.cancel()
	})
	return c.closeErr
}

// SessionID 实现 mcp.Connection 接口。WebSocket 传输无独立会话 ID，返回空串。
func (c *wsServerConn) SessionID() string { return "" }
