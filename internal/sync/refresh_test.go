package syncsvc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// stubFetcher 是 ToolFetcher 的可控替身：可指定返回的工具列表、错误或阻塞行为。
type stubFetcher struct {
	tools   []domain.ToolDef
	err     error
	block   time.Duration // >0 时在返回前阻塞，用于触发超时
	calls   int32
	lastCtx context.Context
}

func (f *stubFetcher) FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	f.calls++
	f.lastCtx = ctx
	if f.block > 0 {
		select {
		case <-time.After(f.block):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

// memCache 是 domain.Tool_Cache 的进程内替身，遵循整列表替换语义，
// 并可注入 Replace 失败以验证「写入失败保留旧缓存」。
type memCache struct {
	mu          sync.Mutex
	tools       []domain.ToolDef
	updatedAt   time.Time
	has         bool
	replaceErr  error
	replaceHits int
}

var _ domain.Tool_Cache = (*memCache)(nil)

func (c *memCache) Get(ctx context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools, c.updatedAt, c.has
}

func (c *memCache) Replace(ctx context.Context, upstreamID string, tools []domain.ToolDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.replaceHits++
	if c.replaceErr != nil {
		return c.replaceErr
	}
	c.tools = tools
	c.updatedAt = time.Now()
	c.has = true
	return nil
}

func (c *memCache) Delete(ctx context.Context, upstreamID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = nil
	c.has = false
	return nil
}

func toolDef(name string) domain.ToolDef {
	return domain.ToolDef{
		OriginalName: name,
		Name:         name,
		InputSchema:  json.RawMessage(`{}`),
		UpstreamID:   "up-1",
	}
}

// TestRefreshSuccessReplacesCache 验证拉取成功时立即整列表替换缓存（Req 6.4）。
func TestRefreshSuccessReplacesCache(t *testing.T) {
	newTools := []domain.ToolDef{toolDef("a"), toolDef("b")}
	fetcher := &stubFetcher{tools: newTools}
	cache := &memCache{tools: []domain.ToolDef{toolDef("old")}, has: true}

	r := NewRefresher(fetcher, cache, 0, nil)
	got, err := r.Refresh(context.Background(), "up-1")
	if err != nil {
		t.Fatalf("刷新成功时不应返回错误：%v", err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("返回的工具列表与拉取结果不一致：%v", toolNamesOf(got))
	}
	// 缓存应被整列表替换为最新列表。
	cached, _, has := cache.Get(context.Background(), "up-1")
	if !has || len(cached) != 2 || cached[0].Name != "a" {
		t.Errorf("缓存应被整列表替换为最新列表，实际=%v has=%v", toolNamesOf(cached), has)
	}
	if cache.replaceHits != 1 {
		t.Errorf("期望恰好替换缓存一次，实际 %d 次", cache.replaceHits)
	}
}

// TestRefreshFetchFailureKeepsOldCache 验证拉取失败时保留旧缓存且返回刷新失败错误（Req 6.5）。
func TestRefreshFetchFailureKeepsOldCache(t *testing.T) {
	oldTools := []domain.ToolDef{toolDef("old")}
	fetcher := &stubFetcher{err: errors.New("connection refused")}
	cache := &memCache{tools: oldTools, has: true}

	r := NewRefresher(fetcher, cache, 0, nil)
	got, err := r.Refresh(context.Background(), "up-1")
	if err == nil {
		t.Fatal("拉取失败时应返回刷新失败错误")
	}
	if got != nil {
		t.Errorf("拉取失败时不应返回工具列表，实际=%v", toolNamesOf(got))
	}
	// 不应触碰缓存。
	if cache.replaceHits != 0 {
		t.Errorf("拉取失败时不应替换缓存，实际替换 %d 次", cache.replaceHits)
	}
	cached, _, has := cache.Get(context.Background(), "up-1")
	if !has || len(cached) != 1 || cached[0].Name != "old" {
		t.Errorf("拉取失败时应保留旧缓存，实际=%v has=%v", toolNamesOf(cached), has)
	}
	// 错误应为统一 APIError。
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
	}
}

// TestRefreshReplaceFailureKeepsOldCache 验证写缓存失败时保留旧缓存并返回错误（Req 6.5）。
func TestRefreshReplaceFailureKeepsOldCache(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{
		tools:      []domain.ToolDef{toolDef("old")},
		has:        true,
		replaceErr: domain.NewError(domain.CodeNotFound, "上游不存在"),
	}

	r := NewRefresher(fetcher, cache, 0, nil)
	_, err := r.Refresh(context.Background(), "up-1")
	if err == nil {
		t.Fatal("写缓存失败时应返回刷新失败错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，实际 %T", err)
	}
	// 底层 NOT_FOUND 应被沿用。
	if apiErr.Code != domain.CodeNotFound {
		t.Errorf("期望沿用底层错误码 NOT_FOUND，实际 %q", apiErr.Code)
	}
	// 缓存内容应保持旧值（memCache 在 replaceErr 时不改写 tools）。
	cached, _, _ := cache.Get(context.Background(), "up-1")
	if len(cached) != 1 || cached[0].Name != "old" {
		t.Errorf("写缓存失败时应保留旧缓存，实际=%v", toolNamesOf(cached))
	}
}

// TestRefreshTimeoutClassifiedAsUpstreamTimeout 验证拉取超时被归类为 UPSTREAM_TIMEOUT
// 且保留旧缓存（Req 6.5）。
func TestRefreshTimeoutClassifiedAsUpstreamTimeout(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}, block: 200 * time.Millisecond}
	cache := &memCache{tools: []domain.ToolDef{toolDef("old")}, has: true}

	r := NewRefresher(fetcher, cache, 20*time.Millisecond, nil)
	_, err := r.Refresh(context.Background(), "up-1")
	if err == nil {
		t.Fatal("拉取超时应返回刷新失败错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，实际 %T", err)
	}
	if apiErr.Code != domain.CodeUpstreamTimeout {
		t.Errorf("拉取超时应归类为 UPSTREAM_TIMEOUT，实际 %q", apiErr.Code)
	}
	if cache.replaceHits != 0 {
		t.Errorf("拉取超时时不应替换缓存，实际替换 %d 次", cache.replaceHits)
	}
}

// TestRefreshRejectsEmptyUpstreamID 验证空上游标识返回校验错误且不拉取。
func TestRefreshRejectsEmptyUpstreamID(t *testing.T) {
	fetcher := &stubFetcher{}
	cache := &memCache{}
	r := NewRefresher(fetcher, cache, 0, nil)

	_, err := r.Refresh(context.Background(), "")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("空上游标识期望 VALIDATION 错误，实际 %q", apiErr.Code)
	}
	if fetcher.calls != 0 {
		t.Errorf("空上游标识不应触发拉取，实际拉取 %d 次", fetcher.calls)
	}
}

// toolNamesOf 提取工具名称便于断言输出。
func toolNamesOf(tools []domain.ToolDef) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}
