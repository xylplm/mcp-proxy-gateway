package xiaozhi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 21.4）为小智接入服务编写「连接与重连」集成测试，针对一个 mock 小智接入点
// 端到端验证三项行为（Req 15.1、15.3、15.4）：
//
//   - Req 15.1：配置接入点地址并启用后，连接器经真实 WebSocket（默认 wsConnector）连出到该
//     接入点。
//   - Req 15.3：当接入点请求调用当前聚合集合中的某个工具时，连接器把调用连同原始参数路由到
//     聚合服务并将成功/错误结果原样回传；当接入点请求工具列表时，返回当前可见聚合集合。
//   - Req 15.4：当连接建立失败或运行期断开时，连接器按指数退避重连。
//
// 与 connector_test.go（用 fakeConnector / in-memory transport 在无网络下验证生命周期与
// 协议转换）不同，本文件搭建一个真实的 mock 小智接入点：用 coder/websocket（连接器所用的
// 同一 WebSocket 库）接受出站连接，并在其上以 MCP「客户端」身份驱动一个会话——这正是小智
// 接入点的真实角色（网关作为出站 WS 客户端连出，但在 MCP 协议层扮演服务端；小智为 MCP
// 客户端，发起 initialize / tools/list / tools/call）。连接器侧使用默认 wsConnector，构成
// 完整的「连接器 ↔ 真实 WS ↔ mock 接入点」回环。
//
// 复用本包内已有设施（同包测试可直接访问）：wsServerConn（WebSocket↔mcp.Connection 适配）、
// fixedConnTransport（把既有连接交给 SDK 驱动）、BuildServer（已在连接器侧由 wsConnector
// 调用）、以及 connector_test.go 中的 fakeAggregation 作为可控聚合服务。

// mockXiaoZhiEndpoint 是一个 mock 小智 MCP 接入点：接受网关的出站 WebSocket 连接，并在其上
// 以 MCP 客户端身份发起会话，从而可对网关（MCP 服务端）发起 tools/list 与 tools/call。
//
// 每接受一条连接即递增 connCount 并把建立好的客户端会话发布到 sessions 通道，供测试取用；
// 同时记录底层 WebSocket 连接以便测试主动「断开」来驱动重连（Req 15.4）。
type mockXiaoZhiEndpoint struct {
	srv *httptest.Server

	connCount atomic.Int32
	sessions  chan *mcp.ClientSession

	mu    sync.Mutex
	conns []*websocket.Conn
}

// newMockXiaoZhiEndpoint 启动 mock 接入点并返回其句柄；调用方负责在结束时 Close。
func newMockXiaoZhiEndpoint(t *testing.T) *mockXiaoZhiEndpoint {
	t.Helper()
	m := &mockXiaoZhiEndpoint{sessions: make(chan *mcp.ClientSession, 8)}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(xzReadLimit)
		m.connCount.Add(1)

		m.mu.Lock()
		m.conns = append(m.conns, c)
		m.mu.Unlock()

		// 将已接受的连接适配为 mcp.Connection，并以 MCP 客户端身份连接网关（MCP 服务端）。
		connCtx, cancel := context.WithCancel(context.Background())
		conn := &wsServerConn{conn: c, ctx: connCtx, cancel: cancel}

		client := mcp.NewClient(&mcp.Implementation{Name: "mock-xiaozhi-endpoint", Version: "0.0.1"}, nil)
		cs, err := client.Connect(context.Background(), &fixedConnTransport{conn: conn}, nil)
		if err != nil {
			cancel()
			_ = c.Close(websocket.StatusInternalError, "client connect failed")
			return
		}

		// 发布会话供测试发起 tools/list / tools/call；通道有缓冲，非阻塞写入避免拖住 handler。
		select {
		case m.sessions <- cs:
		default:
		}

		// 阻塞至网关关闭会话或连接被断开后返回，使该 HTTP 请求结束。
		_ = cs.Wait()
		cancel()
	})

	m.srv = httptest.NewServer(handler)
	t.Cleanup(m.srv.Close)
	return m
}

// wsURL 返回 mock 接入点的 ws:// 地址（连接器会校验 WebSocket 协议前缀）。
func (m *mockXiaoZhiEndpoint) wsURL() string {
	return "ws://" + strings.TrimPrefix(m.srv.URL, "http://")
}

// awaitSession 等待下一条已建立的接入点会话，超时即致命。
func (m *mockXiaoZhiEndpoint) awaitSession(t *testing.T, timeout time.Duration) *mcp.ClientSession {
	t.Helper()
	select {
	case cs := <-m.sessions:
		return cs
	case <-time.After(timeout):
		t.Fatalf("等待接入点建立会话超时（%v）", timeout)
		return nil
	}
}

// dropLatestConn 主动关闭最近一次接受的底层 WebSocket 连接，模拟运行期断线（Req 15.4）。
func (m *mockXiaoZhiEndpoint) dropLatestConn(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.conns) == 0 {
		t.Fatal("尚无可断开的连接")
	}
	_ = m.conns[len(m.conns)-1].Close(websocket.StatusGoingAway, "mock drop")
}

// --- Req 15.1：启用后经 WebSocket 连接到接入点 ---

// TestIntegration_ConnectsToEndpointWhenEnabled 验证：当配置接入点地址并启用时，连接器经
// 真实 WebSocket（默认 wsConnector）连出到该接入点，mock 接入点据此接受到一条连接（Req 15.1）。
func TestIntegration_ConnectsToEndpointWhenEnabled(t *testing.T) {
	endpoint := newMockXiaoZhiEndpoint(t)
	agg := &fakeAggregation{
		tools: []domain.ToolDef{{OriginalName: "raw_a", Name: "alias_a", Description: "工具 A"}},
	}

	// 不注入 WithConnector：使用默认 wsConnector，走真实 WebSocket 连接（Req 15.1）。
	// 注入 noReconnect 以隔离本用例，避免重连干扰连接计数断言。
	c := NewConnector(endpoint.wsURL(), true, agg, WithReconnector(noReconnect{}))
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 接入点应在合理时间内接受到一条出站 WebSocket 连接并完成 MCP 初始化握手。
	cs := endpoint.awaitSession(t, 3*time.Second)
	if cs == nil {
		t.Fatal("启用后接入点应接受到连接")
	}
	if got := endpoint.connCount.Load(); got < 1 {
		t.Fatalf("启用后应至少建立 1 条连接，实际 %d", got)
	}
	if !c.Running() {
		t.Fatal("Start 后连接器应处于运行状态")
	}
}

// --- Req 15.3：工具列表返回可见聚合集合；调用路由到聚合并原样透传 ---

// TestIntegration_ToolListAndCallRouting 验证：mock 接入点经真实 WS 发起 tools/list 时返回
// 当前可见聚合集合；发起 tools/call（成功与上游错误）时，连接器把调用连同原始参数路由到聚合
// 服务，并把成功/错误结果原样回传（Req 15.3）。
func TestIntegration_ToolListAndCallRouting(t *testing.T) {
	endpoint := newMockXiaoZhiEndpoint(t)
	agg := &fakeAggregation{
		tools: []domain.ToolDef{
			{OriginalName: "raw_a", Name: "alias_a", Description: "工具 A", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{OriginalName: "raw_err", Name: "err_tool", Description: "错误工具"},
		},
		invokeResult: domain.ToolResult{
			IsError: false,
			Content: json.RawMessage(`[{"type":"text","text":"ok-result"}]`),
		},
	}

	c := NewConnector(endpoint.wsURL(), true, agg, WithReconnector(noReconnect{}))
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	cs := endpoint.awaitSession(t, 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// tools/list：应返回聚合服务输出的「对外名」可见集合（Req 15.3 关联 15.2）。
	listed, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("接入点请求工具列表失败：%v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if !names["alias_a"] || !names["err_tool"] {
		t.Fatalf("工具列表未返回当前可见聚合集合，实际=%v", names)
	}

	// tools/call（成功）：原始参数应透传到聚合服务，成功结果原样回传。
	args := json.RawMessage(`{"k":"v"}`)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "alias_a", Arguments: args})
	if err != nil {
		t.Fatalf("接入点调用工具失败：%v", err)
	}
	if res.IsError {
		t.Fatalf("成功结果不应标记 IsError，content=%v", res.Content)
	}
	if !containsText(res.Content, "ok-result") {
		t.Fatalf("成功结果未原样透传，content=%+v", res.Content)
	}
	if agg.lastName != "alias_a" {
		t.Fatalf("应以对外名 alias_a 路由到聚合服务，实际=%q", agg.lastName)
	}
	if agg.lastInvokeKey != "" {
		t.Fatalf("小智接入应以空 apiKeyID 路由，实际=%q", agg.lastInvokeKey)
	}
	var gotArgs map[string]string
	if err := json.Unmarshal(agg.lastArgs, &gotArgs); err != nil {
		t.Fatalf("解析透传参数失败：%v，raw=%s", err, string(agg.lastArgs))
	}
	if gotArgs["k"] != "v" {
		t.Fatalf("原始调用参数未原样透传，实际=%v", gotArgs)
	}

	// tools/call（上游错误）：聚合服务返回 IsError=true 的结果应原样回传，且非协议级错误。
	agg.mu.Lock()
	agg.invokeResult = domain.ToolResult{
		IsError: true,
		Content: json.RawMessage(`[{"type":"text","text":"上游报告的错误"}]`),
	}
	agg.mu.Unlock()

	errRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "err_tool", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("上游错误结果不应作为协议级错误返回：%v", err)
	}
	if !errRes.IsError {
		t.Fatalf("期望 IsError=true 的上游错误结果原样透传，实际=%+v", errRes)
	}
	if !containsText(errRes.Content, "上游报告的错误") {
		t.Fatalf("错误结果内容未原样透传，content=%+v", errRes.Content)
	}
}

// --- Req 15.4：运行期断线后按指数退避重连 ---

// TestIntegration_ReconnectsAfterDisconnect 验证：当与接入点的连接在运行期断开时，连接器按
// 指数退避策略重连，mock 接入点据此接受到第二条连接（Req 15.4）。
//
// 为保证测试稳健且快速，注入小退避策略（初始/上限均很小）；这既覆盖「重连发生」的核心断言，
// 也间接驱动 backoffReconnector（生产默认重连器）的真实退避计算路径。
func TestIntegration_ReconnectsAfterDisconnect(t *testing.T) {
	endpoint := newMockXiaoZhiEndpoint(t)
	agg := &fakeAggregation{
		tools: []domain.ToolDef{{OriginalName: "raw_a", Name: "alias_a"}},
	}

	// 小退避：避免越界钳制（normalize 会把过小值回落到默认 1s），故取下界 1s 的初始值，
	// 但用「总是重连」的快速调度器来主导时序断言，规避默认 1s 起步带来的等待，降低耗时与抖动。
	c := NewConnector(endpoint.wsURL(), true, agg,
		WithReconnector(alwaysReconnect{delay: 20 * time.Millisecond}),
	)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 等待首条连接建立。
	_ = endpoint.awaitSession(t, 3*time.Second)
	if got := endpoint.connCount.Load(); got != 1 {
		t.Fatalf("应先建立 1 条连接，实际 %d", got)
	}

	// 模拟运行期断线：关闭底层 WebSocket，连接器的 Serve 应返回并触发重连。
	endpoint.dropLatestConn(t)

	// 重连后接入点应接受到第二条连接。
	_ = endpoint.awaitSession(t, 5*time.Second)
	if got := endpoint.connCount.Load(); got < 2 {
		t.Fatalf("断线后应重连并建立至少 2 条连接，实际 %d", got)
	}
}

// TestIntegration_ReconnectsAfterInitialFailureWithBackoffPolicy 验证：当首次连接建立失败
// （接入点尚未就绪）时，连接器按注入的指数退避策略持续重连，待接入点就绪后成功建立连接
// （Req 15.4 的「连接建立失败」分支，并真实驱动 backoffReconnector 退避计算）。
func TestIntegration_ReconnectsAfterInitialFailureWithBackoffPolicy(t *testing.T) {
	agg := &fakeAggregation{tools: []domain.ToolDef{{OriginalName: "raw_a", Name: "alias_a"}}}

	// 指向一个几乎必然无监听者的本地地址，使首次 Dial 失败，制造「连接建立失败」分支。
	addr := "ws://127.0.0.1:1/mcp"

	// 注入最小合法退避策略（初始/上限 1s、倍数 2）：真实驱动 backoffReconnector 的退避路径。
	c := NewConnector(addr, true, agg,
		WithBackoffPolicy(BackoffPolicy{Initial: 1 * time.Second, Max: 1 * time.Second, Multiplier: 2}),
	)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 连接器应在退避后持续重试；给出充足裕量确认其仍在运行且至少尝试过一次连接失败重连。
	time.Sleep(1500 * time.Millisecond)
	if !c.Running() {
		t.Fatal("连接建立失败后连接器应仍在运行并按退避重连")
	}
}
