package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

type fakeRuntimeEnv struct {
	summary rtenv.Summary
}

func (f *fakeRuntimeEnv) Summary() rtenv.Summary { return f.summary }

func TestRuntimeSummaryOK(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{summary: rtenv.Summary{
		StdioEnabled:     true,
		CommandAllowlist: []string{"node"},
		Tools:            []rtenv.ToolStatus{{Name: "node", Available: true, Path: "/usr/bin/node"}},
		AvailableCount:   1,
		RiskNotes:        []string{"note"},
	}}})
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 20000 {
		t.Fatalf("code=%d", envelope.Code)
	}
}

func TestRuntimeSummaryUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/summary", "")
	if w.Code == http.StatusOK {
		t.Fatalf("expected non-OK, body=%s", w.Body.String())
	}
}
