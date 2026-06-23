package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

func TestSystemLogsQueryFiltersByLevelAndCursor(t *testing.T) {
	store := syslog.NewStore(10)
	store.Add("info", "started", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "", nil)
	store.Add("error", "failed", time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC), "", map[string]any{"code": "boom"})
	store.Add("info", "ready", time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC), "", nil)
	e := newTestEngine(Deps{SystemLogs: store})

	w := doJSON(e, http.MethodGet, "/api/admin/system-logs?afterId=1&level=info&limit=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got struct {
		Logs []syslog.Entry `json:"logs"`
	}
	unmarshalData(t, w, &got)
	if len(got.Logs) != 1 || got.Logs[0].Message != "ready" {
		t.Fatalf("系统日志过滤结果不符合预期：%+v", got.Logs)
	}
}

func TestSystemLogsCanBeCleared(t *testing.T) {
	store := syslog.NewStore(10)
	store.Add("warn", "one", time.Now(), "", nil)
	rec := &fakeAuditRecorder{}
	e := newTestEngine(Deps{SystemLogs: store, AuditRecorder: rec})

	w := doJSON(e, http.MethodDelete, "/api/admin/system-logs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got struct {
		Deleted int `json:"deleted"`
	}
	unmarshalData(t, w, &got)
	if got.Deleted != 1 || len(store.List(0, "", 10)) != 0 {
		t.Fatalf("系统日志未清空：deleted=%d left=%d", got.Deleted, len(store.List(0, "", 10)))
	}
	if len(rec.events) != 1 || rec.events[0].method != "update" || rec.events[0].target != "system-logs:clear" {
		t.Fatalf("应记录清空系统日志审计，实际 %+v", rec.events)
	}
}

func TestSystemLogsExportReturnsDownload(t *testing.T) {
	store := syslog.NewStore(10)
	store.Add("info", "started", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "", nil)
	store.Add("warn", "slow", time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC), "sync.go:1", map[string]any{"ms": 1200})
	e := newTestEngine(Deps{SystemLogs: store})

	w := doJSON(e, http.MethodGet, "/api/admin/system-logs/export?level=warn", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got []syslog.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("导出响应应为 JSON 数组：%v", err)
	}
	if len(got) != 1 || got[0].Level != "warn" || got[0].Message != "slow" {
		t.Fatalf("导出结果不符合预期：%+v", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("期望 JSON Content-Type，实际 %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "mpg-system-logs-warn-") || !strings.Contains(cd, ".json") {
		t.Fatalf("期望系统日志下载文件名，实际 %q", cd)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("期望 nosniff header，实际 %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("期望 no-store cache header，实际 %q", got)
	}
}

func TestSystemLogsInvalidParams(t *testing.T) {
	e := newTestEngine(Deps{SystemLogs: syslog.NewStore(10)})
	for _, path := range []string{
		"/api/admin/system-logs?level=trace",
		"/api/admin/system-logs/export?level=trace",
		"/api/admin/system-logs?afterId=-1",
		"/api/admin/system-logs?limit=bad",
	} {
		w := doJSON(e, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 期望 HTTP 400，实际 %d", path, w.Code)
		}
	}
}
