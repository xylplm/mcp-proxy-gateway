package syncsvc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// gateFetcher 是支持显式同步的 ToolFetcher 替身：每次拉取在 started 上发信号、
// 随后阻塞在 release 上，直到测试放行。用于确定性地构造「上一次同步尚未完成」的
// 并发去重场景（Req 7.8）。calls 用原子计数，保证并发读取安全。
type gateFetcher struct {
	tools   []domain.ToolDef
	err     error
	calls   atomic.Int32
	started chan struct{} // 每次进入 FetchTools 发一个信号
	release chan struct{} // 关闭后所有阻塞的拉取继续返回
}

func newGateFetcher(tools []domain.ToolDef) *gateFetcher {
	return &gateFetcher{
		tools:   tools,
		started: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
}

func (f *gateFetcher) FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	f.calls.Add(1)
	select {
	case f.started <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *gateFetcher) callCount() int32 { return f.calls.Load() }

// stubLister 是 UpstreamLister 的可控替身：返回预设的上游列表或枚举错误。
type stubLister struct {
	ups []domain.Upstream
	err error
}

func (l *stubLister) List(ctx context.Context) ([]domain.Upstream, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.ups, nil
}

// upstream 构造一个带指定启停/自动同步标志的上游 MCP 实例，便于筛选断言。
func upstream(id string, enabled, autoSync bool) domain.Upstream {
	return domain.Upstream{
		ID: id,
		Config: domain.UpstreamConfig{
			Name:     id,
			Enabled:  enabled,
			AutoSync: autoSync,
		},
	}
}

// TestSyncOneSuccessReplacesCache 验证单次同步成功时整列表替换缓存（Req 6.1、7.1）。
func TestSyncOneSuccessReplacesCache(t *testing.T) {
	newTools := []domain.ToolDef{toolDef("a"), toolDef("b")}
	fetcher := &stubFetcher{tools: newTools}
	cache := &memCache{tools: []domain.ToolDef{toolDef("old")}, has: true}

	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)
	ran, err := s.SyncOne(context.Background(), "up-1")
	if err != nil {
		t.Fatalf("同步成功时不应返回错误：%v", err)
	}
	if !ran {
		t.Error("同步应实际执行，ran 期望 true")
	}
	cached, _, has := cache.Get(context.Background(), "up-1")
	if !has || len(cached) != 2 || cached[0].Name != "a" || cached[1].Name != "b" {
		t.Errorf("缓存应被整列表替换为最新列表，实际=%v has=%v", toolNamesOf(cached), has)
	}
	if cache.replaceHits != 1 {
		t.Errorf("期望恰好替换缓存一次，实际 %d 次", cache.replaceHits)
	}
}

// TestSyncOneFailureKeepsOldCache 验证同步失败时保留旧缓存且不触碰缓存（Req 7.5）。
func TestSyncOneFailureKeepsOldCache(t *testing.T) {
	fetcher := &stubFetcher{err: errors.New("connection refused")}
	cache := &memCache{tools: []domain.ToolDef{toolDef("old")}, has: true}

	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)
	ran, err := s.SyncOne(context.Background(), "up-1")
	if err == nil {
		t.Fatal("拉取失败时应返回同步失败错误")
	}
	if !ran {
		t.Error("拉取失败仍属于实际执行了一次同步，ran 期望 true")
	}
	if cache.replaceHits != 0 {
		t.Errorf("同步失败时不应替换缓存，实际替换 %d 次", cache.replaceHits)
	}
	cached, _, has := cache.Get(context.Background(), "up-1")
	if !has || len(cached) != 1 || cached[0].Name != "old" {
		t.Errorf("同步失败时应保留旧缓存，实际=%v has=%v", toolNamesOf(cached), has)
	}
}

// TestSyncOneTimeoutKeepsOldCache 验证同步超时被归类为 UPSTREAM_TIMEOUT 且保留旧缓存
// （Req 7.5）。
func TestSyncOneTimeoutKeepsOldCache(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}, block: 200 * time.Millisecond}
	cache := &memCache{tools: []domain.ToolDef{toolDef("old")}, has: true}

	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 20*time.Millisecond, nil)
	_, err := s.SyncOne(context.Background(), "up-1")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeUpstreamTimeout {
		t.Errorf("同步超时应归类为 UPSTREAM_TIMEOUT，实际 %q", apiErr.Code)
	}
	if cache.replaceHits != 0 {
		t.Errorf("同步超时时不应替换缓存，实际替换 %d 次", cache.replaceHits)
	}
}

// TestSyncOneConcurrentDedup 验证同一上游的第二次并发触发被跳过（Req 7.8）。
//
// 第一次触发进入 fetcher 后阻塞，制造「上一次同步尚未完成」的状态；此时第二次触发
// 应立即返回 ran=false 且不调用 fetcher。
func TestSyncOneConcurrentDedup(t *testing.T) {
	fetcher := newGateFetcher([]domain.ToolDef{toolDef("a")})
	cache := &memCache{}
	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)

	// 第一次触发：在后台执行，将阻塞在 fetcher.release 上。
	var firstRan atomic.Int32
	var firstErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ran, err := s.SyncOne(context.Background(), "up-1")
		if ran {
			firstRan.Store(1)
		}
		firstErr = err
	}()

	// 等待第一次拉取确实进入 fetcher（已登记 in-flight 标志）。
	select {
	case <-fetcher.started:
	case <-time.After(2 * time.Second):
		t.Fatal("第一次拉取未在预期时间内进入 fetcher")
	}

	// 第二次触发：上一次尚未完成，应被跳过。
	ran, err := s.SyncOne(context.Background(), "up-1")
	if err != nil {
		t.Fatalf("被跳过的触发不应返回错误：%v", err)
	}
	if ran {
		t.Error("第二次并发触发应被跳过，ran 期望 false")
	}
	if got := fetcher.callCount(); got != 1 {
		t.Errorf("被跳过的触发不应调用 fetcher，期望拉取 1 次，实际 %d 次", got)
	}

	// 放行第一次触发并等待其完成。
	close(fetcher.release)
	<-done
	if firstErr != nil {
		t.Fatalf("第一次同步不应返回错误：%v", firstErr)
	}
	if firstRan.Load() != 1 {
		t.Error("第一次触发应实际执行，ran 期望 true")
	}
	if cache.replaceHits != 1 {
		t.Errorf("第一次同步应替换缓存一次（第二次被跳过未写入），实际 %d 次", cache.replaceHits)
	}
}

// TestSyncOneSequentialAfterCompletionRuns 验证同步完成后去重标志被释放，后续触发可再次执行。
func TestSyncOneSequentialAfterCompletionRuns(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{}
	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)

	for i := range 3 {
		ran, err := s.SyncOne(context.Background(), "up-1")
		if err != nil {
			t.Fatalf("第 %d 次同步不应返回错误：%v", i+1, err)
		}
		if !ran {
			t.Errorf("第 %d 次顺序同步应实际执行（去重标志已释放），ran 期望 true", i+1)
		}
	}
	if cache.replaceHits != 3 {
		t.Errorf("三次顺序同步应替换缓存 3 次，实际 %d 次", cache.replaceHits)
	}
}

// TestEnsureCachedMissTriggersPull 验证缓存缺失时触发一次拉取并替换缓存（Req 6.3）。
func TestEnsureCachedMissTriggersPull(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{has: false} // 缓存缺失
	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)

	ran, err := s.EnsureCached(context.Background(), "up-1")
	if err != nil {
		t.Fatalf("缓存缺失补拉不应返回错误：%v", err)
	}
	if !ran {
		t.Error("缓存缺失应触发一次拉取，ran 期望 true")
	}
	if fetcher.calls != 1 {
		t.Errorf("缓存缺失应触发恰好一次拉取，实际 %d 次", fetcher.calls)
	}
	cached, _, has := cache.Get(context.Background(), "up-1")
	if !has || len(cached) != 1 || cached[0].Name != "a" {
		t.Errorf("缓存缺失补拉后应写入最新列表，实际=%v has=%v", toolNamesOf(cached), has)
	}
}

// TestEnsureCachedHitSkipsPull 验证缓存命中时不触发拉取（Req 6.3 反向）。
func TestEnsureCachedHitSkipsPull(t *testing.T) {
	fetcher := &stubFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{tools: []domain.ToolDef{toolDef("cached")}, has: true} // 缓存命中
	s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)

	ran, err := s.EnsureCached(context.Background(), "up-1")
	if err != nil {
		t.Fatalf("缓存命中不应返回错误：%v", err)
	}
	if ran {
		t.Error("缓存命中不应触发拉取，ran 期望 false")
	}
	if fetcher.calls != 0 {
		t.Errorf("缓存命中不应触发拉取，实际 %d 次", fetcher.calls)
	}
}

// TestSyncEnabledAutoSyncFiltersUpstreams 验证仅对「启用且开启自动同步」的上游触发同步
// （Req 7.1、7.2）。
func TestSyncEnabledAutoSyncFiltersUpstreams(t *testing.T) {
	lister := &stubLister{ups: []domain.Upstream{
		upstream("enabled-autosync", true, true),     // 应同步
		upstream("enabled-no-autosync", true, false), // 关闭自动同步，跳过（Req 7.2）
		upstream("disabled-autosync", false, true),   // 已停用，跳过
		upstream("disabled-no-autosync", false, false),
	}}
	fetcher := &recordingFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{}
	s := NewPeriodicSyncer(fetcher, cache, lister, 0, nil)

	s.SyncEnabledAutoSync(context.Background())

	synced := fetcher.syncedIDs()
	if len(synced) != 1 || synced[0] != "enabled-autosync" {
		t.Errorf("仅应同步启用且开启自动同步的上游，实际同步=%v", synced)
	}
}

// TestEnsureCachedForEnabledOnlyMissing 验证缺失缓存补拉仅针对已启用且缓存缺失的上游（Req 6.3）。
func TestEnsureCachedForEnabledOnlyMissing(t *testing.T) {
	lister := &stubLister{ups: []domain.Upstream{
		upstream("enabled-missing", true, false),   // 已启用、缓存缺失 → 补拉
		upstream("disabled-missing", false, false), // 已停用 → 跳过
	}}
	fetcher := &recordingFetcher{tools: []domain.ToolDef{toolDef("a")}}
	cache := &memCache{} // 全部缺失
	s := NewPeriodicSyncer(fetcher, cache, lister, 0, nil)

	s.EnsureCachedForEnabled(context.Background())

	synced := fetcher.syncedIDs()
	if len(synced) != 1 || synced[0] != "enabled-missing" {
		t.Errorf("仅应对已启用且缓存缺失的上游补拉，实际=%v", synced)
	}
}

// TestSyncOneRejectsEmptyUpstreamID 验证空上游标识返回校验错误且不拉取。
func TestSyncOneRejectsEmptyUpstreamID(t *testing.T) {
	fetcher := &stubFetcher{}
	s := NewPeriodicSyncer(fetcher, &memCache{}, &stubLister{}, 0, nil)

	ran, err := s.SyncOne(context.Background(), "")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("空上游标识期望 VALIDATION 错误，实际 %q", apiErr.Code)
	}
	if ran {
		t.Error("空上游标识不应执行同步，ran 期望 false")
	}
	if fetcher.calls != 0 {
		t.Errorf("空上游标识不应触发拉取，实际 %d 次", fetcher.calls)
	}
}

// recordingFetcher 记录被同步的上游标识，用于断言枚举筛选行为；并发安全。
type recordingFetcher struct {
	tools []domain.ToolDef
	err   error
	mu    sync.Mutex
	ids   []string
}

func (f *recordingFetcher) FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	f.mu.Lock()
	f.ids = append(f.ids, upstreamID)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *recordingFetcher) syncedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.ids))
	copy(out, f.ids)
	return out
}
