package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type fakeToolCacheStore struct {
	tools     []domain.ToolDef
	updatedAt time.Time
	found     bool
	getCalls  int
}

func (s *fakeToolCacheStore) Get(_ context.Context, _ string) ([]domain.ToolDef, time.Time, bool) {
	s.getCalls++
	return s.tools, s.updatedAt, s.found
}

type fakeToolCacheEnsurer struct {
	calls    int
	lastID   string
	err      error
	onEnsure func()
}

func (e *fakeToolCacheEnsurer) EnsureCached(_ context.Context, upstreamID string) (bool, error) {
	e.calls++
	e.lastID = upstreamID
	if e.err != nil {
		return false, e.err
	}
	if e.onEnsure != nil {
		e.onEnsure()
	}
	return true, nil
}

type fakeUpstreamTester struct {
	calls  int
	config domain.UpstreamConfig
	result domain.UpstreamTestResult
	err    error
}

func (t *fakeUpstreamTester) Test(_ context.Context, cfg domain.UpstreamConfig) (domain.UpstreamTestResult, error) {
	t.calls++
	t.config = cfg
	if t.err != nil {
		return domain.UpstreamTestResult{}, t.err
	}
	return t.result, nil
}

func TestUpstreamTestReturnsToolPreview(t *testing.T) {
	tester := &fakeUpstreamTester{
		result: domain.UpstreamTestResult{
			OK:         true,
			Stage:      "ok",
			DurationMS: 12,
			Count:      2,
			Tools: []domain.ToolDef{{
				OriginalName: "search",
				Name:         "search",
				Description:  "搜索",
			}},
		},
	}
	e := newTestEngine(Deps{UpstreamTester: tester})

	w := doJSON(e, http.MethodPost, "/api/admin/upstreams/test", `{
		"name":"临时测试",
		"transport":"streamable-http",
		"connParams":{"url":"https://example.com/mcp"},
		"credential":"token",
		"enabled":true,
		"sortOrder":0,
		"autoSync":true
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if tester.calls != 1 {
		t.Fatalf("期望测试服务被调用 1 次，实际 %d", tester.calls)
	}
	if tester.config.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("传输类型未正确传入测试服务：%q", tester.config.Transport)
	}
	if tester.config.ConnParams["url"] != "https://example.com/mcp" {
		t.Fatalf("连接参数未正确传入测试服务：%+v", tester.config.ConnParams)
	}

	var got domain.UpstreamTestResult
	unmarshalData(t, w, &got)
	if !got.OK || got.Stage != "ok" || got.Count != 2 || len(got.Tools) != 1 {
		t.Fatalf("测试结果响应不符合预期：%+v", got)
	}
	if got.Tools[0].Name != "search" {
		t.Fatalf("工具预览未正确返回：%+v", got.Tools)
	}
}

func TestUpstreamTestReturnsValidationErrors(t *testing.T) {
	tester := &fakeUpstreamTester{
		err: domain.NewValidationError("上游 MCP 连接参数校验失败", map[string]string{
			"connParams.url": "缺少必填连接参数 \"url\"",
		}),
	}
	e := newTestEngine(Deps{UpstreamTester: tester})

	w := doJSON(e, http.MethodPost, "/api/admin/upstreams/test", `{
		"name":"临时测试",
		"transport":"streamable-http",
		"connParams":{},
		"enabled":true,
		"sortOrder":0,
		"autoSync":true
	}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	_, _, fields := parseErrorEnvelope(t, w)
	if fields["connParams.url"] == "" {
		t.Fatalf("期望返回 connParams.url 字段错误，实际 %+v", fields)
	}
}

func TestListUpstreamToolsCacheMissEnsuresAndReturnsTools(t *testing.T) {
	updatedAt := time.Date(2026, 6, 10, 10, 20, 0, 0, time.UTC)
	cache := &fakeToolCacheStore{}
	ensurer := &fakeToolCacheEnsurer{
		onEnsure: func() {
			cache.found = true
			cache.updatedAt = updatedAt
			cache.tools = []domain.ToolDef{{
				OriginalName: "media_subscribe",
				Name:         "media_subscribe",
				Description:  "添加媒体订阅",
				UpstreamID:   "up-1",
			}}
		},
	}
	e := newTestEngine(Deps{ToolCache: cache, CacheEnsurer: ensurer})

	w := doJSON(e, http.MethodGet, "/api/admin/upstreams/up-1/tools", "")

	if w.Code != http.StatusOK {
		t.Fatalf("缓存缺失补拉后期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if ensurer.calls != 1 || ensurer.lastID != "up-1" {
		t.Fatalf("期望对 up-1 触发一次缓存补拉，实际 calls=%d lastID=%q", ensurer.calls, ensurer.lastID)
	}
	if cache.getCalls != 2 {
		t.Fatalf("期望补拉前后各读取一次缓存，实际 %d 次", cache.getCalls)
	}

	var got struct {
		ID        string           `json:"id"`
		Count     int              `json:"count"`
		Tools     []domain.ToolDef `json:"tools"`
		UpdatedAt time.Time        `json:"updatedAt"`
	}
	unmarshalData(t, w, &got)
	if got.ID != "up-1" || got.Count != 1 || len(got.Tools) != 1 {
		t.Fatalf("工具列表响应不符合预期：%+v", got)
	}
	if got.Tools[0].Name != "media_subscribe" || got.Tools[0].UpstreamID != "up-1" {
		t.Fatalf("工具定义未正确返回：%+v", got.Tools[0])
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updatedAt 期望 %s，实际 %s", updatedAt, got.UpdatedAt)
	}
}
