package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件以内存 fake 注入 AuditService，验证审计查询端点的路由装配、分页参数解析与
// 倒序分页结果回填，无需接真实仓储。

// fakeAudit 是 AuditService 的内存实现，记录最近一次入参以便断言。
type fakeAudit struct {
	result  audit.PageResult
	err     error
	gotPage int
	gotSize int
}

func (f *fakeAudit) List(_ context.Context, page, pageSize int) (audit.PageResult, error) {
	f.gotPage, f.gotSize = page, pageSize
	if f.err != nil {
		return audit.PageResult{}, f.err
	}
	return f.result, nil
}

// TestQueryAuditReturnsPage 验证审计查询返回分页记录与总数（Req 22.4）。
func TestQueryAuditReturnsPage(t *testing.T) {
	a := &fakeAudit{result: audit.PageResult{
		Records:  []store.AuditRecord{{ID: 2, EventType: "login"}, {ID: 1, EventType: "create"}},
		Page:     1,
		PageSize: 20,
		Total:    2,
	}}
	e := newTestEngine(Deps{Audit: a})

	w := doJSON(e, http.MethodGet, "/api/admin/audit?page=1&pageSize=20", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if a.gotPage != 1 || a.gotSize != 20 {
		t.Errorf("分页参数未正确接线：page=%d size=%d", a.gotPage, a.gotSize)
	}
	var got struct {
		Records  []store.AuditRecord `json:"records"`
		Page     int                 `json:"page"`
		PageSize int                 `json:"pageSize"`
		Total    int64               `json:"total"`
	}
	unmarshalData(t, w, &got)
	if len(got.Records) != 2 || got.Total != 2 {
		t.Errorf("分页结果不符：%+v", got)
	}
	// 倒序：首条应为较新的记录（ID 较大）。
	if got.Records[0].ID != 2 {
		t.Errorf("期望倒序返回，首条 ID=2，实际 %d", got.Records[0].ID)
	}
}

// TestQueryAuditDefaultParams 验证缺省分页参数以 0 透传（由审计服务收敛）。
func TestQueryAuditDefaultParams(t *testing.T) {
	a := &fakeAudit{result: audit.PageResult{Records: []store.AuditRecord{}, Page: 1, PageSize: 20}}
	e := newTestEngine(Deps{Audit: a})

	w := doJSON(e, http.MethodGet, "/api/admin/audit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if a.gotPage != 0 || a.gotSize != 0 {
		t.Errorf("缺省参数期望以 0 透传，实际 page=%d size=%d", a.gotPage, a.gotSize)
	}
}

// TestQueryAuditInvalidPageMapsTo400 验证非整数分页参数返回字段级 400。
func TestQueryAuditInvalidPageMapsTo400(t *testing.T) {
	a := &fakeAudit{}
	e := newTestEngine(Deps{Audit: a})

	w := doJSON(e, http.MethodGet, "/api/admin/audit?page=abc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法分页参数期望 HTTP 400，实际 %d", w.Code)
	}
}

// TestAuditServiceUnavailable 验证依赖未接线时返回 503。
func TestAuditServiceUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/audit", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖未接线期望 HTTP 503，实际 %d", w.Code)
	}
}
