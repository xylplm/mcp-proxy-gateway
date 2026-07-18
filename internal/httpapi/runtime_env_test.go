package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

type fakeRuntimeEnv struct {
	summary rtenv.Summary
	catalog []rtenv.CatalogPackage
}

func (f *fakeRuntimeEnv) Summary() rtenv.Summary { return f.summary }
func (f *fakeRuntimeEnv) Catalog() []rtenv.CatalogPackage {
	if f.catalog != nil {
		return f.catalog
	}
	return nil
}
func (f *fakeRuntimeEnv) PreviewInstall(packageID string) (rtenv.CatalogPackage, error) {
	if packageID == "bad" {
		return rtenv.CatalogPackage{}, fmt.Errorf("未知预置包")
	}
	return rtenv.CatalogPackage{PackageSpec: rtenv.PackageSpec{ID: packageID, Name: "t"}, Supported: true}, nil
}
func (f *fakeRuntimeEnv) InstallPackage(_ context.Context, packageID string) (rtenv.InstallResult, error) {
	if packageID == "bad" {
		return rtenv.InstallResult{}, fmt.Errorf("未知预置包")
	}
	return rtenv.InstallResult{ID: packageID, Name: "t", Version: "1"}, nil
}
func (f *fakeRuntimeEnv) UninstallPackage(packageID string) error {
	if packageID == "bad" {
		return fmt.Errorf("未知预置包")
	}
	return nil
}

func TestRuntimeSummaryOK(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{summary: rtenv.Summary{
		StdioEnabled:     true,
		CommandAllowlist: []string{"node"},
		Tools:            []rtenv.ToolStatus{{Name: "node", Available: true, Path: "/usr/bin/node"}},
		AvailableCount:   1,
		RiskNotes:        []string{"note"},
		ProcessHardening: true,
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

func TestRuntimeInstallPreviewAndInstall(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})
	w := doJSON(e, http.MethodPost, "/api/admin/runtime/install/preview", `{"packageId":"node-22.14.0"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/install", `{"packageId":"node-22.14.0"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/install", `{"packageId":"bad"}`)
	if w.Code == http.StatusOK {
		t.Fatal("bad package should fail")
	}
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/uninstall", `{"packageId":"node-22.14.0"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("uninstall status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRuntimeCatalog(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{catalog: []rtenv.CatalogPackage{
		{PackageSpec: rtenv.PackageSpec{ID: "node-22.14.0", Name: "Node.js"}, Supported: true},
	}}})
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/catalog", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
