package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	rtenv "github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

type fakeRuntimeEnv struct {
	summary    rtenv.Summary
	catalog    []rtenv.CatalogPackage
	browseStat rtenv.BrowseStatResult
	policy     rtenv.Policy
}

func (f *fakeRuntimeEnv) Policy() rtenv.Policy {
	if f.policy.CommandAllowlist != nil || f.policy.GlobalFileRoots != nil {
		return f.policy
	}
	return rtenv.DefaultPolicy()
}
func (f *fakeRuntimeEnv) Summary() rtenv.Summary { return f.summary }
func (f *fakeRuntimeEnv) Catalog() []rtenv.CatalogPackage {
	if f.catalog != nil {
		return f.catalog
	}
	return nil
}
func (f *fakeRuntimeEnv) KnownToolCatalog() []rtenv.KnownTool { return rtenv.KnownTools() }
func (f *fakeRuntimeEnv) Preflight(req rtenv.PreflightRequest) rtenv.PreflightResult {
	return rtenv.EvaluatePreflight(req, rtenv.DefaultPolicy(), "/data/runtime", func(string) (string, error) {
		return "/usr/bin/x", nil
	})
}
func (f *fakeRuntimeEnv) PreviewInstall(packageID string) (rtenv.CatalogPackage, error) {
	if packageID == "bad" {
		return rtenv.CatalogPackage{}, fmt.Errorf("未知预置包")
	}
	return rtenv.CatalogPackage{ID: packageID, Name: "t", Supported: true}, nil
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
func (f *fakeRuntimeEnv) ListDeps(_ context.Context, kind rtenv.DepKind) (rtenv.ListDepsResult, error) {
	return rtenv.ListDepsResult{Kind: kind, Items: []rtenv.Dependency{}}, nil
}
func (f *fakeRuntimeEnv) InstallDep(_ context.Context, kind rtenv.DepKind, spec string) (rtenv.InstallDepResult, error) {
	return rtenv.InstallDepResult{Kind: kind, Name: spec}, nil
}
func (f *fakeRuntimeEnv) UninstallDep(_ context.Context, kind rtenv.DepKind, name string) (rtenv.InstallDepResult, error) {
	return rtenv.InstallDepResult{Kind: kind, Name: name}, nil
}
func (f *fakeRuntimeEnv) BrowseRoots(contextRoots []string) rtenv.BrowseRootsResult {
	return rtenv.BrowseRootsResult{
		Roots: []rtenv.BrowseRoot{{
			ID: "data", Label: "数据目录", Path: "/data", Kind: "data",
		}},
		Platform:      "linux",
		PathSeparator: "/",
	}
}
func (f *fakeRuntimeEnv) BrowseList(path, mode string, limit int, contextRoots []string) (rtenv.BrowseListResult, error) {
	if path == "/denied" {
		return rtenv.BrowseListResult{}, fmt.Errorf("路径不在允许浏览范围内")
	}
	if path == "/missing" {
		return rtenv.BrowseListResult{}, fmt.Errorf("路径不存在")
	}
	return rtenv.BrowseListResult{
		Path: path, Entries: []rtenv.BrowseEntry{{Name: "a", Path: path + "/a", Type: "dir", Readable: true, Enterable: true}},
		Platform: "linux", PathSeparator: "/",
	}, nil
}
func (f *fakeRuntimeEnv) BrowseStat(path string, contextRoots []string) (rtenv.BrowseStatResult, error) {
	if f.browseStat.Path != "" {
		return f.browseStat, nil
	}
	return rtenv.BrowseStatResult{Path: path, Exists: true, Type: "dir", Allowed: true, Readable: true}, nil
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

func TestRuntimeDirectoryInspect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &fakeRuntimeEnv{policy: rtenv.Policy{
		StdioEnabled:     true,
		CommandAllowlist: rtenv.DefaultCommandAllowlist(),
		GlobalFileRoots:  []string{dir},
	}}
	env.browseStat = rtenv.BrowseStatResult{Path: dir, Exists: true, Type: "dir", Allowed: true, Readable: true}
	e := newTestEngine(Deps{RuntimeEnv: env})
	w := doJSON(e, http.MethodPost, "/api/admin/runtime/directory/inspect", `{"path":`+strconv.Quote(dir)+`,"fileAccessRoots":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRuntimeDirectoryInspectRejectsBrowseOnlyRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &fakeRuntimeEnv{}
	env.browseStat = rtenv.BrowseStatResult{Path: dir, Exists: true, Type: "dir", Allowed: true, Readable: true}
	e := newTestEngine(Deps{RuntimeEnv: env})
	w := doJSON(e, http.MethodPost, "/api/admin/runtime/directory/inspect", `{"path":`+strconv.Quote(dir)+`}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("browse-only directory should be rejected early, status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "可浏览但不可启动") {
		t.Fatalf("error should explain the browse/launch distinction: %s", w.Body.String())
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

func TestRuntimeCatalogAndToolsAndPreflight(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{catalog: []rtenv.CatalogPackage{
		{ID: "node-22.14.0", Name: "Node.js", Supported: true},
	}}})
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/catalog", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(e, http.MethodGet, "/api/admin/runtime/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("tools status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/preflight", `{"transport":"stdio","command":"npx"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRuntimeDepsValidation(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})

	// list：非法 kind 拒绝
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/deps?kind=docker", "")
	if w.Code == http.StatusOK {
		t.Fatal("非法 kind 应被拒绝")
	}
	// list：合法 kind 通过
	w = doJSON(e, http.MethodGet, "/api/admin/runtime/deps?kind=npm", "")
	if w.Code != http.StatusOK {
		t.Fatalf("npm list status=%d body=%s", w.Code, w.Body.String())
	}

	// install：空 spec 拒绝
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/deps/install", `{"kind":"npm","spec":""}`)
	if w.Code == http.StatusOK {
		t.Fatal("空 spec 应被拒绝")
	}
	// install：非法 kind 拒绝
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/deps/install", `{"kind":"nope","spec":"lodash"}`)
	if w.Code == http.StatusOK {
		t.Fatal("非法 kind 应被拒绝")
	}
	// install：合法通过（fake 返回成功）
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/deps/install", `{"kind":"npm","spec":"lodash@4.17.21"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install status=%d body=%s", w.Code, w.Body.String())
	}

	// uninstall：空 name 拒绝
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/deps/uninstall", `{"kind":"pip","name":""}`)
	if w.Code == http.StatusOK {
		t.Fatal("空 name 应被拒绝")
	}
	// uninstall：合法通过
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/deps/uninstall", `{"kind":"pip","name":"requests"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("uninstall status=%d body=%s", w.Code, w.Body.String())
	}
}
