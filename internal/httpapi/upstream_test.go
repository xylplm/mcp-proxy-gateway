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
