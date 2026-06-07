package static

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// buildTestServer 以内存文件系统构造一个 Static_Server，便于在不依赖真实构建产物的前提下
// 验证文件服务与 SPA fallback 行为（Req 17.1、17.2）。
func buildTestServer(t *testing.T) *Server {
	t.Helper()
	root := fstest.MapFS{
		"dist/index.html":        {Data: []byte("<!doctype html><title>spa-root</title>")},
		"dist/favicon.ico":       {Data: []byte("icon-bytes")},
		"dist/assets/app.js":     {Data: []byte("console.log('app')")},
		"dist/assets/styles.css": {Data: []byte("body{}")},
	}
	s, err := newFromFS(root)
	if err != nil {
		t.Fatalf("newFromFS 失败: %v", err)
	}
	return s
}

func doGet(t *testing.T, s *Server, target string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Result()
}

// 验证：存在的内嵌资源被原样提供，且带有正确的 Content-Type（Req 17.1）。
func TestServeExistingAsset(t *testing.T) {
	s := buildTestServer(t)

	resp := doGet(t, s, "/assets/app.js")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("期望 200，得到 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "console.log") {
		t.Fatalf("期望返回 app.js 内容，得到 %q", string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("期望 JS Content-Type，得到 %q", ct)
	}
}

// 验证：根路径 / 返回 index.html（Req 17.1）。
func TestServeRootReturnsIndex(t *testing.T) {
	s := buildTestServer(t)

	resp := doGet(t, s, "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("期望 200，得到 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "spa-root") {
		t.Fatalf("期望返回 index.html 内容，得到 %q", string(body))
	}
}

// 验证：未知客户端路由路径（非 API、非文件）兜底返回 index.html 且为 200（Req 17.2）。
func TestUnknownClientRouteFallsBackToIndex(t *testing.T) {
	s := buildTestServer(t)

	for _, target := range []string{"/dashboard", "/upstreams/123/edit", "/settings"} {
		resp := doGet(t, s, target)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("路径 %s 期望 200，得到 %d", target, resp.StatusCode)
		}
		if !strings.Contains(string(body), "spa-root") {
			t.Fatalf("路径 %s 期望兜底 index.html，得到 %q", target, string(body))
		}
	}
}

// 验证：API 路由不被静态服务兜底为 index.html，而返回 404（Req 17.2）。
func TestAPIRoutesNotShadowed(t *testing.T) {
	s := buildTestServer(t)

	for _, target := range []string{"/api/admin/upstreams", "/mcp/sse", "/healthz"} {
		resp := doGet(t, s, target)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("API 路径 %s 期望 404，得到 %d", target, resp.StatusCode)
		}
		if strings.Contains(string(body), "spa-root") {
			t.Fatalf("API 路径 %s 不应兜底返回 index.html", target)
		}
	}
}

// 验证 isAPIRoute 的文件/兜底判定逻辑：API 前缀判 true，静态/客户端路由判 false（Req 17.2）。
func TestIsAPIRoute(t *testing.T) {
	cases := map[string]bool{
		"/api/admin/x":  true,
		"/api/":         true,
		"/mcp/ws":       true,
		"/healthz":      true,
		"/":             false,
		"/assets/a.js":  false,
		"/dashboard":    false,
		"/healthz-page": false, // 客户端路由恰以 healthz 开头但非 /healthz 精确匹配
	}
	for p, want := range cases {
		if got := isAPIRoute(p); got != want {
			t.Errorf("isAPIRoute(%q)=%v，期望 %v", p, got, want)
		}
	}
}

// 验证：真实内嵌产物可成功构造 Static_Server 且根路径可服务（Req 17.1）。
func TestNewFromEmbeddedDist(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New() 失败（dist/ 是否已构建/同步？）: %v", err)
	}
	resp := doGet(t, s, "/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("内嵌根路径期望 200，得到 %d", resp.StatusCode)
	}
}
