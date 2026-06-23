package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/backup"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type fakeBackupService struct {
	exportData []byte
	exportErr  error
	imported   []byte
	importErr  error
}

func (f *fakeBackupService) Export(_ context.Context) ([]byte, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return f.exportData, nil
}

func (f *fakeBackupService) Import(_ context.Context, data []byte) error {
	if f.importErr != nil {
		return f.importErr
	}
	f.imported = append([]byte(nil), data...)
	return nil
}

func TestExportBackupReturnsDownload(t *testing.T) {
	svc := &fakeBackupService{exportData: []byte(`{"version":"mpg-backup/v1"}`)}
	e := newTestEngine(Deps{Backup: svc})

	w := doJSON(e, http.MethodGet, "/api/admin/backup/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want HTTP 200, got %d, body %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != string(svc.exportData) {
		t.Fatalf("want raw backup body %q, got %q", svc.exportData, got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("want JSON content type, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "mpg-backup-") || !strings.Contains(cd, ".json") {
		t.Fatalf("want attachment backup filename, got %q", cd)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("want nosniff header, got %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("want no-store cache header, got %q", got)
	}
}

func TestPreviewBackupReturnsCountsAndSecretFlag(t *testing.T) {
	content := marshalHTTPBackup(t, sampleHTTPBackup())
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodPost, "/api/admin/backup/preview", `{"content":`+quoteJSON(t, content)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want HTTP 200, got %d, body %s", w.Code, w.Body.String())
	}

	var got backupPreviewResponse
	unmarshalData(t, w, &got)
	if got.Version != backup.FormatVersion {
		t.Fatalf("want version %q, got %q", backup.FormatVersion, got.Version)
	}
	if got.UpstreamCount != 1 || got.AliasRuleCount != 1 || got.MCPFilterRuleCount != 1 ||
		got.APIKeyCount != 1 || got.APIKeyFilterRuleCount != 1 || got.ACLCount != 2 {
		t.Fatalf("unexpected preview counts: %+v", got)
	}
	if !got.ContainsSecrets {
		t.Fatalf("want preview to mark backup as containing secrets")
	}
}

func TestImportBackupCallsServiceAndRequestsRestart(t *testing.T) {
	content := marshalHTTPBackup(t, sampleHTTPBackup())
	svc := &fakeBackupService{}
	runtime := &fakeSettingsRuntime{}
	e := newTestEngine(Deps{Backup: svc, SettingsRuntime: runtime})

	w := doJSON(e, http.MethodPost, "/api/admin/backup/import", `{"content":`+quoteJSON(t, content)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want HTTP 200, got %d, body %s", w.Code, w.Body.String())
	}
	if string(svc.imported) != content {
		t.Fatalf("backup service received unexpected content")
	}
	if !runtime.restartRequested {
		t.Fatalf("want runtime restart requested after import")
	}

	var got backupImportResponse
	unmarshalData(t, w, &got)
	if !got.Imported || !got.RestartRequested || got.Preview.UpstreamCount != 1 {
		t.Fatalf("unexpected import response: %+v", got)
	}
}

func TestImportBackupRejectsInvalidFileBeforeServiceCall(t *testing.T) {
	svc := &fakeBackupService{}
	e := newTestEngine(Deps{Backup: svc})

	w := doJSON(e, http.MethodPost, "/api/admin/backup/import", `{"content":"not-json"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want HTTP 400, got %d, body %s", w.Code, w.Body.String())
	}
	if len(svc.imported) != 0 {
		t.Fatalf("invalid backup should not be passed to service")
	}
	code, _, _ := parseErrorEnvelope(t, w)
	if code != 42200 {
		t.Fatalf("want backup invalid business code 42200, got %d", code)
	}
}

func TestBackupServiceUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/backup/export", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want HTTP 503, got %d", w.Code)
	}
}

func sampleHTTPBackup() backup.Backup {
	return backup.Backup{
		Version: backup.FormatVersion,
		YAML: func() config.YAMLConfig {
			cfg := config.DefaultYAMLConfig()
			cfg.JWTSecret = "jwt-secret"
			cfg.Admin = config.AdminConfig{Username: "admin", PasswordHash: "hash", Initialized: true}
			return cfg
		}(),
		Business: backup.BusinessConfig{
			Upstreams: []backup.UpstreamEntry{
				{
					ID: "11111111-1111-4111-8111-111111111111",
					Config: domain.UpstreamConfig{
						Name:       "demo",
						Transport:  domain.TransportSSE,
						ConnParams: map[string]any{"url": "https://example.com/sse"},
						Credential: "secret-token",
						Enabled:    true,
					},
				},
			},
			AliasRules: []domain.AliasRule{{
				ID:          "alias-1",
				ScopeType:   "upstreams",
				UpstreamIDs: []string{"11111111-1111-4111-8111-111111111111"},
				Pattern:     "old",
				TargetName:  "new",
			}},
			MCPFilterRules: []domain.FilterRule{{
				ID:          "mcp-filter-1",
				ScopeType:   "upstreams",
				UpstreamIDs: []string{"11111111-1111-4111-8111-111111111111"},
				Pattern:     "blocked",
				Enabled:     true,
			}},
			APIKeys: []backup.APIKeyEntry{{
				Meta: store.APIKey{
					ID:        "22222222-2222-4222-8222-222222222222",
					Name:      "demo-key",
					KeyHash:   []byte{1, 2, 3},
					KeyPlain:  "mpg_secret",
					KeyPrefix: "mpg_",
					Enabled:   true,
				},
				FilterRules: []domain.FilterRule{{ID: "api-filter-1", Pattern: "tool", Enabled: true}},
				ACLCIDRs:    []string{"10.0.0.0/8", "192.168.1.1"},
			}},
		},
	}
}

func marshalHTTPBackup(t *testing.T, b backup.Backup) string {
	t.Helper()
	data, err := backup.Marshal(b)
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	return string(data)
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("quote JSON: %v", err)
	}
	return string(data)
}
