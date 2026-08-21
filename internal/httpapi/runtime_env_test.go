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
	summary         rtenv.Summary
	browseStat      rtenv.BrowseStatResult
	policy          rtenv.Policy
	listDepsErr     error
	installDepErr   error
	uninstallDepErr error
}

func (f *fakeRuntimeEnv) Policy() rtenv.Policy {
	if f.policy.CommandAllowlist != nil || f.policy.GlobalFileRoots != nil {
		return f.policy
	}
	return rtenv.DefaultPolicy()
}
func (f *fakeRuntimeEnv) Summary() rtenv.Summary              { return f.summary }
func (f *fakeRuntimeEnv) KnownToolCatalog() []rtenv.KnownTool { return rtenv.KnownTools() }
func (f *fakeRuntimeEnv) Preflight(req rtenv.PreflightRequest) rtenv.PreflightResult {
	return rtenv.EvaluatePreflight(req, rtenv.DefaultPolicy(), "/data/runtime", func(string) (string, error) {
		return "/usr/bin/x", nil
	})
}
func (f *fakeRuntimeEnv) ListDeps(_ context.Context, kind rtenv.DepKind) (rtenv.ListDepsResult, error) {
	if f.listDepsErr != nil {
		return rtenv.ListDepsResult{Kind: kind, Items: []rtenv.Dependency{}}, f.listDepsErr
	}
	return rtenv.ListDepsResult{Kind: kind, Items: []rtenv.Dependency{}}, nil
}
func (f *fakeRuntimeEnv) InstallDep(_ context.Context, kind rtenv.DepKind, spec string) (rtenv.InstallDepResult, error) {
	if f.installDepErr != nil {
		return rtenv.InstallDepResult{Kind: kind}, f.installDepErr
	}
	return rtenv.InstallDepResult{Kind: kind, Name: spec}, nil
}
func (f *fakeRuntimeEnv) UninstallDep(_ context.Context, kind rtenv.DepKind, name string) (rtenv.InstallDepResult, error) {
	if f.uninstallDepErr != nil {
		return rtenv.InstallDepResult{Kind: kind}, f.uninstallDepErr
	}
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

func TestRuntimeToolsAndPreflight(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})
	w := doJSON(e, http.MethodGet, "/api/admin/runtime/tools", "")
	if w.Code != http.StatusOK {
		t.Fatalf("tools status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSON(e, http.MethodPost, "/api/admin/runtime/preflight", `{"transport":"stdio","command":"npx"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", w.Code, w.Body.String())
	}
}

// 受管安装路由已删除，不能再以 404 之外的形式存在（避免前端残留调用被静默接受）。
func TestRuntimeManagedInstallRoutesAreGone(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/admin/runtime/catalog"},
		{method: http.MethodPost, path: "/api/admin/runtime/install/preview", body: `{"packageId":"node"}`},
		{method: http.MethodPost, path: "/api/admin/runtime/install", body: `{"packageId":"node"}`},
		{method: http.MethodPost, path: "/api/admin/runtime/uninstall", body: `{"packageId":"node"}`},
	} {
		w := doJSON(e, request.method, request.path, request.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeDepsUnsupportedManagementReturnsValidationError(t *testing.T) {
	unsupported := fmt.Errorf("%w：test unsupported", rtenv.ErrLocalRuntimeUnsupported)
	env := &fakeRuntimeEnv{
		listDepsErr:     unsupported,
		installDepErr:   unsupported,
		uninstallDepErr: unsupported,
	}
	e := newTestEngine(Deps{RuntimeEnv: env})

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/admin/runtime/deps?kind=npm"},
		{method: http.MethodPost, path: "/api/admin/runtime/deps/install", body: `{"kind":"npm","spec":"lodash"}`},
		{method: http.MethodPost, path: "/api/admin/runtime/deps/uninstall", body: `{"kind":"npm","name":"lodash"}`},
	} {
		w := doJSON(e, request.method, request.path, request.body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), rtenv.ErrLocalRuntimeUnsupported.Error()) {
			t.Fatalf("unsupported reason missing: %s", w.Body.String())
		}
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

// 缺依赖是 stdio 上游最常见的失败原因；装好之后必须立即唤醒失败中的上游重试，
// 否则降频状态下要等到最大退避间隔，用户会以为没生效。
func TestRuntimeDepChangeWakesUnavailableUpstreams(t *testing.T) {
	for _, request := range []struct {
		name string
		path string
		body string
	}{
		{name: "install", path: "/api/admin/runtime/deps/install", body: `{"kind":"npm","spec":"lodash"}`},
		{name: "uninstall", path: "/api/admin/runtime/deps/uninstall", body: `{"kind":"npm","name":"lodash"}`},
	} {
		t.Run(request.name, func(t *testing.T) {
			ups := &fakeUpstreamService{}
			e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}, Upstream: ups})
			w := doJSON(e, http.MethodPost, request.path, request.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if ups.retryUnavailableCalls != 1 {
				t.Fatalf("应唤醒一次失败中的上游，got %d", ups.retryUnavailableCalls)
			}
		})
	}
}

// 依赖操作失败时不应唤醒：什么都没修好，唤醒只会制造无意义的重连。
func TestRuntimeDepFailureDoesNotWakeUpstreams(t *testing.T) {
	ups := &fakeUpstreamService{}
	env := &fakeRuntimeEnv{installDepErr: fmt.Errorf("uv pip install 失败")}
	e := newTestEngine(Deps{RuntimeEnv: env, Upstream: ups})
	w := doJSON(e, http.MethodPost, "/api/admin/runtime/deps/install", `{"kind":"npm","spec":"lodash"}`)
	if w.Code == http.StatusOK {
		t.Fatalf("安装失败应返回错误：%s", w.Body.String())
	}
	if ups.retryUnavailableCalls != 0 {
		t.Fatalf("失败不应唤醒上游，got %d", ups.retryUnavailableCalls)
	}
}
