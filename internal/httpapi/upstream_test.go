package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type fakeToolCacheStore struct {
	tools     []domain.ToolDef
	updatedAt time.Time
	found     bool
	change    domain.ToolChangeSummary
	changeOK  bool
	getCalls  int
	byID      map[string]struct {
		tools     []domain.ToolDef
		updatedAt time.Time
		found     bool
	}
}

func (s *fakeToolCacheStore) Get(_ context.Context, id string) ([]domain.ToolDef, time.Time, bool) {
	s.getCalls++
	if s.byID != nil {
		entry, ok := s.byID[id]
		if !ok {
			return nil, time.Time{}, false
		}
		return entry.tools, entry.updatedAt, entry.found
	}
	return s.tools, s.updatedAt, s.found
}

func (s *fakeToolCacheStore) GetChangeSummary(_ context.Context, _ string) (domain.ToolChangeSummary, bool) {
	return s.change, s.changeOK
}

type fakeUpstreamService struct {
	list      []domain.Upstream
	created   []domain.UpstreamConfig
	createErr map[string]error
	err       error
}

func (s *fakeUpstreamService) Create(_ context.Context, cfg domain.UpstreamConfig) (domain.Upstream, error) {
	s.created = append(s.created, cfg)
	if s.createErr != nil {
		if err := s.createErr[cfg.Name]; err != nil {
			return domain.Upstream{}, err
		}
	}
	return domain.Upstream{
		ID:     "up-" + cfg.Name,
		Config: cfg,
		State:  domain.ConnConnecting,
	}, nil
}

func (s *fakeUpstreamService) Update(context.Context, string, domain.UpstreamConfig) (domain.Upstream, error) {
	return domain.Upstream{}, nil
}

func (s *fakeUpstreamService) Delete(context.Context, string) error { return nil }

func (s *fakeUpstreamService) List(context.Context) ([]domain.Upstream, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.list, nil
}

func (s *fakeUpstreamService) SetEnabled(context.Context, string, bool) error { return nil }

func (s *fakeUpstreamService) Reorder(context.Context, []string) error { return nil }

func (s *fakeUpstreamService) Reconnect(context.Context, string) error { return nil }

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

func TestPreviewUpstreamImportParsesMCPServers(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodPost, "/api/admin/upstreams/import/preview", `{
		"content":"{\"mcpServers\":{\"local-files\":{\"command\":\"npx\",\"args\":[\"-y\",\"@modelcontextprotocol/server-filesystem\",\"D:/work\"],\"env\":{\"TOKEN\":\"abc\"}},\"remote\":{\"url\":\"https://example.com/mcp\",\"headers\":{\"Authorization\":\"Bearer token\"}}}}"
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got upstreamImportPreview
	unmarshalData(t, w, &got)
	if got.Count != 2 || len(got.Items) != 2 {
		t.Fatalf("导入预览数量不符合预期：%+v", got)
	}
	if got.Items[0].Config.Name != "local-files" || got.Items[0].Config.Transport != domain.TransportStdio {
		t.Fatalf("stdio 配置解析错误：%+v", got.Items[0].Config)
	}
	if got.Items[0].Config.ConnParams["command"] != "npx" {
		t.Fatalf("stdio command 未正确解析：%+v", got.Items[0].Config.ConnParams)
	}
	if got.Items[1].Config.Name != "remote" || got.Items[1].Config.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("远程配置解析错误：%+v", got.Items[1].Config)
	}
	if got.Items[1].Config.ConnParams["url"] != "https://example.com/mcp" {
		t.Fatalf("远程 url 未正确解析：%+v", got.Items[1].Config.ConnParams)
	}
}

func TestImportUpstreamsCreatesValidAndReportsFailures(t *testing.T) {
	upstream := &fakeUpstreamService{
		createErr: map[string]error{
			"bad": domain.NewValidationError("上游 MCP 配置校验失败", map[string]string{
				"connParams.url": "缺少必填连接参数 \"url\"",
			}),
		},
	}
	e := newTestEngine(Deps{Upstream: upstream})

	w := doJSON(e, http.MethodPost, "/api/admin/upstreams/import", `{
		"items":[
			{"name":"ok","transport":"streamable-http","connParams":{"url":"https://example.com/mcp"},"enabled":true,"autoSync":true},
			{"name":"bad","transport":"streamable-http","connParams":{},"enabled":true,"autoSync":true}
		]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if len(upstream.created) != 2 {
		t.Fatalf("期望逐条尝试创建 2 个上游，实际 %d", len(upstream.created))
	}
	var got upstreamImportResult
	unmarshalData(t, w, &got)
	if len(got.Created) != 1 || got.Created[0].Name != "ok" || got.Created[0].Upstream == nil {
		t.Fatalf("成功项不符合预期：%+v", got.Created)
	}
	if len(got.Failed) != 1 || got.Failed[0].Name != "bad" || got.Failed[0].Fields["connParams.url"] == "" {
		t.Fatalf("失败项不符合预期：%+v", got.Failed)
	}
}

func TestExportUpstreamsMCPJSONReturnsStandardConfig(t *testing.T) {
	upstream := &fakeUpstreamService{list: []domain.Upstream{
		{
			ID: "up-local",
			Config: domain.UpstreamConfig{
				Name:      "local-files",
				Transport: domain.TransportStdio,
				ConnParams: map[string]any{
					"command": "npx",
					"args":    []any{"-y", "@modelcontextprotocol/server-filesystem", "D:/work"},
					"env":     map[string]any{"TOKEN": "abc"},
					"cwd":     "D:/work",
				},
				Credential: "local-secret",
				Enabled:    true,
				AutoSync:   true,
				Tags:       []string{"local"},
			},
		},
		{
			ID: "up-remote",
			Config: domain.UpstreamConfig{
				Name:       "remote",
				Transport:  domain.TransportStreamableHTTP,
				ConnParams: map[string]any{"url": "https://example.com/mcp", "headers": map[string]any{"Authorization": "Bearer token"}},
				Enabled:    false,
				AutoSync:   true,
			},
		},
	}}
	e := newTestEngine(Deps{Upstream: upstream})

	w := doJSON(e, http.MethodGet, "/api/admin/upstreams/export/mcp-json", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	assertDownloadHeaders(t, w, "mpg-mcp-servers-")
	var got mcpExportFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("导出响应应为 MCP JSON 对象：%v", err)
	}
	local := got.MCPServers["local-files"]
	if local.Command != "npx" || len(local.Args) != 3 || local.Env["TOKEN"] != "abc" || local.Credential != "local-secret" || !local.Enabled {
		t.Fatalf("stdio 导出不符合预期：%+v", local)
	}
	remote := got.MCPServers["remote"]
	if remote.Type != "streamable-http" || remote.URL != "https://example.com/mcp" || remote.Headers["Authorization"] != "Bearer token" || remote.Enabled {
		t.Fatalf("远程导出不符合预期：%+v", remote)
	}
}

func TestPreviewUpstreamImportRejectsEmptyContent(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodPost, "/api/admin/upstreams/import/preview", `{"content":"  "}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	_, _, fields := parseErrorEnvelope(t, w)
	if fields["content"] == "" {
		t.Fatalf("期望返回 content 字段错误，实际 %+v", fields)
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

func TestListUpstreamToolsCacheMissCanSkipEnsure(t *testing.T) {
	cache := &fakeToolCacheStore{}
	ensurer := &fakeToolCacheEnsurer{}
	e := newTestEngine(Deps{ToolCache: cache, CacheEnsurer: ensurer})

	w := doJSON(e, http.MethodGet, "/api/admin/upstreams/up-1/tools?ensure=false", "")

	if w.Code != http.StatusOK {
		t.Fatalf("缓存缺失且跳过补拉时期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if ensurer.calls != 0 {
		t.Fatalf("ensure=false 不应触发缓存补拉，实际调用 %d 次", ensurer.calls)
	}
	if cache.getCalls != 1 {
		t.Fatalf("期望只读取一次缓存，实际 %d 次", cache.getCalls)
	}

	var got struct {
		ID        string           `json:"id"`
		Count     int              `json:"count"`
		Tools     []domain.ToolDef `json:"tools"`
		UpdatedAt *time.Time       `json:"updatedAt"`
	}
	unmarshalData(t, w, &got)
	if got.ID != "up-1" || got.Count != 0 || len(got.Tools) != 0 || got.UpdatedAt != nil {
		t.Fatalf("跳过补拉时应返回空缓存视图，实际：%+v", got)
	}
}

func TestListUpstreamToolSummariesDoesNotEnsure(t *testing.T) {
	updatedAt := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	upstream := &fakeUpstreamService{
		list: []domain.Upstream{
			{ID: "up-cached"},
			{ID: "up-missing"},
		},
	}
	cache := &fakeToolCacheStore{byID: map[string]struct {
		tools     []domain.ToolDef
		updatedAt time.Time
		found     bool
	}{
		"up-cached": {
			tools: []domain.ToolDef{{
				Name:       "search",
				UpstreamID: "up-cached",
			}},
			updatedAt: updatedAt,
			found:     true,
		},
	}}
	ensurer := &fakeToolCacheEnsurer{}
	e := newTestEngine(Deps{Upstream: upstream, ToolCache: cache, CacheEnsurer: ensurer})

	w := doJSON(e, http.MethodGet, "/api/admin/upstreams/tool-summaries", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if ensurer.calls != 0 {
		t.Fatalf("摘要接口不应触发缓存补拉，实际调用 %d 次", ensurer.calls)
	}
	if cache.getCalls != 2 {
		t.Fatalf("应按上游数量读取缓存摘要，实际 %d 次", cache.getCalls)
	}

	var got struct {
		Summaries []struct {
			ID            string                    `json:"id"`
			Count         int                       `json:"count"`
			UpdatedAt     *time.Time                `json:"updatedAt"`
			ChangeSummary *domain.ToolChangeSummary `json:"changeSummary"`
		} `json:"summaries"`
	}
	unmarshalData(t, w, &got)
	if len(got.Summaries) != 2 {
		t.Fatalf("摘要数量不符合预期：%+v", got.Summaries)
	}
	if got.Summaries[0].ID != "up-cached" || got.Summaries[0].Count != 1 || got.Summaries[0].UpdatedAt == nil {
		t.Fatalf("有缓存摘要不符合预期：%+v", got.Summaries[0])
	}
	if !got.Summaries[0].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updatedAt 期望 %s，实际 %s", updatedAt, got.Summaries[0].UpdatedAt)
	}
	if got.Summaries[0].ChangeSummary != nil {
		t.Fatalf("未注入变更摘要时不应返回 changeSummary：%+v", got.Summaries[0].ChangeSummary)
	}
	if got.Summaries[1].ID != "up-missing" || got.Summaries[1].Count != 0 || got.Summaries[1].UpdatedAt != nil {
		t.Fatalf("缺失缓存摘要不符合预期：%+v", got.Summaries[1])
	}
}

func TestListUpstreamToolSummariesIncludesChangeSummary(t *testing.T) {
	updatedAt := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	changeAt := time.Date(2026, 6, 23, 10, 1, 0, 0, time.UTC)
	upstream := &fakeUpstreamService{
		list: []domain.Upstream{{ID: "up-cached"}},
	}
	cache := &fakeToolCacheStore{
		tools: []domain.ToolDef{{
			Name:       "search",
			UpstreamID: "up-cached",
		}},
		updatedAt: updatedAt,
		found:     true,
		change: domain.ToolChangeSummary{
			Added:         2,
			Removed:       1,
			SchemaChanged: 3,
			SyncedAt:      changeAt,
		},
		changeOK: true,
	}
	e := newTestEngine(Deps{Upstream: upstream, ToolCache: cache, CacheEnsurer: &fakeToolCacheEnsurer{}})

	w := doJSON(e, http.MethodGet, "/api/admin/upstreams/tool-summaries", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got struct {
		Summaries []struct {
			ID            string                    `json:"id"`
			Count         int                       `json:"count"`
			UpdatedAt     *time.Time                `json:"updatedAt"`
			ChangeSummary *domain.ToolChangeSummary `json:"changeSummary"`
		} `json:"summaries"`
	}
	unmarshalData(t, w, &got)
	if len(got.Summaries) != 1 || got.Summaries[0].ChangeSummary == nil {
		t.Fatalf("应返回变更摘要：%+v", got.Summaries)
	}
	if got.Summaries[0].ChangeSummary.Added != 2 || got.Summaries[0].ChangeSummary.Removed != 1 || got.Summaries[0].ChangeSummary.SchemaChanged != 3 {
		t.Fatalf("变更摘要不符合预期：%+v", got.Summaries[0].ChangeSummary)
	}
	if !got.Summaries[0].ChangeSummary.SyncedAt.Equal(changeAt) {
		t.Fatalf("syncedAt 期望 %s，实际 %s", changeAt, got.Summaries[0].ChangeSummary.SyncedAt)
	}
}
