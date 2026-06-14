package httpapi

import (
	"net/http"
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
	e := newTestEngine(Deps{SystemLogs: store})

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
}

func TestSystemLogsInvalidParams(t *testing.T) {
	e := newTestEngine(Deps{SystemLogs: syslog.NewStore(10)})
	for _, path := range []string{
		"/api/admin/system-logs?level=trace",
		"/api/admin/system-logs?afterId=-1",
		"/api/admin/system-logs?limit=bad",
	} {
		w := doJSON(e, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s 期望 HTTP 400，实际 %d", path, w.Code)
		}
	}
}
