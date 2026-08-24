package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type fakeAggregationTools struct {
	tools         []domain.ToolDef
	details       []domain.ToolDetail
	err           error
	invoke        domain.ToolResult
	invokeErr     error
	cacheStats    domain.ToolResultCacheStats
	clearResult   domain.ToolResultCacheClearResult
	setCalls      int
	detailCalls   int
	invokeCalls   int
	statsCalls    int
	clearCalls    int
	invokeKey     string
	invokeName    string
	invokeArgs    json.RawMessage
	detailKey     string
	clearFilter   domain.ToolResultCacheClearFilter
	invalidations int
}

func (f *fakeAggregationTools) BuildToolSet(context.Context, string) ([]domain.ToolDef, error) {
	f.setCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *fakeAggregationTools) BuildToolDetails(_ context.Context, apiKeyID string) ([]domain.ToolDetail, error) {
	f.detailCalls++
	f.detailKey = apiKeyID
	if f.err != nil {
		return nil, f.err
	}
	return f.details, nil
}

func (f *fakeAggregationTools) InvokeTool(_ context.Context, apiKeyID, exposedName string, args json.RawMessage) (domain.ToolResult, error) {
	f.invokeCalls++
	f.invokeKey = apiKeyID
	f.invokeName = exposedName
	f.invokeArgs = append(json.RawMessage(nil), args...)
	return f.invoke, f.invokeErr
}

func (f *fakeAggregationTools) ToolResultCacheStats() domain.ToolResultCacheStats {
	f.statsCalls++
	return f.cacheStats
}

func (f *fakeAggregationTools) ClearToolResultCache(filter domain.ToolResultCacheClearFilter) domain.ToolResultCacheClearResult {
	f.clearCalls++
	f.clearFilter = filter
	return f.clearResult
}

func (f *fakeAggregationTools) InvalidateToolSetCache() {
	f.invalidations++
}

func TestGetAggregatedToolSummaryUsesToolSetOnly(t *testing.T) {
	agg := &fakeAggregationTools{tools: []domain.ToolDef{
		{Name: "search"},
		{Name: "read"},
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodGet, "/api/admin/tools/summary", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.setCalls != 1 {
		t.Fatalf("摘要接口应调用 BuildToolSet 1 次，实际 %d", agg.setCalls)
	}
	if agg.detailCalls != 0 {
		t.Fatalf("摘要接口不应拉取工具详情，实际调用 %d 次", agg.detailCalls)
	}

	var got struct {
		Count int `json:"count"`
	}
	unmarshalData(t, w, &got)
	if got.Count != 2 {
		t.Fatalf("摘要工具数不符合预期：%+v", got)
	}
}

func TestListAggregatedToolsUsesToolDetails(t *testing.T) {
	agg := &fakeAggregationTools{details: []domain.ToolDetail{
		{Tool: domain.ToolDef{Name: "search"}},
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodGet, "/api/admin/tools/aggregated", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.detailCalls != 1 {
		t.Fatalf("完整工具目录应调用 BuildToolDetails 1 次，实际 %d", agg.detailCalls)
	}
	if agg.setCalls != 0 {
		t.Fatalf("完整工具目录不应额外调用 BuildToolSet，实际 %d 次", agg.setCalls)
	}

	var got struct {
		Tools       []domain.ToolDef    `json:"tools"`
		ToolDetails []domain.ToolDetail `json:"toolDetails"`
		Count       int                 `json:"count"`
	}
	unmarshalData(t, w, &got)
	if got.Count != 1 || len(got.Tools) != 1 || len(got.ToolDetails) != 1 {
		t.Fatalf("完整工具目录响应不符合预期：%+v", got)
	}
}

func TestListAggregatedToolsUsesAPIKeyPerspective(t *testing.T) {
	agg := &fakeAggregationTools{details: []domain.ToolDetail{
		{Tool: domain.ToolDef{Name: "visible_for_key"}},
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodGet, "/api/admin/tools/aggregated?apiKeyId=key-42", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.detailKey != "key-42" {
		t.Fatalf("应按 API Key 视角构建工具详情，实际 apiKeyID=%q", agg.detailKey)
	}
}

func TestGetToolResultCacheStats(t *testing.T) {
	agg := &fakeAggregationTools{cacheStats: domain.ToolResultCacheStats{
		Entries:    2,
		MaxEntries: 512,
		Hits:       7,
		Misses:     3,
		Stores:     2,
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodGet, "/api/admin/tools/cache", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.statsCalls != 1 {
		t.Fatalf("缓存统计接口应调用 ToolResultCacheStats 1 次，实际 %d", agg.statsCalls)
	}
	var got struct {
		Cache domain.ToolResultCacheStats `json:"cache"`
	}
	unmarshalData(t, w, &got)
	if got.Cache.Entries != 2 || got.Cache.Hits != 7 || got.Cache.Misses != 3 {
		t.Fatalf("缓存统计响应不符合预期：%+v", got.Cache)
	}
}

func TestClearToolResultCacheWithFilter(t *testing.T) {
	agg := &fakeAggregationTools{clearResult: domain.ToolResultCacheClearResult{
		Deleted:   1,
		Remaining: 2,
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodDelete, "/api/admin/tools/cache", `{"exposedName":"read_file","apiKeyId":"key-1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.clearCalls != 1 {
		t.Fatalf("缓存清理接口应调用 ClearToolResultCache 1 次，实际 %d", agg.clearCalls)
	}
	if agg.clearFilter.ExposedName != "read_file" || agg.clearFilter.APIKeyID != "key-1" {
		t.Fatalf("缓存清理过滤条件不符合预期：%+v", agg.clearFilter)
	}
	var got struct {
		Result domain.ToolResultCacheClearResult `json:"result"`
	}
	unmarshalData(t, w, &got)
	if got.Result.Deleted != 1 || got.Result.Remaining != 2 {
		t.Fatalf("缓存清理响应不符合预期：%+v", got.Result)
	}
}

func TestInvokeToolPlaygroundDelegatesToAggregation(t *testing.T) {
	agg := &fakeAggregationTools{invoke: domain.ToolResult{
		Content: json.RawMessage(`[{"type":"text","text":"ok"}]`),
	}}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodPost, "/api/admin/tools/playground", `{"apiKeyId":"key-1","name":"read_file","args":{"path":"README.md"}}`)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.invokeCalls != 1 {
		t.Fatalf("调试台应调用 InvokeTool 1 次，实际 %d", agg.invokeCalls)
	}
	if agg.invokeKey != "key-1" || agg.invokeName != "read_file" {
		t.Fatalf("调用视角或工具名不符合预期：key=%q name=%q", agg.invokeKey, agg.invokeName)
	}
	if string(agg.invokeArgs) != `{"path":"README.md"}` {
		t.Fatalf("入参应原样透传，实际 %s", string(agg.invokeArgs))
	}
	var got toolPlaygroundResponse
	unmarshalData(t, w, &got)
	if !got.Success || got.IsError || string(got.Content) != `[{"type":"text","text":"ok"}]` {
		t.Fatalf("调试结果不符合预期：%+v content=%s", got, string(got.Content))
	}
}

func TestInvokeToolPlaygroundReturnsCallErrorAsResult(t *testing.T) {
	agg := &fakeAggregationTools{
		invokeErr: domain.NewError(domain.CodeToolNotFound, "工具不存在"),
	}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodPost, "/api/admin/tools/playground", `{"name":"missing","args":{}}`)

	if w.Code != http.StatusOK {
		t.Fatalf("调用错误应作为调试结果返回 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got toolPlaygroundResponse
	unmarshalData(t, w, &got)
	if got.Success || got.ErrorCode != string(domain.CodeToolNotFound) || got.Error != "工具不存在" {
		t.Fatalf("调试错误结果不符合预期：%+v", got)
	}
}

func TestInvokeToolPlaygroundRejectsMissingToolName(t *testing.T) {
	agg := &fakeAggregationTools{}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodPost, "/api/admin/tools/playground", `{"args":{}}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺少工具名期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.invokeCalls != 0 {
		t.Fatalf("参数非法时不应触发工具调用，实际 %d 次", agg.invokeCalls)
	}
	_, _, fields := parseErrorEnvelope(t, w)
	if fields["name"] == "" {
		t.Fatalf("应返回 name 字段错误，实际 %+v", fields)
	}
}

func TestInvokeToolPlaygroundRejectsNonObjectArgs(t *testing.T) {
	agg := &fakeAggregationTools{}
	e := newTestEngine(Deps{Aggregation: agg})

	w := doJSON(e, http.MethodPost, "/api/admin/tools/playground", `{"name":"read_file","args":[1,2]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("非对象入参期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if agg.invokeCalls != 0 {
		t.Fatalf("入参非法时不应触发工具调用，实际 %d 次", agg.invokeCalls)
	}
	_, _, fields := parseErrorEnvelope(t, w)
	if fields["args"] == "" {
		t.Fatalf("应返回 args 字段错误，实际 %+v", fields)
	}
}
