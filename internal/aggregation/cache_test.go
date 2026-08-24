package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/toolsearch"
)

type cacheTestStore struct {
	tools      map[string][]domain.ToolDef
	replaceErr error
	deleteErr  error
}

func (c *cacheTestStore) Get(_ context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	tools, ok := c.tools[upstreamID]
	return tools, time.Time{}, ok
}

func (c *cacheTestStore) Replace(_ context.Context, upstreamID string, tools []domain.ToolDef) error {
	if c.replaceErr != nil {
		return c.replaceErr
	}
	c.tools[upstreamID] = append([]domain.ToolDef(nil), tools...)
	return nil
}

func (c *cacheTestStore) Delete(_ context.Context, upstreamID string) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	delete(c.tools, upstreamID)
	return nil
}

type cacheTestUpstreams struct {
	upstreams []domain.Upstream
	calls     int
}

func (u *cacheTestUpstreams) ListUpstreams(_ context.Context) ([]domain.Upstream, error) {
	u.calls++
	return u.upstreams, nil
}

func newCacheTestService(store domain.Tool_Cache, upstreams UpstreamLister) *Service {
	return NewService(
		store,
		domain.NewRuleEngine(),
		upstreams,
		&invFakeAliases{byUpstream: map[string][]domain.AliasRule{}},
		&invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}},
		&invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}},
		&invFakeToolPolicies{},
	)
}

func TestBuildToolSetUsesShortAPIScopedCache(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up": {{OriginalName: "first", Name: "first"}},
	}}
	upstreams := &cacheTestUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up", 0)}}
	svc := newCacheTestService(store, upstreams)

	if _, err := svc.BuildToolSet(context.Background(), "key-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BuildToolSet(context.Background(), "key-a"); err != nil {
		t.Fatal(err)
	}
	if upstreams.calls != 1 {
		t.Fatalf("same API key should reuse cached aggregate, ListUpstreams calls=%d", upstreams.calls)
	}
	if _, err := svc.BuildToolSet(context.Background(), "key-b"); err != nil {
		t.Fatal(err)
	}
	if upstreams.calls != 2 {
		t.Fatalf("different API key must have an isolated cache entry, calls=%d", upstreams.calls)
	}
}

func TestInvalidatingCacheOnlyClearsAfterSuccessfulWrite(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up": {{OriginalName: "first", Name: "first"}},
	}}
	upstreams := &cacheTestUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up", 0)}}
	svc := newCacheTestService(store, upstreams)
	wrapped := NewInvalidatingCache(store, svc)

	if _, err := svc.BuildToolSet(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	svc.setCachedToolResult("", "first", json.RawMessage(`{}`), domain.ToolResult{Content: json.RawMessage(`[]`)}, time.Minute)
	if err := wrapped.Replace(context.Background(), "up", []domain.ToolDef{{OriginalName: "second", Name: "second"}}); err != nil {
		t.Fatal(err)
	}
	tools, err := svc.BuildToolSet(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if upstreams.calls != 2 || len(tools) != 1 || tools[0].Name != "second" {
		t.Fatalf("successful replacement must invalidate aggregate: calls=%d tools=%+v", upstreams.calls, tools)
	}
	if stats := svc.ToolResultCacheStats(); stats.Entries != 0 {
		t.Fatalf("tool-list replacement must also clear results tied to the prior source mapping: %+v", stats)
	}

	store.replaceErr = errors.New("write failed")
	svc.setCachedToolResult("", "second", json.RawMessage(`{}`), domain.ToolResult{Content: json.RawMessage(`[]`)}, time.Minute)
	if err := wrapped.Replace(context.Background(), "up", []domain.ToolDef{{OriginalName: "third", Name: "third"}}); !errors.Is(err, store.replaceErr) {
		t.Fatalf("expected write error, got %v", err)
	}
	if _, err := svc.BuildToolSet(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if upstreams.calls != 2 {
		t.Fatalf("failed replacement must retain aggregate cache, calls=%d", upstreams.calls)
	}
	if stats := svc.ToolResultCacheStats(); stats.Entries != 1 {
		t.Fatalf("failed replacement must retain prior call result cache: %+v", stats)
	}
}

func TestBuildToolSearchSetCachesLazyIndexAndBoundsEntries(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up": {{OriginalName: "first", Name: "first", Description: "first tool"}},
	}}
	upstreams := &cacheTestUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up", 0)}}
	svc := newCacheTestService(store, upstreams)
	base := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }

	first, firstIndex, err := svc.BuildToolSearchSet(context.Background(), "key-a")
	if err != nil || len(first) != 1 || firstIndex == nil {
		t.Fatalf("first search set unexpected: set=%+v index=%p err=%v", first, firstIndex, err)
	}
	second, secondIndex, err := svc.BuildToolSearchSet(context.Background(), "key-a")
	if err != nil || len(second) != 1 || firstIndex != secondIndex || upstreams.calls != 1 {
		t.Fatalf("search index should reuse aggregate cache: set=%+v same=%t upstreamCalls=%d err=%v", second, firstIndex == secondIndex, upstreams.calls, err)
	}

	// An expired key is discarded on its next access rather than retained.
	base = base.Add(toolSetCacheTTL + time.Nanosecond)
	if _, err := svc.BuildToolSet(context.Background(), "key-b"); err != nil {
		t.Fatal(err)
	}
	svc.toolSetCacheMu.RLock()
	_, expiredRetained := svc.toolSetCache["key-a"]
	svc.toolSetCacheMu.RUnlock()
	if expiredRetained {
		t.Fatal("expired aggregate entry should be removed while inserting a fresh key")
	}

	for i := 0; i < maxToolSetCacheEntries+5; i++ {
		if _, err := svc.BuildToolSet(context.Background(), fmt.Sprintf("key-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	svc.toolSetCacheMu.RLock()
	entryCount := len(svc.toolSetCache)
	svc.toolSetCacheMu.RUnlock()
	if entryCount > maxToolSetCacheEntries {
		t.Fatalf("aggregate cache must stay bounded: entries=%d max=%d", entryCount, maxToolSetCacheEntries)
	}
}

func TestBuildToolSearchSetDropsIndexWithoutDiscoveryProjection(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up": {{OriginalName: "first", Name: "first", Description: "first tool"}},
	}}
	upstreams := &cacheTestUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up", 0)}}
	svc := newCacheTestService(store, upstreams)
	base := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	staleIndex := toolsearch.Build([]toolsearch.Doc{{Name: "stale"}})

	// This is the only unsafe intermediate state a concurrent invalidation
	// could otherwise expose: a lazy index from an older projection but no
	// matching discovery slice. The service must rebuild, never pair them.
	svc.toolSetCache["key-a"] = cachedToolSet{
		tools:       []domain.ToolDef{{OriginalName: "first", Name: "first", Description: "first tool"}},
		builtAt:     base,
		searchIndex: staleIndex,
	}
	discoveries, index, err := svc.BuildToolSearchSet(context.Background(), "key-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(discoveries) != 1 || discoveries[0].Tool.Name != "first" || index == nil || index == staleIndex {
		t.Fatalf("search index must be rebuilt from the matching discovery projection: discoveries=%+v index=%p", discoveries, index)
	}
	if result := index.Search("first", 1, 0); len(result.Hits) != 1 || result.Hits[0].DocIndex != 0 {
		t.Fatalf("rebuilt index does not match discovery order: %+v", result)
	}
}

func TestFreshRestrictedBuildRetainsRoutingConfigsForOtherCachedKeys(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "a", Name: "a"}},
		"up-b": {{OriginalName: "b", Name: "b"}},
	}}
	upstreams := &cacheTestUpstreams{upstreams: []domain.Upstream{
		invEnabledUpstream("up-a", 0),
		invEnabledUpstream("up-b", 1),
	}}
	svc := newCacheTestService(store, upstreams)
	svc.SetUpstreamAccessAuthorizer(selectedUpstreamAuthorizer{allowed: "up-a"})

	if _, err := svc.BuildToolSet(context.Background(), "restricted-key"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.upstreamConfig("up-b"); !ok {
		t.Fatal("a restricted build must retain routing configuration for sources visible to other cached API keys")
	}
}

func TestBuildToolDiscoveriesOmitsInputSchema(t *testing.T) {
	store := &cacheTestStore{tools: map[string][]domain.ToolDef{
		"up": {{
			OriginalName: "query",
			Name:         "query",
			Description:  "query data",
			InputSchema:  []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		}},
	}}
	svc := newCacheTestService(store, &cacheTestUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up", 0)}})
	discoveries, err := svc.BuildToolDiscoveries(context.Background(), "key-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(discoveries) != 1 || discoveries[0].Tool.InputSchema != nil {
		t.Fatalf("discovery projection must omit input schema: %+v", discoveries)
	}
}
