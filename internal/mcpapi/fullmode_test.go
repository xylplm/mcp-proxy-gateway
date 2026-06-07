package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 13.1「实现全量工具模式」的单元测试，使用 fake Aggregation_Service，
// 覆盖以下核心功能逻辑（Req 11.1、11.2 及其依赖的聚合调用语义 Req 10.3、10.4、11.7）：
//   - ListTools 一次性返回全部可见聚合工具定义，且对外名称/描述/InputSchema 正确转换；
//   - 无可见工具时返回空列表而非错误（Req 10.7）；
//   - InputSchema 为空时回退到最小合法 JSON Schema；
//   - ListTools 透传 apiKeyID 给聚合服务（不同 API Key 视角可见集合不同）；
//   - CallTool 调用路由透传：name 与原始 args 原样传给 InvokeTool，结果原样返回；
//   - CallTool 在工具不可见时上抛 TOOL_NOT_FOUND 且不返回结果。
//
// fake 与辅助统一以 fm 前缀命名，避免与同包其它测试（如智能模式 13.2）的标识符冲突。

// fmFakeAggregation 是 domain.Aggregation_Service 的内存假实现。
//
// buildResult/buildErr 控制 BuildToolSet 返回；invokeResult/invokeErr 控制 InvokeTool
// 返回。同时记录最近一次调用的入参，便于断言「apiKeyID / name / args 透传」。
type fmFakeAggregation struct {
	// BuildToolSet 行为与记录。
	buildResult   []domain.ToolDef
	buildErr      error
	gotBuildKeyID string
	buildCalls    int

	// InvokeTool 行为与记录。
	invokeResult   domain.ToolResult
	invokeErr      error
	gotInvokeKeyID string
	gotInvokeName  string
	gotInvokeArgs  json.RawMessage
	invokeCalled   bool
}

func (f *fmFakeAggregation) BuildToolSet(_ context.Context, apiKeyID string) ([]domain.ToolDef, error) {
	f.buildCalls++
	f.gotBuildKeyID = apiKeyID
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.buildResult, nil
}

func (f *fmFakeAggregation) InvokeTool(_ context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	f.invokeCalled = true
	f.gotInvokeKeyID = apiKeyID
	f.gotInvokeName = exposedName
	f.gotInvokeArgs = args
	if f.invokeErr != nil {
		return domain.ToolResult{}, f.invokeErr
	}
	return f.invokeResult, nil
}

// 编译期断言：fake 必须满足 domain.Aggregation_Service 接口契约。
var _ domain.Aggregation_Service = (*fmFakeAggregation)(nil)

// TestFullModeListToolsReturnsAllTools 验证：全量模式一次性返回全部可见聚合工具定义，
// 且名称/描述/InputSchema 被正确转换为 MCP 工具定义（Req 11.1、11.2）。
func TestFullModeListToolsReturnsAllTools(t *testing.T) {
	agg := &fmFakeAggregation{
		buildResult: []domain.ToolDef{
			{
				OriginalName: "read_file",
				Name:         "fs_read",
				Description:  "读取文件",
				InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
				UpstreamID:   "up-a",
			},
			{
				OriginalName: "list_dir",
				Name:         "fs_list",
				Description:  "列出目录",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				UpstreamID:   "up-a",
			},
			{
				OriginalName: "query",
				Name:         "db_query",
				Description:  "执行查询",
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				UpstreamID:   "up-b",
			},
		},
	}
	h := NewFullModeHandler(agg)

	tools, err := h.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools 不应返回错误，got err=%v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("应一次性返回全部 3 个工具，got=%d", len(tools))
	}

	// 按对外名校验转换结果（顺序应与聚合输出一致）。
	if tools[0].Name != "fs_read" || tools[0].Description != "读取文件" {
		t.Fatalf("第 1 个工具转换错误：got name=%q desc=%q", tools[0].Name, tools[0].Description)
	}
	// InputSchema 应原样透传（json.RawMessage 赋给 any 字段）。
	gotSchema, ok := tools[0].InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema 类型应为 json.RawMessage，got %T", tools[0].InputSchema)
	}
	if string(gotSchema) != `{"type":"object","properties":{"path":{"type":"string"}}}` {
		t.Fatalf("InputSchema 未原样透传：got=%s", gotSchema)
	}
	if tools[2].Name != "db_query" {
		t.Fatalf("第 3 个工具对外名错误：got=%q want=%q", tools[2].Name, "db_query")
	}
}

// TestFullModeListToolsEmpty 验证：无可见工具时返回空列表（非 nil）而非错误（Req 10.7）。
func TestFullModeListToolsEmpty(t *testing.T) {
	agg := &fmFakeAggregation{buildResult: nil}
	h := NewFullModeHandler(agg)

	tools, err := h.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("无可见工具时不应返回错误，got err=%v", err)
	}
	if tools == nil {
		t.Fatalf("应返回非 nil 的空切片")
	}
	if len(tools) != 0 {
		t.Fatalf("应返回空工具列表，got=%d", len(tools))
	}
}

// TestFullModeListToolsDefaultSchema 验证：工具 InputSchema 为空时回退到最小合法 JSON Schema。
func TestFullModeListToolsDefaultSchema(t *testing.T) {
	agg := &fmFakeAggregation{
		buildResult: []domain.ToolDef{
			{OriginalName: "noargs", Name: "noargs", Description: "无入参工具", InputSchema: nil},
		},
	}
	h := NewFullModeHandler(agg)

	tools, err := h.ListTools(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTools 不应返回错误，got err=%v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("应返回 1 个工具，got=%d", len(tools))
	}
	gotSchema, ok := tools[0].InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("InputSchema 类型应为 json.RawMessage，got %T", tools[0].InputSchema)
	}
	if string(gotSchema) != `{"type":"object"}` {
		t.Fatalf("空 InputSchema 应回退到最小合法 Schema，got=%s", gotSchema)
	}
}

// TestFullModeListToolsPassesAPIKeyID 验证：ListTools 把 apiKeyID 透传给聚合服务，
// 使不同 API Key 视角得到各自的可见集合（Req 11.2 基于完整管线含 API Key 级过滤）。
func TestFullModeListToolsPassesAPIKeyID(t *testing.T) {
	agg := &fmFakeAggregation{}
	h := NewFullModeHandler(agg)

	if _, err := h.ListTools(context.Background(), "key-42"); err != nil {
		t.Fatalf("ListTools 不应返回错误，got err=%v", err)
	}
	if agg.gotBuildKeyID != "key-42" {
		t.Fatalf("apiKeyID 未透传给聚合服务：got=%q want=%q", agg.gotBuildKeyID, "key-42")
	}
}

// TestFullModeListToolsPropagatesError 验证：聚合服务构建失败时 ListTools 原样上抛错误。
func TestFullModeListToolsPropagatesError(t *testing.T) {
	wantErr := domain.NewError(domain.CodeUpstreamUnavailable, "缓存读取失败")
	agg := &fmFakeAggregation{buildErr: wantErr}
	h := NewFullModeHandler(agg)

	_, err := h.ListTools(context.Background(), "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("应原样上抛聚合错误，got err=%v", err)
	}
}

// TestFullModeCallToolRoutesThrough 验证：CallTool 把 apiKeyID/name/原始 args 原样透传给
// InvokeTool，并将上游结果原样转换返回（Req 10.3、调用路由透传）。
func TestFullModeCallToolRoutesThrough(t *testing.T) {
	wantContent := json.RawMessage(`[{"type":"text","text":"ok"}]`)
	agg := &fmFakeAggregation{
		invokeResult: domain.ToolResult{IsError: false, Content: wantContent},
	}
	h := NewFullModeHandler(agg)

	args := json.RawMessage(`{"path":"/tmp/a.txt"}`)
	res, err := h.CallTool(context.Background(), "key-1", "fs_read", args)
	if err != nil {
		t.Fatalf("CallTool 不应返回错误，got err=%v", err)
	}
	if !agg.invokeCalled {
		t.Fatalf("CallTool 应路由到聚合服务 InvokeTool")
	}
	if agg.gotInvokeKeyID != "key-1" {
		t.Fatalf("apiKeyID 未透传：got=%q want=%q", agg.gotInvokeKeyID, "key-1")
	}
	if agg.gotInvokeName != "fs_read" {
		t.Fatalf("工具名未透传：got=%q want=%q", agg.gotInvokeName, "fs_read")
	}
	if string(agg.gotInvokeArgs) != string(args) {
		t.Fatalf("原始参数未原样透传：got=%s want=%s", agg.gotInvokeArgs, args)
	}
	if res.IsError {
		t.Fatalf("成功结果不应标记为错误")
	}
	// 结果 content 应保留原始 text 内容。
	raw, mErr := json.Marshal(res.Content)
	if mErr != nil {
		t.Fatalf("序列化结果 content 失败：%v", mErr)
	}
	if !json.Valid(raw) {
		t.Fatalf("结果 content 不是合法 JSON：%s", raw)
	}
}

// TestFullModeCallToolPropagatesError 验证：工具不可见时 InvokeTool 返回 TOOL_NOT_FOUND，
// CallTool 原样上抛该错误且不返回结果（Req 10.4、11.7）。
func TestFullModeCallToolPropagatesError(t *testing.T) {
	notFound := domain.NewError(domain.CodeToolNotFound, "工具不存在于当前可见聚合工具集合中")
	agg := &fmFakeAggregation{invokeErr: notFound}
	h := NewFullModeHandler(agg)

	res, err := h.CallTool(context.Background(), "", "ghost_tool", json.RawMessage(`{}`))
	if res != nil {
		t.Fatalf("出错时不应返回结果，got=%+v", res)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeToolNotFound {
		t.Fatalf("期望错误码 %s，got %s", domain.CodeToolNotFound, apiErr.Code)
	}
}
