package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type fakeSecurityService struct {
	events     []store.SecurityEvent
	blocks     []store.SecurityBlock
	eventQuery store.SecurityEventQuery
	blockQuery store.SecurityBlockQuery
}

func (f *fakeSecurityService) Summary(context.Context) (store.SecuritySummary, error) {
	return store.SecuritySummary{}, nil
}

func (f *fakeSecurityService) ListEvents(_ context.Context, query store.SecurityEventQuery) ([]store.SecurityEvent, error) {
	f.eventQuery = query
	return f.events, nil
}

func (f *fakeSecurityService) ListBlocks(_ context.Context, query store.SecurityBlockQuery) ([]store.SecurityBlock, error) {
	f.blockQuery = query
	return f.blocks, nil
}

func (f *fakeSecurityService) ReleaseBlock(context.Context, string) (store.SecurityBlock, error) {
	return store.SecurityBlock{}, nil
}

func TestSecurityEventsExportReturnsDownload(t *testing.T) {
	svc := &fakeSecurityService{events: []store.SecurityEvent{{
		ID:          7,
		EventType:   "auth_failed",
		SubjectType: "ip",
		Subject:     "203.0.113.10",
		Count:       3,
		CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}}}
	e := newTestEngine(Deps{Security: svc})

	w := doJSON(e, http.MethodGet, "/api/admin/security/events/export?eventType=auth_failed&limit=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got []store.SecurityEvent
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("导出响应应为 JSON 数组：%v", err)
	}
	if len(got) != 1 || got[0].ID != 7 || got[0].EventType != "auth_failed" {
		t.Fatalf("导出事件不符合预期：%+v", got)
	}
	if svc.eventQuery.EventType != "auth_failed" || svc.eventQuery.Limit != 10 {
		t.Fatalf("应透传事件筛选参数，实际 %+v", svc.eventQuery)
	}
	assertDownloadHeaders(t, w, "mpg-security-events-")
}

func TestSecurityBlocksExportReturnsDownload(t *testing.T) {
	until := time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC)
	svc := &fakeSecurityService{blocks: []store.SecurityBlock{{
		ID:           "block-1",
		SubjectType:  "ip",
		Subject:      "203.0.113.20",
		Status:       store.SecurityBlockStatusActive,
		BlockedUntil: &until,
		CreatedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}}}
	e := newTestEngine(Deps{Security: svc})

	w := doJSON(e, http.MethodGet, "/api/admin/security/blocks/export?status=active&limit=5", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got []store.SecurityBlock
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("导出响应应为 JSON 数组：%v", err)
	}
	if len(got) != 1 || got[0].ID != "block-1" || got[0].Status != store.SecurityBlockStatusActive {
		t.Fatalf("导出封禁不符合预期：%+v", got)
	}
	if svc.blockQuery.Status != "active" || svc.blockQuery.Limit != 5 {
		t.Fatalf("应透传封禁筛选参数，实际 %+v", svc.blockQuery)
	}
	assertDownloadHeaders(t, w, "mpg-security-blocks-")
}

func TestSecurityExportInvalidLimit(t *testing.T) {
	e := newTestEngine(Deps{Security: &fakeSecurityService{}})
	for _, path := range []string{
		"/api/admin/security/events/export?limit=bad",
		"/api/admin/security/blocks/export?limit=bad",
	} {
		w := doJSON(e, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 期望 HTTP 400，实际 %d", path, w.Code)
		}
	}
}

func assertDownloadHeaders(t *testing.T, w http.ResponseWriter, filenamePrefix string) {
	t.Helper()
	header := w.Header()
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("期望 JSON Content-Type，实际 %q", ct)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, filenamePrefix) || !strings.Contains(cd, ".json") {
		t.Fatalf("期望下载文件名包含 %q，实际 %q", filenamePrefix, cd)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("期望 nosniff header，实际 %q", got)
	}
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("期望 no-store cache header，实际 %q", got)
	}
}
