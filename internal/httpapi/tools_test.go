package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type fakeAggregationTools struct {
	tools       []domain.ToolDef
	details     []domain.ToolDetail
	err         error
	setCalls    int
	detailCalls int
}

func (f *fakeAggregationTools) BuildToolSet(context.Context, string) ([]domain.ToolDef, error) {
	f.setCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *fakeAggregationTools) BuildToolDetails(context.Context, string) ([]domain.ToolDetail, error) {
	f.detailCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.details, nil
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
