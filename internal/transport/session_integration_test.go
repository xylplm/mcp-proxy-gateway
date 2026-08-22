package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// 本文件（任务 8.8）实现四种传输类型的连接与初始化集成/冒烟测试。
//
// 覆盖需求：4.1（stdio）、4.2（SSE）、4.3（Streamable-HTTP）、4.4（WebSocket）、4.9（连接超时标记失败）。
//
// 测试策略——优先「真实回环」：
//   - 在测试进程内用官方 MCP Go SDK 的 server 能力（mcp.NewServer + AddTool）启动一个 mock 上游 MCP server，
//     再用本包工厂构造的客户端会话连接它，端到端验证「连接建立 → MCP 初始化握手 → tools/list → tools/call」。
//   - SSE / Streamable-HTTP：用 httptest.Server 挂载 SDK 的 SSEHandler / StreamableHTTPHandler 做 HTTP 回环。
//   - WebSocket：SDK 未内置 WS 服务端，故用 coder/websocket 接受连接并复用本包的 wsConn 适配为 mcp.Connection，
//     再交给 SDK server 驱动，构成真实 WS 回环。
//   - stdio：用「测试二进制自重入」模式——通过 TestMain 检测环境变量，子进程以 stdio 形式运行同一 mock server，
//     客户端以子进程方式连接（无需任何外部可执行文件或网络）。
//
// 连接超时（Req 4.9）：另用一个「接受连接但永不完成初始化」的静默服务器，配较短连接超时，
// 验证 Connect 中止并返回连接超时错误（CodeUpstreamTimeout）；并对不可达地址做失败路径冒烟。
//
// 所有用例均自包含、无外部网络/外部服务依赖（httptest 回环 + 本地子进程）。

// stdioServerEnv 是触发「测试二进制以 stdio mock server 模式运行」的环境变量名。
const stdioServerEnv = "MPG_STDIO_MOCK_SERVER"

// echoToolName / errorToolName 为 mock server 暴露的两个工具名。
const (
	echoToolName  = "echo"
	errorToolName = "always_error"
)

// TestMain 在进入测试前检测自重入标志：若置位则以 stdio 形式运行 mock MCP server 并退出，
// 从而让 stdio 集成测试可以把本测试二进制当作上游子进程启动（Req 4.1），无需外部依赖。
func TestMain(m *testing.M) {
	if os.Getenv(stdioServerEnv) == "1" {
		runStdioMockServer()
		return
	}
	os.Exit(m.Run())
}

// runStdioMockServer 以 stdio 传输运行 mock MCP server，阻塞直至客户端关闭（stdin 关闭）后返回。
func runStdioMockServer() {
	srv := newMockMCPServer()
	// StdioTransport 通过本进程的 stdin/stdout 承载 JSON-RPC；客户端 Close 关闭 stdin 后 Run 返回。
	_ = srv.Run(context.Background(), &mcp.StdioTransport{})
}

// newMockMCPServer 构造一个最小可用的上游 mock MCP server：
//   - echo 工具：将收到的原始入参以文本内容原样回显（用于验证 tools/call 参数透传与成功结果）；
//   - always_error 工具：返回 IsError=true 的结果（用于验证上游错误结果原样透传）。
//
// 刻意使用低层 server.AddTool（ToolHandler），以便直接读取原始参数并自定义返回内容，
// 不引入入参/出参的自动校验，贴合传输层「原始参数透传、结果原样返回」的契约（Req 10.3）。
func newMockMCPServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "mock-upstream", Version: "0.0.1"}, nil)

	objectSchema := json.RawMessage(`{"type":"object"}`)

	srv.AddTool(
		&mcp.Tool{
			Name:        echoToolName,
			Description: "回显收到的原始入参，用于集成测试",
			InputSchema: objectSchema,
		},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// 将原始入参字节原样作为文本内容回显，便于断言参数透传。
			raw := string(req.Params.Arguments)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: raw}},
			}, nil
		},
	)

	srv.AddTool(
		&mcp.Tool{
			Name:        errorToolName,
			Description: "始终返回上游错误结果，用于验证错误结果透传",
			InputSchema: objectSchema,
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "上游报告的错误"}},
			}, nil
		},
	)

	return srv
}

// --- 通用断言与会话辅助 ---

// mustNewSession 通过工厂构造会话，构造失败即致命。
func mustNewSession(t *testing.T, cfg domain.UpstreamConfig) UpstreamSession {
	t.Helper()
	sess, err := NewFactory().NewSession(cfg)
	if err != nil {
		t.Fatalf("构造会话失败：%v", err)
	}
	return sess
}

// setConnectTimeout 在测试中覆盖会话的连接建立超时（同包可直接访问内嵌 baseSession 字段）。
// 用于让成功用例快速失败、让超时用例用较短超时验证 Req 4.9。
func setConnectTimeout(sess UpstreamSession, d time.Duration) {
	switch s := sess.(type) {
	case *stdioSession:
		s.connectTimeout = d
	case *sseSession:
		s.connectTimeout = d
	case *streamableHTTPSession:
		s.connectTimeout = d
	case *wsSession:
		s.connectTimeout = d
	}
}

// assertToolFlow 对一个已构造的会话执行完整的连接 → 初始化 → tools/list → tools/call 流程断言。
// transport 仅用于错误信息标注。
func assertToolFlow(t *testing.T, sess UpstreamSession, transport string) {
	t.Helper()

	// 给成功用例一个有限的连接超时，避免上游异常时长时间挂起。
	setConnectTimeout(sess, 15*time.Second)

	ctx := context.Background()

	// 1) 连接建立 + MCP 初始化握手（Req 4.1/4.2/4.3/4.4）。
	if err := sess.Connect(ctx); err != nil {
		t.Fatalf("[%s] 连接与初始化失败：%v", transport, err)
	}

	// 2) tools/list：应能收到 mock server 暴露的工具。
	tools, err := sess.ListTools(ctx)
	if err != nil {
		t.Fatalf("[%s] ListTools 失败：%v", transport, err)
	}
	if !containsToolNamed(tools, echoToolName) || !containsToolNamed(tools, errorToolName) {
		t.Fatalf("[%s] ListTools 未返回期望工具，实际=%v", transport, toolNames(tools))
	}

	// 3) tools/call（成功）：echo 工具应原样回显入参，验证参数透传与成功结果。
	args := json.RawMessage(`{"msg":"hello"}`)
	res, err := sess.CallTool(ctx, echoToolName, args)
	if err != nil {
		t.Fatalf("[%s] CallTool(echo) 失败：%v", transport, err)
	}
	if res.IsError {
		t.Fatalf("[%s] CallTool(echo) 不应为错误结果：%s", transport, string(res.Content))
	}
	if !strings.Contains(string(res.Content), "hello") {
		t.Fatalf("[%s] CallTool(echo) 结果未透传入参，content=%s", transport, string(res.Content))
	}

	// 4) tools/call（上游错误）：always_error 工具应返回 IsError=true 的结果并原样透传（Req 10.3）。
	errRes, err := sess.CallTool(ctx, errorToolName, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("[%s] CallTool(always_error) 不应返回协议级错误：%v", transport, err)
	}
	if !errRes.IsError {
		t.Fatalf("[%s] CallTool(always_error) 期望 IsError=true，实际=%+v", transport, errRes)
	}
}

// containsToolNamed 报告工具列表中是否存在指定原始名称的工具。
func containsToolNamed(tools []domain.ToolDef, name string) bool {
	for _, td := range tools {
		if td.OriginalName == name {
			return true
		}
	}
	return false
}

// toolNames 提取工具原始名称，用于错误信息展示。
func toolNames(tools []domain.ToolDef) []string {
	out := make([]string, 0, len(tools))
	for _, td := range tools {
		out = append(out, td.OriginalName)
	}
	return out
}

// --- stdio（Req 4.1）---

// TestIntegrationStdioConnectAndToolFlow 通过把测试二进制自身作为 stdio 上游子进程，
// 验证 stdio 传输的连接、初始化、tools/list 与 tools/call。
func TestIntegrationStdioConnectAndToolFlow(t *testing.T) {
	// 测试二进制基名不在产品默认白名单内；此处仅禁用白名单、保留危险命令 denylist。
	SetPolicyProvider(func() runtime.Policy {
		return runtime.Policy{StdioEnabled: true, CommandAllowlist: []string{}}
	})
	t.Cleanup(func() { SetPolicyProvider(nil) })

	cfg := domain.UpstreamConfig{
		Name:      "stdio-upstream",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: os.Args[0], // 重新执行测试二进制本身
			// 通过用户 env 注入（不被敏感前缀清理），触发 TestMain 的 mock server 模式。
			ParamEnv: map[string]string{stdioServerEnv: "1"},
		},
	}

	sess := mustNewSession(t, cfg)
	defer sess.Close()

	assertToolFlow(t, sess, "stdio")
}

// --- SSE（Req 4.2）---

// TestIntegrationSSEConnectAndToolFlow 用 httptest + SDK SSEHandler 做 SSE 回环，验证完整工具流程。
func TestIntegrationSSEConnectAndToolFlow(t *testing.T) {
	srv := newMockMCPServer()
	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	cfg := domain.UpstreamConfig{
		Name:       "sse-upstream",
		Transport:  domain.TransportSSE,
		ConnParams: map[string]any{ParamURL: httpSrv.URL},
	}

	sess := mustNewSession(t, cfg)
	defer sess.Close()

	assertToolFlow(t, sess, "sse")
}

// --- Streamable-HTTP（Req 4.3）---

// TestIntegrationStreamableHTTPConnectAndToolFlow 用 httptest + SDK StreamableHTTPHandler 做回环，验证完整工具流程。
func TestIntegrationStreamableHTTPConnectAndToolFlow(t *testing.T) {
	srv := newMockMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	httpSrv := httptest.NewServer(handler)
	defer httpSrv.Close()

	cfg := domain.UpstreamConfig{
		Name:       "streamable-http-upstream",
		Transport:  domain.TransportStreamableHTTP,
		ConnParams: map[string]any{ParamURL: httpSrv.URL},
	}

	sess := mustNewSession(t, cfg)
	defer sess.Close()

	assertToolFlow(t, sess, "streamable-http")
}

// --- WebSocket（Req 4.4）---

// fixedConnTransport 是一个返回预置 mcp.Connection 的服务端 mcp.Transport，
// 用于把已接受的 WebSocket 连接交给 SDK server 驱动。
type fixedConnTransport struct {
	conn mcp.Connection
}

// Connect 返回预置连接（仅被 Server.Connect 调用一次）。
func (t *fixedConnTransport) Connect(_ context.Context) (mcp.Connection, error) {
	return t.conn, nil
}

// newWSMCPServer 启动一个承载 MCP 的 WebSocket 回环服务：接受 WS 连接后复用本包 wsConn
// 适配为 mcp.Connection，并交给 SDK mock server 驱动，阻塞至客户端断开。
func newWSMCPServer() *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(wsReadLimit)

		connCtx, cancel := context.WithCancel(context.Background())
		conn := &wsConn{conn: c, ctx: connCtx, cancel: cancel}

		srv := newMockMCPServer()
		ss, err := srv.Connect(context.Background(), &fixedConnTransport{conn: conn}, nil)
		if err != nil {
			cancel()
			_ = c.Close(websocket.StatusInternalError, "connect failed")
			return
		}
		// 阻塞至客户端断开；返回后该 HTTP 请求才算完成，httptest.Server.Close 据此等待。
		_ = ss.Wait()
	})
	return httptest.NewServer(handler)
}

// httpToWS 将 httptest 的 http(s):// 地址转换为 ws(s):// 地址（工厂会校验 WS 协议）。
func httpToWS(httpURL string) string {
	if after, ok := strings.CutPrefix(httpURL, "https://"); ok {
		return "wss://" + after
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

// TestIntegrationWebSocketConnectAndToolFlow 用 coder/websocket 回环 + SDK server 验证 WS 传输完整工具流程。
func TestIntegrationWebSocketConnectAndToolFlow(t *testing.T) {
	httpSrv := newWSMCPServer()
	defer httpSrv.Close()

	cfg := domain.UpstreamConfig{
		Name:       "websocket-upstream",
		Transport:  domain.TransportWebSocket,
		ConnParams: map[string]any{ParamURL: httpToWS(httpSrv.URL)},
	}

	sess := mustNewSession(t, cfg)
	// 先于服务器关闭客户端会话，使服务端 ss.Wait 返回、handler 结束，httptest.Close 不致挂起。
	defer sess.Close()

	assertToolFlow(t, sess, "websocket")
}

// --- 连接超时与失败路径（Req 4.9）---

// TestIntegrationConnectTimeoutMarksFailure 用「接受连接但永不完成初始化握手」的静默服务器，
// 配较短连接超时，验证 Connect 在超时后中止并返回连接超时错误（标记建立失败，Req 4.9）。
func TestIntegrationConnectTimeoutMarksFailure(t *testing.T) {
	// 静默 SSE 端点：接受 HTTP 请求但永不发送 endpoint 事件，使 SDK 的 initialize 握手无法完成。
	silent := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer silent.Close()

	cfg := domain.UpstreamConfig{
		Name:       "silent-sse-upstream",
		Transport:  domain.TransportSSE,
		ConnParams: map[string]any{ParamURL: silent.URL},
	}

	sess := mustNewSession(t, cfg)
	defer sess.Close()

	// 较短超时以快速触发 Req 4.9 的超时分支。
	setConnectTimeout(sess, 300*time.Millisecond)

	start := time.Now()
	err := sess.Connect(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("静默服务器下 Connect 应因超时而失败")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *domain.APIError，实际 err=%v", err)
	}
	if apiErr.Code != domain.CodeUpstreamTimeout {
		t.Fatalf("应返回 UPSTREAM_TIMEOUT 错误码，实际=%s err=%v", apiErr.Code, err)
	}
	// 应在接近配置超时处返回，而非长时间挂起（给握手与调度留出充足裕量）。
	if elapsed > 5*time.Second {
		t.Fatalf("Connect 超时返回过慢：耗时 %v", elapsed)
	}
}

// TestIntegrationUnreachableConnectFails 对不可达地址做失败路径冒烟：Connect 必须失败并返回统一错误。
func TestIntegrationUnreachableConnectFails(t *testing.T) {
	cfg := domain.UpstreamConfig{
		Name:      "unreachable-http-upstream",
		Transport: domain.TransportStreamableHTTP,
		// 127.0.0.1:1 几乎必然无监听者，连接会被拒绝或超时。
		ConnParams: map[string]any{ParamURL: "http://127.0.0.1:1/mcp"},
	}

	sess := mustNewSession(t, cfg)
	defer sess.Close()

	setConnectTimeout(sess, 2*time.Second)

	err := sess.Connect(context.Background())
	if err == nil {
		t.Fatal("不可达地址下 Connect 应失败")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *domain.APIError，实际 err=%v", err)
	}
	switch apiErr.Code {
	case domain.CodeUpstreamUnavailable, domain.CodeUpstreamTimeout:
		// 连接被拒绝（不可用）或超时均为可接受的失败标记。
	default:
		t.Fatalf("不可达地址应返回 UPSTREAM_UNAVAILABLE 或 UPSTREAM_TIMEOUT，实际=%s err=%v", apiErr.Code, err)
	}
}

func TestIsSessionLostClassifiesOnlyConnectionTerminalErrors(t *testing.T) {
	if !IsSessionLost(ErrSessionLost) {
		t.Fatal("显式会话终态错误应被识别")
	}
	if !IsSessionLost(fmt.Errorf("sdk: client is closing: failed to reconnect")) {
		t.Fatal("SDK 明确关闭/重连耗尽语义应被识别")
	}
	if !IsSessionLost(mcp.ErrSessionMissing) {
		t.Fatal("Streamable HTTP 服务端会话缺失应被识别为会话终态")
	}
	if IsSessionLost(context.DeadlineExceeded) {
		t.Fatal("调用超时不应被当作会话终态")
	}
	if IsSessionLost(errors.New("工具参数不合法")) {
		t.Fatal("业务错误不应被当作会话终态")
	}
	if IsSessionLost(&net.OpError{Op: "read", Err: errors.New("瞬态代理错误")}) {
		t.Fatal("裸网络操作错误不应在缺少终态证据时拆掉逻辑会话")
	}
}

func TestStdioClientConnForwardsLifecycleWaiter(t *testing.T) {
	inner := &sdkClientConn{done: make(chan struct{})}
	waitErr := errors.New("stdio child exited")
	inner.waitErr = waitErr
	close(inner.done)
	wrapped := &stdioClientConn{inner: inner}

	err := wrapped.WaitClosed(context.Background())
	if !errors.Is(err, ErrSessionLost) {
		t.Fatalf("stdio 装饰器应透传 SDK 会话终态，got=%v", err)
	}
}
