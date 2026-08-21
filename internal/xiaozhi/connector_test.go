package xiaozhi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 21.1）为小智接入服务编写单元测试，覆盖：
//   - 工具列表请求返回聚合服务输出的当前可见集合（Req 15.2）；
//   - 调用请求路由到聚合服务并原样透传成功/错误结果（Req 15.3）；
//   - 停用时关闭连接并停止重连（Req 15.5）；
//   - 启用时建立连接、未启用时不连接（Req 15.1、15.5）。
//
// 测试策略：用 fakeAggregation 作为可控聚合服务、用 fakeConnector / mock 接入点替换真实
// WebSocket，从而在无网络的前提下验证连接管理、工具暴露与调用路由逻辑。工具暴露与调用
// 路由的 MCP 协议正确性，则通过 in-memory transport 真实驱动 BuildServer 构建的服务端来验证。

// --- 测试替身 ---

// fakeAggregation 是 domain.Aggregation_Service 的可控实现。
type fakeAggregation struct {
	mu sync.Mutex

	tools         []domain.ToolDef
	buildErr      error
	buildCalls    int
	lastBuildKey  string
	invokeResult  domain.ToolResult
	invokeErr     error
	invokeCalls   int
	lastInvokeKey string
	lastName      string
	lastArgs      json.RawMessage
}

func (f *fakeAggregation) BuildToolSet(_ context.Context, apiKeyID string) ([]domain.ToolDef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buildCalls++
	f.lastBuildKey = apiKeyID
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	// 返回副本，避免测试间共享底层数组。
	out := make([]domain.ToolDef, len(f.tools))
	copy(out, f.tools)
	return out, nil
}

func (f *fakeAggregation) InvokeTool(_ context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invokeCalls++
	f.lastInvokeKey = apiKeyID
	f.lastName = exposedName
	f.lastArgs = args
	if f.invokeErr != nil {
		return domain.ToolResult{}, f.invokeErr
	}
	return f.invokeResult, nil
}

type panicEndpointHandler struct {
	tools []domain.ToolDef
}

func (h panicEndpointHandler) ListTools(context.Context) ([]domain.ToolDef, error) {
	return h.tools, nil
}

func (h panicEndpointHandler) CallTool(context.Context, string, json.RawMessage) (domain.ToolResult, error) {
	panic("hidden xiaozhi detail")
}

// fakeConnector 是可控的 EndpointConnector：记录调用、可配置阻塞直到 ctx 取消。
type fakeConnector struct {
	mu sync.Mutex

	serveCalls   atomic.Int32
	lastEndpoint string
	lastHandler  EndpointHandler

	// blockUntilCancel 为 true 时，Serve 阻塞直至 ctx 取消（模拟保持连接）。
	blockUntilCancel bool
	// serveErr 为非阻塞模式下 Serve 的返回值。
	serveErr error

	// started/canceled 用于断言连接生命周期。
	startedCh  chan struct{}
	startsOnce sync.Once
	canceled   atomic.Bool
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{startedCh: make(chan struct{}, 1)}
}

func (f *fakeConnector) Serve(ctx context.Context, endpoint string, handler EndpointHandler) error {
	f.serveCalls.Add(1)
	f.mu.Lock()
	f.lastEndpoint = endpoint
	f.lastHandler = handler
	block := f.blockUntilCancel
	serveErr := f.serveErr
	f.mu.Unlock()

	f.startsOnce.Do(func() { close(f.startedCh) })

	if block {
		<-ctx.Done()
		f.canceled.Store(true)
		return ctx.Err()
	}
	return serveErr
}

// --- 工具列表暴露与调用路由（经 in-memory transport 真实驱动 BuildServer）---

// connectInMemory 用 in-memory 传输把 BuildServer 构建的服务端与一个 MCP 客户端会话相连，
// 返回客户端会话用于发起 tools/list 与 tools/call。
func connectInMemory(t *testing.T, handler EndpointHandler) *mcp.ClientSession {
	t.Helper()
	srv, err := BuildServer(context.Background(), handler)
	if err != nil {
		t.Fatalf("BuildServer 失败：%v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("服务端连接失败：%v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-xiaozhi-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("客户端连接失败：%v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestListToolsReturnsVisibleAggregationSet(t *testing.T) {
	agg := &fakeAggregation{
		tools: []domain.ToolDef{
			{OriginalName: "raw_a", Name: "alias_a", Description: "工具 A", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{OriginalName: "raw_b", Name: "tool_b", Description: "工具 B"},
		},
	}
	handler := newAggregationBridge(agg)
	cs := connectInMemory(t, handler)

	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools 失败：%v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("期望 2 个工具，实际 %d", len(res.Tools))
	}

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	// 返回的应为聚合服务输出的「对外名」（已应用别名重写，Req 15.2）。
	if !got["alias_a"] || !got["tool_b"] {
		t.Fatalf("工具列表未返回聚合可见集合的对外名，实际=%v", got)
	}

	// 小智接入无 API Key，应以空 apiKeyID 取全局可见集合（Req 15.2）。
	if agg.lastBuildKey != "" {
		t.Fatalf("期望以空 apiKeyID 构建集合，实际=%q", agg.lastBuildKey)
	}
}

func TestListToolsEmptyVisibleSet(t *testing.T) {
	agg := &fakeAggregation{tools: nil}
	cs := connectInMemory(t, newAggregationBridge(agg))

	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools 失败：%v", err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("空可见集合应返回空工具列表，实际 %d 个", len(res.Tools))
	}
}

func TestCallToolRoutesToAggregationAndPassesThroughSuccess(t *testing.T) {
	agg := &fakeAggregation{
		tools: []domain.ToolDef{
			{OriginalName: "raw_a", Name: "alias_a", Description: "工具 A"},
		},
		invokeResult: domain.ToolResult{
			IsError: false,
			Content: json.RawMessage(`[{"type":"text","text":"ok-result"}]`),
		},
	}
	cs := connectInMemory(t, newAggregationBridge(agg))

	args := json.RawMessage(`{"k":"v"}`)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "alias_a", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool 失败：%v", err)
	}
	if res.IsError {
		t.Fatalf("成功结果不应标记 IsError，content=%v", res.Content)
	}

	// 调用应路由到聚合服务，并以对外名与原始参数透传（Req 15.3）。
	if agg.invokeCalls != 1 {
		t.Fatalf("期望 InvokeTool 被调用 1 次，实际 %d", agg.invokeCalls)
	}
	if agg.lastName != "alias_a" {
		t.Fatalf("期望以对外名 alias_a 路由，实际=%q", agg.lastName)
	}
	if agg.lastInvokeKey != "" {
		t.Fatalf("期望以空 apiKeyID 路由，实际=%q", agg.lastInvokeKey)
	}
	var gotArgs map[string]string
	if err := json.Unmarshal(agg.lastArgs, &gotArgs); err != nil {
		t.Fatalf("解析透传参数失败：%v，raw=%s", err, string(agg.lastArgs))
	}
	if gotArgs["k"] != "v" {
		t.Fatalf("原始参数未原样透传，实际=%v", gotArgs)
	}

	// 成功结果内容应原样回传。
	raw, _ := json.Marshal(res.Content)
	if !containsText(res.Content, "ok-result") {
		t.Fatalf("成功结果内容未原样透传，content=%s", string(raw))
	}
}

func TestCallToolPassesThroughUpstreamError(t *testing.T) {
	agg := &fakeAggregation{
		tools: []domain.ToolDef{
			{OriginalName: "raw_err", Name: "err_tool", Description: "错误工具"},
		},
		invokeResult: domain.ToolResult{
			IsError: true,
			Content: json.RawMessage(`[{"type":"text","text":"上游报告的错误"}]`),
		},
	}
	cs := connectInMemory(t, newAggregationBridge(agg))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "err_tool", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("CallTool 不应返回协议级错误：%v", err)
	}
	// 上游报告的错误结果应原样透传（IsError=true）（Req 15.3）。
	if !res.IsError {
		t.Fatalf("期望 IsError=true 的上游错误结果原样透传，实际=%+v", res)
	}
	if !containsText(res.Content, "上游报告的错误") {
		t.Fatalf("错误结果内容未原样透传，content=%+v", res.Content)
	}
}

func TestCallToolRecoversHandlerPanic(t *testing.T) {
	cs := connectInMemory(t, panicEndpointHandler{
		tools: []domain.ToolDef{{OriginalName: "raw_panic", Name: "panic_tool", Description: "panic 工具"}},
	})

	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "panic_tool", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("handler panic 应转换为协议级错误")
	}
	if strings.Contains(err.Error(), "hidden xiaozhi detail") {
		t.Fatalf("panic 明细不应暴露给调用方：%v", err)
	}
}

// containsText 报告 MCP content 中是否存在包含指定文本的 TextContent。
func containsText(content []mcp.Content, want string) bool {
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if strings.Contains(tc.Text, want) {
				return true
			}
		}
	}
	return false
}

// --- 连接生命周期：启用连接、停用关闭并停止重连 ---

func TestStartConnectsWhenEnabled(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	fc.blockUntilCancel = true

	c := NewConnector("ws://endpoint.example/mcp", true, agg, WithConnector(fc), WithReconnector(noReconnect{}))
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 等待 Serve 被调用。
	select {
	case <-fc.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("启用时应建立连接，但 Serve 未被调用")
	}

	if !c.Running() {
		t.Fatal("Start 后连接器应处于运行状态")
	}
	if fc.lastEndpoint != "ws://endpoint.example/mcp" {
		t.Fatalf("Serve 收到的 endpoint 不正确：%q", fc.lastEndpoint)
	}
	if fc.serveCalls.Load() != 1 {
		t.Fatalf("期望 Serve 被调用 1 次，实际 %d", fc.serveCalls.Load())
	}
}

func TestStartNoConnectWhenDisabled(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	fc.blockUntilCancel = true

	c := NewConnector("ws://endpoint.example/mcp", false, agg, WithConnector(fc))
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 给后台循环一点时间（若错误地启动了会调用 Serve）。
	time.Sleep(100 * time.Millisecond)

	if c.Running() {
		t.Fatal("未启用时不应运行连接循环")
	}
	if fc.serveCalls.Load() != 0 {
		t.Fatalf("未启用时不应建立连接，Serve 调用次数=%d", fc.serveCalls.Load())
	}
}

func TestStopClosesConnectionAndStopsReconnect(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	fc.blockUntilCancel = true

	// 用「总是重连」的调度器，以验证 Stop 能终止重连循环（Req 15.5）。
	c := NewConnector("ws://endpoint.example/mcp", true, agg,
		WithConnector(fc),
		WithReconnector(alwaysReconnect{delay: 10 * time.Millisecond}),
	)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}

	select {
	case <-fc.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 未被调用")
	}

	c.Stop()

	// 停用后应关闭连接（Serve 因 ctx 取消而返回）且不再运行。
	if !fc.canceled.Load() {
		t.Fatal("Stop 应取消连接上下文以关闭连接")
	}
	if c.Running() {
		t.Fatal("Stop 后连接器不应处于运行状态")
	}

	// 记录此刻调用次数，确认停用后不再发起新的重连。
	callsAfterStop := fc.serveCalls.Load()
	time.Sleep(100 * time.Millisecond)
	if fc.serveCalls.Load() != callsAfterStop {
		t.Fatalf("Stop 后不应再重连，Serve 调用次数从 %d 变为 %d", callsAfterStop, fc.serveCalls.Load())
	}
}

func TestStopIsIdempotentAndStartBeforeStop(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	fc.blockUntilCancel = true

	c := NewConnector("ws://endpoint.example/mcp", true, agg, WithConnector(fc))
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	select {
	case <-fc.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 未被调用")
	}

	// 多次 Stop 应安全（幂等）。
	c.Stop()
	c.Stop()

	// 未启动时 Stop 也应安全。
	c2 := NewConnector("ws://endpoint.example/mcp", true, agg, WithConnector(newFakeConnector()))
	c2.Stop()
}

func TestReconnectStopsWhenNoReconnect(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	// 非阻塞模式：Serve 立即返回，模拟连接断开。
	fc.blockUntilCancel = false
	fc.serveErr = errors.New("断开")

	c := NewConnector("ws://endpoint.example/mcp", true, agg,
		WithConnector(fc),
		WithReconnector(noReconnect{}),
	)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	// 等待运行循环自然结束（noReconnect 下连接断开即停止）。
	deadline := time.After(2 * time.Second)
	for c.Running() {
		select {
		case <-deadline:
			t.Fatal("noReconnect 下连接断开后运行循环应结束")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// noReconnect 下应仅尝试连接一次。
	if got := fc.serveCalls.Load(); got != 1 {
		t.Fatalf("noReconnect 下应仅连接 1 次，实际 %d", got)
	}
}

// alwaysReconnect 是一个测试用的 Reconnector：以固定延迟无限重连。
type alwaysReconnect struct {
	delay time.Duration
}

func (a alwaysReconnect) NextDelay() (time.Duration, bool) { return a.delay, true }
