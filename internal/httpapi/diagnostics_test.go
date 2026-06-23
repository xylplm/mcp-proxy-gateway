package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

func TestDiagnosticsExportRedactsSecretsAndKeepsUsefulSummary(t *testing.T) {
	settings := &fakeSettings{cfg: config.YAMLConfig{
		Server:    config.ServerConfig{AdminAddr: ":8080", LogLevel: "debug"},
		Admin:     config.AdminConfig{Username: "admin", PasswordHash: "bcrypt-secret", Initialized: true},
		JWTSecret: "jwt-secret",
		Sync:      config.SyncConfig{Cron: "0 */30 * * * *", TimeoutS: 30},
	}}
	upstreams := &fakeUpstreamService{list: []domain.Upstream{{
		ID: "up-1",
		Config: domain.UpstreamConfig{
			Name:       "remote",
			Transport:  domain.TransportStreamableHTTP,
			ConnParams: map[string]any{"url": "https://example.com/mcp", "headers": map[string]any{"Authorization": "Bearer secret-token"}},
			Credential: "plain-credential",
			Enabled:    true,
			AutoSync:   true,
		},
		State:     domain.ConnAvailable,
		CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC),
	}}}
	logs := syslog.NewStore(10)
	logs.Add("warn", "upstream failed", time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC), "manager.go:1", map[string]any{
		"token": "log-secret",
		"code":  "timeout",
	})
	stats := &fakeStats{callRecords: []store.CallRecordView{{
		ID:             9,
		UpstreamID:     "up-1",
		UpstreamName:   "remote",
		OriginalName:   "search",
		ExposedName:    "search",
		CalledAt:       time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC),
		LatencyMS:      123,
		Success:        false,
		Status:         store.CallStatusFailed,
		RequestArgs:    json.RawMessage(`{"q":"private"}`),
		ResponseResult: json.RawMessage(`{"secret":"result"}`),
		ErrorMessage:   "timeout",
	}}}

	e := newTestEngine(Deps{
		Settings:   settings,
		Upstream:   upstreams,
		ToolCache:  &fakeToolCacheStore{tools: []domain.ToolDef{{Name: "search"}}, found: true, updatedAt: time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC)},
		SystemLogs: logs,
		Stats:      stats,
	})

	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	assertDownloadHeaders(t, w, "mpg-diagnostics-")

	var got diagnosticsBundle
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("诊断包应为 JSON 对象：%v", err)
	}
	if got.FormatVersion != diagnosticsFormatVersion || got.Runtime.GoVersion == "" {
		t.Fatalf("诊断包基础信息不完整：%+v", got)
	}
	if got.Settings.Admin.PasswordHashSet != true || got.Settings.Admin.Username != "admin" {
		t.Fatalf("管理员摘要不符合预期：%+v", got.Settings.Admin)
	}
	if got.Settings.JWTSecret != redactedValue {
		t.Fatalf("JWT secret 应脱敏，实际 %q", got.Settings.JWTSecret)
	}
	if len(got.Upstreams) != 1 || got.Upstreams[0].Config.Credential != redactedValue {
		t.Fatalf("上游凭证应脱敏：%+v", got.Upstreams)
	}
	headers := got.Upstreams[0].Config.ConnParams["headers"].(map[string]any)
	if headers["Authorization"] != redactedValue {
		t.Fatalf("上游 header 应脱敏：%+v", headers)
	}
	if len(got.RecentCallRecords) != 1 || got.RecentCallRecords[0].ErrorMessage != "timeout" {
		t.Fatalf("调用摘要不符合预期：%+v", got.RecentCallRecords)
	}

	body := w.Body.String()
	for _, secret := range []string{"jwt-secret", "bcrypt-secret", "plain-credential", "secret-token", "log-secret", `"RequestArgs"`, `"ResponseResult"`} {
		if strings.Contains(body, secret) {
			t.Fatalf("诊断包不应包含敏感或明细内容 %q：%s", secret, body)
		}
	}
	if !strings.Contains(body, "https://example.com/mcp") || !strings.Contains(body, "upstream failed") {
		t.Fatalf("诊断包应保留排障摘要信息：%s", body)
	}
}
