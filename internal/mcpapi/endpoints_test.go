package mcpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 13.4「实现多传输对外端点」的端到端集成测试，覆盖 Req 11.8、11.9：
//   - 三种对外传输（SSE / Streamable-HTTP / WebSocket）暴露同一聚合能力：tools/list 与 tools/call
//     在三种传输下行为一致；
//   - 构建 server 前会经注入的 APIKeyResolver 取出已鉴权 API Key 视角（Req 11.9 的接线点）；
//   - 工具调用经聚合服务 InvokeTool 路由并原样回传结果。
//
// fake 与辅助统一以 ep 前缀命名，避免与同包其它测试的标识符冲突。

// epFakeAggregation 是 domain.Aggregation_Service 的内存假实现，记录最近一次 BuildToolSet
// 与 InvokeTool 的 apiKeyID/name/args 入参，便于断言透传与 API Key 视角。
type epFakeAggregation struct {
	mu sync.Mutex

	buildResult []domain.ToolDef
	gotBuildIDs []string

	invokeResult   domain.ToolResult
	gotInvokeID    string
	gotInvokeName  string
	gotInvokeArgs  string
	invokeCalledAt int
}

func (f *epFakeAggregation) BuildToolSet(_ context.Context, apiKeyID string) ([]domain.ToolDef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotBuildIDs = append(f.gotBuildIDs, apiKeyID)
	return f.buildResult, nil
}

func (f *epFakeAggregation) InvokeTool(_ context.Context, apiKeyID, name string, args json.RawMessage) (domain.ToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invokeCalledAt++
	f.gotInvokeID = apiKeyID
	f.gotInvokeName = name
	f.gotInvokeArgs = string(args)
	return f.invokeResult, nil
}

var _ domain.Aggregation_Service = (*epFakeAggregation)(nil)

// epNewTestServer 启动一个挂载了三种对外传输端点的 httptest 服务器。
//
// resolveKey 模拟前置鉴权中间件：本测试直接从查询参数 api_key 取标识写入 gin.Context，
// 再由 APIKeyResolver 取出，等价于真实链路中鉴权中间件写入元数据后的读取。
func epNewTestServer(t *testing.T, agg domain.Aggregation_Service, mode string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// 模拟鉴权中间件：把 api_key 查询参数作为已鉴权 API Key 标识写入上下文。
	r.Use(func(c *gin.Context) {
		c.Set("test.apikey", c.Query("api_key"))
		c.Next()
	})

	svc := NewService(agg, mode, 50, nil)
	resolve := func(c *gin.Context) (string, bool) {
		v, ok := c.Get("test.apikey")
		if !ok {
			return "", false
		}
		return v.(string), true
	}
	eps := NewEndpoints(svc, resolve, nil)
	eps.Register(r)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// epToolDefs 返回测试用的两条聚合工具定义。
func epToolDefs() []domain.ToolDef {
	return []domain.ToolDef{
		{OriginalName: "read_file", Name: "fs_read", Description: "读取文件", InputSchema: json.RawMessage(`{"type":"object"}`), UpstreamID: "up-a"},
		{OriginalName: "query", Name: "db_query", Description: "执行查询", InputSchema: json.RawMessage(`{"type":"object"}`), UpstreamID: "up-b"},
	}
}

// epConnectClient 用给定 transport 建立 MCP 客户端会话。
func epConnectClient(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("客户端连接失败：%v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// epAssertFullModeToolsAndCall 在全量模式下断言 tools/list 返回全部工具，且 tools/call 路由到聚合服务。
func epAssertFullModeToolsAndCall(t *testing.T, cs *mcp.ClientSession, agg *epFakeAggregation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list 失败：%v", err)
	}
	if len(listed.Tools) != 2 {
		t.Fatalf("全量模式应暴露 2 个聚合工具，got=%d", len(listed.Tools))
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fs_read",
		Arguments: map[string]any{"path": "/tmp/a.txt"},
	})
	if err != nil {
		t.Fatalf("tools/call 失败：%v", err)
	}
	if res.IsError {
		t.Fatalf("调用不应标记为错误")
	}
	if agg.gotInvokeName != "fs_read" {
		t.Fatalf("调用未路由到聚合服务：got name=%q", agg.gotInvokeName)
	}
	if !strings.Contains(agg.gotInvokeArgs, "/tmp/a.txt") {
		t.Fatalf("原始参数未透传：got=%s", agg.gotInvokeArgs)
	}
}

// TestEndpointsSSEFullMode 验证 SSE 传输下全量模式的工具列表与调用（Req 11.8、11.9）。
func TestEndpointsSSEFullMode(t *testing.T) {
	agg := &epFakeAggregation{
		buildResult:  epToolDefs(),
		invokeResult: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)},
	}
	srv := epNewTestServer(t, agg, ModeFull)

	cs := epConnectClient(t, &mcp.SSEClientTransport{Endpoint: srv.URL + PathSSE + "?api_key=key-sse"})
	epAssertFullModeToolsAndCall(t, cs, agg)

	if len(agg.gotBuildIDs) == 0 || agg.gotBuildIDs[0] != "key-sse" {
		t.Fatalf("应按已鉴权 API Key 视角构建 server，got=%v", agg.gotBuildIDs)
	}
}

// TestEndpointsStreamableHTTPFullMode 验证 Streamable-HTTP 传输下全量模式的工具列表与调用。
func TestEndpointsStreamableHTTPFullMode(t *testing.T) {
	agg := &epFakeAggregation{
		buildResult:  epToolDefs(),
		invokeResult: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)},
	}
	srv := epNewTestServer(t, agg, ModeFull)

	cs := epConnectClient(t, &mcp.StreamableClientTransport{Endpoint: srv.URL + PathHTTP + "?api_key=key-http"})
	epAssertFullModeToolsAndCall(t, cs, agg)

	if len(agg.gotBuildIDs) == 0 || agg.gotBuildIDs[0] != "key-http" {
		t.Fatalf("应按已鉴权 API Key 视角构建 server，got=%v", agg.gotBuildIDs)
	}
}

// TestEndpointsSmartModeExposesGatewayTools 验证智能模式下三传输只暴露四个网关工具（Req 11.3）。
//
// 以 Streamable-HTTP 为代表验证模式生效；SSE/WS 复用同一 BuildServer，无需逐一重复。
func TestEndpointsSmartModeExposesGatewayTools(t *testing.T) {
	agg := &epFakeAggregation{buildResult: epToolDefs()}
	srv := epNewTestServer(t, agg, ModeSmart)

	cs := epConnectClient(t, &mcp.StreamableClientTransport{Endpoint: srv.URL + PathHTTP + "?api_key=key-smart"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list 失败：%v", err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("智能模式应仅暴露 4 个网关工具，got=%d", len(listed.Tools))
	}
	names := map[string]bool{}
	for _, tl := range listed.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{GatewayToolListTools, GatewayToolSearchTools, GatewayToolGetTool, GatewayToolCallTool} {
		if !names[want] {
			t.Fatalf("缺少网关工具 %q，got=%v", want, names)
		}
	}
}

// epWSClientTransport 是测试用的 WebSocket 客户端 transport（实现 mcp.Transport）。
//
// 与对外 WS 服务端适配对称：拨号建立 WebSocket 连接，并以文本帧承载 JSON-RPC 消息。
type epWSClientTransport struct {
	endpoint string
}

func (t *epWSClientTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, resp, err := websocket.Dial(ctx, t.endpoint, nil)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	conn.SetReadLimit(32 << 20)
	connCtx, cancel := context.WithCancel(context.Background())
	return &epWSClientConn{conn: conn, ctx: connCtx, cancel: cancel}, nil
}

type epWSClientConn struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (c *epWSClientConn) Read(context.Context) (jsonrpc.Message, error) {
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		return nil, err
	}
	return jsonrpc.DecodeMessage(data)
}

func (c *epWSClientConn) Write(_ context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(c.ctx, websocket.MessageText, data)
}

func (c *epWSClientConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.conn.Close(websocket.StatusNormalClosure, "")
		c.cancel()
	})
	return err
}

func (c *epWSClientConn) SessionID() string { return "" }

// TestEndpointsWebSocketFullMode 验证 WebSocket 传输下全量模式的工具列表与调用（Req 11.8、11.9）。
func TestEndpointsWebSocketFullMode(t *testing.T) {
	agg := &epFakeAggregation{
		buildResult:  epToolDefs(),
		invokeResult: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)},
	}
	srv := epNewTestServer(t, agg, ModeFull)

	// httptest 服务器地址形如 http://127.0.0.1:port，转换为 ws:// 方案。
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + PathWS + "?api_key=key-ws"
	cs := epConnectClient(t, &epWSClientTransport{endpoint: wsURL})
	epAssertFullModeToolsAndCall(t, cs, agg)

	if len(agg.gotBuildIDs) == 0 || agg.gotBuildIDs[0] != "key-ws" {
		t.Fatalf("应按已鉴权 API Key 视角构建 server，got=%v", agg.gotBuildIDs)
	}
}
