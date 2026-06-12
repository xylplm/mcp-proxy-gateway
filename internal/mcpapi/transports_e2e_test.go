package mcpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 13.5「编写对外三种传输端到端集成测试」的端到端集成测试（Req 11.8）。
//
// 任务 13.4 的 endpoints_test.go 已覆盖「全量模式」下三种传输（SSE/Streamable-HTTP/WebSocket）
// 的工具列表与调用；本文件作为互补，覆盖「智能模式」下三种传输的端到端路径：
//   - 经网关工具 list_tools 发现当前可见聚合工具（工具列表）；
//   - 经网关工具 call_tool 路由到具体聚合工具并原样回传结果（工具调用）。
//
// 这样三种对外传输在两种模式（全量 + 智能）下的「工具列表 + 工具调用」均有端到端用例覆盖，
// 验证差异仅在传输形式、聚合能力完全一致（Req 11.8）。

// epListToolsViaGateway 经智能模式网关工具 list_tools 发现可见聚合工具，返回解析后的分页结果。
//
// list_tools 的结果以 JSON 文本 content 回传（见 service.handleGatewayListTools → jsonResult），
// 故此处取首个文本 content 反序列化为 ToolPage。
func epListToolsViaGateway(t *testing.T, cs *mcp.ClientSession) ToolPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      GatewayToolListTools,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("调用网关工具 list_tools 失败：%v", err)
	}
	if res.IsError {
		t.Fatalf("list_tools 不应标记为错误")
	}
	if len(res.Content) == 0 {
		t.Fatalf("list_tools 应返回 JSON 文本内容")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("list_tools 返回内容类型应为文本，got=%T", res.Content[0])
	}
	var page ToolPage
	if err := json.Unmarshal([]byte(text.Text), &page); err != nil {
		t.Fatalf("解析 list_tools 分页结果失败：%v，raw=%s", err, text.Text)
	}
	return page
}

// epCallToolViaGateway 经智能模式网关工具 call_tool 路由到具体聚合工具并返回原始结果。
func epCallToolViaGateway(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      GatewayToolCallTool,
		Arguments: map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("调用网关工具 call_tool 失败：%v", err)
	}
	return res
}

// epAssertSmartModeDiscoverAndCall 在智能模式下断言：
//  1. 经 list_tools 能发现全部可见聚合工具（工具列表）；
//  2. 经 call_tool 能路由到具体聚合工具且原始参数透传（工具调用）。
func epAssertSmartModeDiscoverAndCall(t *testing.T, cs *mcp.ClientSession, agg *epFakeAggregation) {
	t.Helper()

	// 工具列表：list_tools 应返回两条聚合工具摘要。
	page := epListToolsViaGateway(t, cs)
	if len(page.Tools) != 2 {
		t.Fatalf("智能模式 list_tools 应发现 2 个聚合工具，got=%d", len(page.Tools))
	}
	names := map[string]bool{}
	for _, ts := range page.Tools {
		names[ts.Name] = true
	}
	if !names["fs_read"] || !names["db_query"] {
		t.Fatalf("list_tools 未返回预期工具，got=%v", names)
	}

	// 工具调用：call_tool 应路由到聚合服务并透传原始参数。
	res := epCallToolViaGateway(t, cs, "db_query", map[string]any{"sql": "select 1"})
	if res.IsError {
		t.Fatalf("call_tool 不应标记为错误")
	}
	if agg.gotInvokeName != "db_query" {
		t.Fatalf("call_tool 未路由到聚合服务：got name=%q", agg.gotInvokeName)
	}
	if !strings.Contains(agg.gotInvokeArgs, "select 1") {
		t.Fatalf("call_tool 原始参数未透传：got=%s", agg.gotInvokeArgs)
	}
}

// epNewSmartAgg 构造智能模式端到端测试用的聚合服务假实现（两条工具 + 固定调用结果）。
func epNewSmartAgg() *epFakeAggregation {
	return &epFakeAggregation{
		buildResult:  epToolDefs(),
		invokeResult: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)},
	}
}

// TestSmartModeSSEDiscoverAndCall 验证 SSE 传输下智能模式的工具列表与调用端到端（Req 11.8）。
func TestSmartModeSSEDiscoverAndCall(t *testing.T) {
	agg := epNewSmartAgg()
	srv := epNewTestServer(t, agg)

	cs := epConnectClient(t, &mcp.SSEClientTransport{Endpoint: srv.URL + PathSmartSSE + "?api_key=key-sse"})
	epAssertSmartModeDiscoverAndCall(t, cs, agg)

	if agg.gotInvokeID != "key-sse" {
		t.Fatalf("call_tool 应按已鉴权 API Key 视角路由，got=%q", agg.gotInvokeID)
	}
}

// TestSmartModeStreamableHTTPDiscoverAndCall 验证 Streamable-HTTP 传输下智能模式的工具列表与调用端到端（Req 11.8）。
func TestSmartModeStreamableHTTPDiscoverAndCall(t *testing.T) {
	agg := epNewSmartAgg()
	srv := epNewTestServer(t, agg)

	cs := epConnectClient(t, &mcp.StreamableClientTransport{Endpoint: srv.URL + PathSmartHTTP + "?api_key=key-http"})
	epAssertSmartModeDiscoverAndCall(t, cs, agg)

	if agg.gotInvokeID != "key-http" {
		t.Fatalf("call_tool 应按已鉴权 API Key 视角路由，got=%q", agg.gotInvokeID)
	}
}

// TestSmartModeWebSocketDiscoverAndCall 验证 WebSocket 传输下智能模式的工具列表与调用端到端（Req 11.8）。
func TestSmartModeWebSocketDiscoverAndCall(t *testing.T) {
	agg := epNewSmartAgg()
	srv := epNewTestServer(t, agg)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + PathSmartWS + "?api_key=key-ws"
	cs := epConnectClient(t, &epWSClientTransport{endpoint: wsURL})
	epAssertSmartModeDiscoverAndCall(t, cs, agg)

	if agg.gotInvokeID != "key-ws" {
		t.Fatalf("call_tool 应按已鉴权 API Key 视角路由，got=%q", agg.gotInvokeID)
	}
}
