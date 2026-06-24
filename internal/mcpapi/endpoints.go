package mcpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 本文件（任务 13.4）把对外 MCP API 服务（MCP_API_Service）接线到三种 MCP 传输端点：
//   - SSE（Server-Sent Events）          → GET/POST /mcp/sse
//   - Streamable-HTTP                     → GET/POST/DELETE /mcp/http
//   - WebSocket                           → GET /mcp/ws（HTTP 升级）
//
// 三个端点复用同一套聚合能力：每条对外连接都经 Service.BuildServer 据当前模式（全量/智能）
// 与 API Key 视角构建一个独立的 *mcp.Server，从而保证三种传输暴露完全一致的可见集合，
// 差异仅在「传输形式」（Req 11.8）。
//
// 鉴权前置：三个端点都必须置于 API Key 鉴权中间件（apikey.Authenticator.Middleware）及其后续
// 的来源白名单、限流中间件之后；本文件不自行实现鉴权，而是在构建 server 前从已鉴权上下文取出
// API Key 标识（经注入的 APIKeyResolver），确保「在暴露任何聚合能力之前已完成 API Key 校验」
//（Req 11.9）。为避免与 apikey 包产生编译期耦合，这里以函数注入读取 API Key，而非直接依赖该包。
//
// SSE 与 Streamable-HTTP 复用官方 SDK 的 server transport（mcp.SSEHandler / mcp.StreamableHTTPHandler）；
// WebSocket 官方 SDK 未内置 server transport，故在本文件基于 coder/websocket 自封装一个实现
// mcp.Transport/mcp.Connection 的服务端适配（与上游 WS 客户端会话 session_ws.go 对称）。

// 三个对外端点的相对路径（挂载前缀由装配处决定，通常为根，见设计「路由分面」）。
const (
	// PathSSE 为 SSE 传输端点路径。
	PathSSE = "/mcp/sse"
	// PathHTTP 为 Streamable-HTTP 传输端点路径。
	PathHTTP = "/mcp/http"
	// PathWS 为 WebSocket 传输端点路径（全量模式）。
	PathWS = "/mcp/ws"

	// PathSmartSSE 为智能模式 SSE 传输端点路径。
	PathSmartSSE = "/mcp/smart/sse"
	// PathSmartHTTP 为智能模式 Streamable-HTTP 传输端点路径。
	PathSmartHTTP = "/mcp/smart/http"
	// PathSmartWS 为智能模式 WebSocket 传输端点路径。
	PathSmartWS = "/mcp/smart/ws"
)

// wsServerReadLimit 为对外 WebSocket 单条消息的最大字节数。
//
// 与上游 WS 客户端会话保持一致（32MiB）：聚合工具列表/调用结果可能较大，放宽默认上限
// 以兼顾安全与可用。
const wsServerReadLimit = 32 << 20

// mcpRequestBodyLimit limits public MCP POST bodies before handing them to the
// SDK transport. Normal JSON-RPC tool arguments have plenty of room, while
// malformed or oversized requests are stopped before they can pressure memory.
var mcpRequestBodyLimit int64 = 8 << 20

// modeContextKey 用于把 MCP 模式注入 *http.Request 上下文。
type modeContextKey struct{}

// apiKeyContextKey 是把已鉴权 API Key 标识注入 *http.Request 上下文时使用的私有键类型。
//
// SSE / Streamable-HTTP 的 SDK handler 通过 getServer(*http.Request) 回调构建 server，
// 而 API Key 标识由前置中间件存于 gin.Context；故在委派给 SDK handler 前，先把标识注入
// 请求上下文，getServer 再据此键取回，使 BuildServer 能按正确的 API Key 视角构建。
type apiKeyContextKey struct{}

// APIKeyResolver 从 gin 上下文解析当前请求已鉴权的 API Key 标识。
//
// 端点位于 API Key 鉴权中间件之后，鉴权通过后会将 API Key 元数据写入上下文；本解析器据此
// 取出其标识（apiKeyID）。返回 ok=false 表示上下文中无 API Key（理论上不应发生，因为鉴权
// 中间件会在缺失时拒绝请求）。以函数注入而非直接依赖 apikey 包，便于解耦与单元测试。
//
// 装配处（任务 27.2）通常以
//
//	func(c *gin.Context) (string, bool) { m, ok := apikey.MetadataFromContext(c); return m.ID, ok }
//
// 作为该解析器传入。
type APIKeyResolver func(c *gin.Context) (string, bool)

// Endpoints 把对外 MCP API 服务接线到 SSE / Streamable-HTTP / WebSocket 三种传输。
//
// 它持有一次性构建好的 SDK SSE / Streamable-HTTP handler（其 getServer 回调按请求上下文中的
// API Key 视角构建 server），并为三种传输提供可挂载到 gin 路由的处理器。无连接态，可安全并发使用。
type Endpoints struct {
	// svc 为对外 MCP API 装配核心，按模式与 API Key 视角构建 *mcp.Server。
	svc *Service
	// resolveKey 从 gin 上下文取出已鉴权 API Key 标识；为 nil 时按全局视角（空 apiKeyID）构建。
	resolveKey APIKeyResolver
	// logger 用于记录构建/连接异常；为空时回退到 slog.Default()。
	logger *slog.Logger

	// sseHandler / httpHandler 为复用的 SDK server transport handler。
	sseHandler  *mcp.SSEHandler
	httpHandler *mcp.StreamableHTTPHandler
}

// NewEndpoints 构造对外多传输端点接线。
//
//   - svc 为对外 MCP API 装配核心（必需）；
//   - resolveKey 用于从已鉴权上下文取出 API Key 标识，为 nil 时所有连接按全局视角构建；
//   - logger 为空时回退到默认 logger。
func NewEndpoints(svc *Service, resolveKey APIKeyResolver, logger *slog.Logger) *Endpoints {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Endpoints{svc: svc, resolveKey: resolveKey, logger: logger}
	e.sseHandler = mcp.NewSSEHandler(e.getServer, nil)
	e.httpHandler = mcp.NewStreamableHTTPHandler(e.getServer, nil)
	return e
}

// Register 在给定 gin 路由组上注册三个对外传输端点。
//
// 调用方须在传入的路由组上预先挂载 API Key 鉴权、来源白名单与限流中间件，使本端点始终在
// 鉴权之后执行（Req 11.9）。SSE 与 Streamable-HTTP 支持 GET/POST（Streamable 还支持 DELETE
// 用于会话终止），WebSocket 经 GET 升级。
func (e *Endpoints) Register(rg gin.IRoutes) {
	rg.GET(PathSSE, e.handleSSE)
	rg.POST(PathSSE, e.handleSSE)

	rg.GET(PathHTTP, e.handleHTTP)
	rg.POST(PathHTTP, e.handleHTTP)
	rg.DELETE(PathHTTP, e.handleHTTP)

	rg.GET(PathWS, e.handleWS)
}

// getServer 是 SDK SSE / Streamable-HTTP handler 的 server 构建回调。
//
// 它从请求上下文取出由 handleSSE/handleHTTP 注入的 API Key 标识，调用 Service.BuildServer
// 按当前模式与该 API Key 视角构建一个独立的 *mcp.Server。构建失败时记录告警并返回 nil，
// SDK handler 据此返回 400，不暴露任何聚合能力。
func (e *Endpoints) getServer(req *http.Request) *mcp.Server {
	apiKeyID, _ := req.Context().Value(apiKeyContextKey{}).(string)
	mode, _ := req.Context().Value(modeContextKey{}).(string)
	if mode == "" {
		mode = ModeFull
	}
	srv, err := e.svc.BuildServer(req.Context(), apiKeyID, mode)
	if err != nil {
		e.logger.Warn("构建对外 MCP 服务端失败", "apiKeyID", apiKeyID, "mode", mode, "error", err)
		return nil
	}
	return srv
}

// withAPIKey 将当前请求已鉴权的 API Key 标识注入 *http.Request 上下文，供 getServer 取回。
//
// 标识来源于注入的 resolveKey（从 gin 上下文读取鉴权中间件存入的元数据）；resolveKey 为 nil
// 或解析不到时以空标识（全局视角）注入。返回携带新上下文的请求。
func (e *Endpoints) withAPIKey(c *gin.Context, mode string) *http.Request {
	apiKeyID := ""
	if e.resolveKey != nil {
		if id, ok := e.resolveKey(c); ok {
			apiKeyID = id
		}
	}
	ctx := context.WithValue(c.Request.Context(), apiKeyContextKey{}, apiKeyID)
	ctx = context.WithValue(ctx, modeContextKey{}, mode)
	return c.Request.WithContext(ctx)
}

// handleSSE 以 SSE 传输对外暴露聚合能力（Req 11.8）。委派给 SDK 的 SSEHandler，
// 构建 server 前已注入 API Key 视角（Req 11.9）。
func (e *Endpoints) handleSSE(c *gin.Context) {
	if !limitRequestBody(c) {
		return
	}
	e.sseHandler.ServeHTTP(c.Writer, e.withAPIKey(c, ModeFull))
}

// handleHTTP 以 Streamable-HTTP 传输对外暴露聚合能力（Req 11.8）。委派给 SDK 的
// StreamableHTTPHandler，构建 server 前已注入 API Key 视角（Req 11.9）。
func (e *Endpoints) handleHTTP(c *gin.Context) {
	if !limitRequestBody(c) {
		return
	}
	e.httpHandler.ServeHTTP(c.Writer, e.withAPIKey(c, ModeFull))
}

func limitRequestBody(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Body == nil || c.Request.Method != http.MethodPost {
		return true
	}
	if mcpRequestBodyLimit <= 0 {
		return true
	}
	if c.Request.ContentLength > mcpRequestBodyLimit {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, mcpRequestBodyLimit)
	return true
}

func requireWebSocketUpgrade(c *gin.Context) bool {
	if c == nil || c.Request == nil || isWebSocketUpgrade(c.Request) {
		return true
	}
	c.Header("Connection", "Upgrade")
	c.Header("Upgrade", "websocket")
	c.AbortWithStatus(http.StatusUpgradeRequired)
	return false
}

func isWebSocketUpgrade(req *http.Request) bool {
	if req == nil {
		return false
	}
	return headerHasToken(req.Header, "Connection", "Upgrade") &&
		headerHasToken(req.Header, "Upgrade", "websocket")
}

func headerHasToken(header http.Header, key, token string) bool {
	for _, value := range header.Values(key) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// handleWS 以 WebSocket 传输对外暴露聚合能力（Req 11.8）。
//
// 官方 SDK 未内置 WS server transport，故此处自行完成 HTTP→WebSocket 升级，并把升级后的
// 连接包装为实现 mcp.Transport 的服务端适配，交由 Service 据 API Key 视角构建的 *mcp.Server
// 运行。鉴权已在前置中间件完成（Req 11.9），此处仅在升级成功后取 API Key 视角构建 server。
//
// 该处理器会阻塞至会话结束（连接关闭或上下文取消），符合 WS 长连接语义。
func (e *Endpoints) handleWS(c *gin.Context) {
	apiKeyID := ""
	if e.resolveKey != nil {
		if id, ok := e.resolveKey(c); ok {
			apiKeyID = id
		}
	}
	if !requireWebSocketUpgrade(c) {
		return
	}

	srv, err := e.svc.BuildServer(c.Request.Context(), apiKeyID, ModeFull)
	if err != nil {
		e.logger.Warn("构建对外 MCP 服务端失败", "apiKeyID", apiKeyID, "error", err)
		// 升级前失败：以普通 HTTP 错误响应，尚未切换协议。
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		// Accept 失败时其内部已写出 HTTP 错误响应；此处仅记录。
		e.logger.Warn("WebSocket 升级失败", "apiKeyID", apiKeyID, "error", err)
		return
	}
	conn.SetReadLimit(wsServerReadLimit)

	// 连接生命周期上下文：Close 时取消以解除阻塞的 Read。
	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &wsServerTransport{conn: &wsServerConn{conn: conn, ctx: connCtx, cancel: cancel}}

	session, err := srv.Connect(c.Request.Context(), transport, nil)
	if err != nil {
		e.logger.Warn("WebSocket 会话建立失败", "apiKeyID", apiKeyID, "error", err)
		_ = conn.Close(websocket.StatusInternalError, "session setup failed")
		cancel()
		return
	}
	defer session.Close()

	// 阻塞至会话结束（客户端断开或服务端关闭）。
	if werr := session.Wait(); werr != nil {
		e.logger.Debug("WebSocket 会话结束", "apiKeyID", apiKeyID, "error", werr)
	}
}

// wsServerTransport 把一条已升级的服务端 WebSocket 连接适配为 mcp.Transport。
//
// 与上游 WS 客户端会话（transport/session_ws.go）对称：客户端侧实现 mcp.Transport 用于「连出」，
// 这里实现 mcp.Transport 用于「接入」——SDK 的 Server.Connect 会对其调用一次 Connect 取得逻辑连接。
type wsServerTransport struct {
	conn *wsServerConn
}

// Connect 返回承载 JSON-RPC 的逻辑连接。SDK 约定 Connect 仅被调用一次。
func (t *wsServerTransport) Connect(context.Context) (mcp.Connection, error) {
	return t.conn, nil
}

// wsServerConn 将一条服务端 WebSocket 连接适配为 mcp.Connection，以文本帧收发 JSON-RPC 消息。
//
// 读写均使用「连接生命周期上下文」而非单次调用上下文，避免单个 RPC 的超时/取消把整条连接拆掉
// （coder/websocket 在传入上下文取消时会关闭连接）；RPC 级取消由 SDK 的 jsonrpc 层负责。
type wsServerConn struct {
	conn   *websocket.Conn
	ctx    context.Context    // 连接生命周期上下文，驱动 Read/Write
	cancel context.CancelFunc // 在 Close 时取消 ctx

	closeOnce sync.Once
	closeErr  error
}

// Read 读取下一条消息并解码为 jsonrpc.Message。使用连接生命周期上下文，使 Close 能解除阻塞的 Read。
func (c *wsServerConn) Read(context.Context) (jsonrpc.Message, error) {
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(data)
}

// Write 将 jsonrpc.Message 编码后以文本帧写出，使用连接生命周期上下文以避免单次调用取消关连接。
func (c *wsServerConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

// Close 正常关闭 WebSocket 连接并取消连接生命周期上下文。重复调用安全（幂等）。
func (c *wsServerConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		c.cancel()
	})
	return c.closeErr
}

// SessionID 实现 mcp.Connection。对外 WS 传输无独立会话 ID，返回空串。
func (c *wsServerConn) SessionID() string { return "" }

// RegisterSmart 在给定 gin 路由组上注册智能模式的三种对外传输端点。
func (e *Endpoints) RegisterSmart(rg gin.IRoutes) {
	rg.GET(PathSmartSSE, e.handleSmartSSE)
	rg.POST(PathSmartSSE, e.handleSmartSSE)

	rg.GET(PathSmartHTTP, e.handleSmartHTTP)
	rg.POST(PathSmartHTTP, e.handleSmartHTTP)
	rg.DELETE(PathSmartHTTP, e.handleSmartHTTP)

	rg.GET(PathSmartWS, e.handleSmartWS)
}

func (e *Endpoints) handleSmartSSE(c *gin.Context) {
	if !limitRequestBody(c) {
		return
	}
	e.sseHandler.ServeHTTP(c.Writer, e.withAPIKey(c, ModeSmart))
}

func (e *Endpoints) handleSmartHTTP(c *gin.Context) {
	if !limitRequestBody(c) {
		return
	}
	e.httpHandler.ServeHTTP(c.Writer, e.withAPIKey(c, ModeSmart))
}

func (e *Endpoints) handleSmartWS(c *gin.Context) {
	apiKeyID := ""
	if e.resolveKey != nil {
		if id, ok := e.resolveKey(c); ok {
			apiKeyID = id
		}
	}
	if !requireWebSocketUpgrade(c) {
		return
	}

	srv, err := e.svc.BuildServer(c.Request.Context(), apiKeyID, ModeSmart)
	if err != nil {
		e.logger.Warn("构建智能模式 MCP 服务端失败", "apiKeyID", apiKeyID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		e.logger.Warn("智能模式 WebSocket 升级失败", "apiKeyID", apiKeyID, "error", err)
		return
	}
	conn.SetReadLimit(wsServerReadLimit)

	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &wsServerTransport{conn: &wsServerConn{conn: conn, ctx: connCtx, cancel: cancel}}

	session, err := srv.Connect(c.Request.Context(), transport, nil)
	if err != nil {
		e.logger.Warn("智能模式 WebSocket 会话建立失败", "apiKeyID", apiKeyID, "error", err)
		_ = conn.Close(websocket.StatusInternalError, "session setup failed")
		cancel()
		return
	}
	defer session.Close()

	if werr := session.Wait(); werr != nil {
		e.logger.Debug("智能模式 WebSocket 会话结束", "apiKeyID", apiKeyID, "error", werr)
	}
}
