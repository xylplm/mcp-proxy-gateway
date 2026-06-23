package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type fakeConfig struct{ cfg config.YAMLConfig }

func (f fakeConfig) Config() config.YAMLConfig { return f.cfg }

type fakeRepo struct {
	events []store.SecurityEvent
	blocks []store.SecurityBlock
}

func (r *fakeRepo) InsertEvent(_ context.Context, ev store.SecurityEvent) (store.SecurityEvent, error) {
	ev.ID = int64(len(r.events) + 1)
	r.events = append(r.events, ev)
	return ev, nil
}

func (r *fakeRepo) CreateBlock(_ context.Context, block store.SecurityBlock) (store.SecurityBlock, error) {
	block.ID = "block-1"
	r.blocks = append(r.blocks, block)
	return block, nil
}

func (r *fakeRepo) ListEvents(context.Context, store.SecurityEventQuery) ([]store.SecurityEvent, error) {
	return r.events, nil
}

func (r *fakeRepo) ListBlocks(context.Context, store.SecurityBlockQuery) ([]store.SecurityBlock, error) {
	return r.blocks, nil
}

func (r *fakeRepo) ReleaseBlock(_ context.Context, id string, releasedAt time.Time) (store.SecurityBlock, error) {
	for i := range r.blocks {
		if r.blocks[i].ID == id {
			r.blocks[i].Status = store.SecurityBlockStatusReleased
			r.blocks[i].ReleasedAt = &releasedAt
			return r.blocks[i], nil
		}
	}
	return store.SecurityBlock{}, nil
}

func (r *fakeRepo) MarkExpiredBlocks(context.Context, time.Time) (int64, error) { return 0, nil }

func (r *fakeRepo) ListActiveBlocks(context.Context, time.Time) ([]store.SecurityBlock, error) {
	return r.blocks, nil
}

func (r *fakeRepo) CountBlocksBySubjectSince(context.Context, string, string, time.Time) (int64, error) {
	return int64(len(r.blocks)), nil
}

func (r *fakeRepo) Summary(context.Context, time.Time) (store.SecuritySummary, error) {
	return store.SecuritySummary{}, nil
}

type fakeCache struct {
	counts map[string]int64
	blocks map[string]bool
}

func newFakeCache() *fakeCache {
	return &fakeCache{counts: make(map[string]int64), blocks: make(map[string]bool)}
}

func (c *fakeCache) Incr(_ context.Context, key string, _ time.Duration) (int64, error) {
	c.counts[key]++
	return c.counts[key], nil
}

func (c *fakeCache) SetBlock(_ context.Context, subjectType, subject string, _ time.Duration) error {
	c.blocks[subjectType+":"+subject] = true
	return nil
}

func (c *fakeCache) IsBlocked(_ context.Context, subjectType, subject string) (bool, error) {
	return c.blocks[subjectType+":"+subject], nil
}

func (c *fakeCache) DeleteBlock(_ context.Context, subjectType, subject string) error {
	delete(c.blocks, subjectType+":"+subject)
	return nil
}

func newSecurityTestContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/mcp/http", nil)
	req.RemoteAddr = "203.0.113.10:4321"
	req.Header.Set("User-Agent", "security-test")
	c.Request = req
	return c
}

func securityTestConfig(mode string) config.YAMLConfig {
	cfg := config.DefaultYAMLConfig()
	cfg.Security.Mode = mode
	cfg.Security.FailureWindowS = 300
	cfg.Security.MaxFailuresPerIP = 2
	cfg.Security.MaxFailuresPerKeyFingerprint = 2
	cfg.Security.FirstBlockDurationS = 60
	cfg.Security.MaxBlockDurationS = 300
	cfg.Security.EscalationWindowS = 300
	cfg.JWTSecret = "test-secret"
	return cfg
}

func TestRecordAuthFailureMonitorOnlyRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &fakeRepo{}
	cache := newFakeCache()
	guard := NewGuard(repo, cache, fakeConfig{cfg: securityTestConfig(config.SecurityModeMonitor)}, nil)
	guard.now = func() time.Time { return time.Unix(1000, 0) }

	guard.RecordAuthFailure(newSecurityTestContext(), "mpg_bad", "invalid_key")
	guard.RecordAuthFailure(newSecurityTestContext(), "mpg_bad", "invalid_key")

	if len(repo.events) == 0 {
		t.Fatal("monitor 模式应记录安全事件")
	}
	if len(repo.blocks) != 0 || len(cache.blocks) != 0 {
		t.Fatalf("monitor 模式不应自动封禁，blocks=%d cache=%d", len(repo.blocks), len(cache.blocks))
	}
}

func TestRecordAuthFailureEnforceBlocksAtThreshold(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &fakeRepo{}
	cache := newFakeCache()
	guard := NewGuard(repo, cache, fakeConfig{cfg: securityTestConfig(config.SecurityModeEnforce)}, nil)
	guard.now = func() time.Time { return time.Unix(1000, 0) }

	guard.RecordAuthFailure(newSecurityTestContext(), "mpg_bad", "invalid_key")
	guard.RecordAuthFailure(newSecurityTestContext(), "mpg_bad", "invalid_key")

	if len(repo.blocks) == 0 {
		t.Fatal("enforce 模式达到阈值后应创建封禁记录")
	}
	if !cache.blocks[SubjectIP+":203.0.113.10"] {
		t.Fatalf("应写入 IP 热封禁缓存，cache=%v", cache.blocks)
	}
}

func TestReleaseBlockDeletesCacheAndRecordsEvent(t *testing.T) {
	repo := &fakeRepo{blocks: []store.SecurityBlock{{
		ID:          "block-1",
		SubjectType: SubjectIP,
		Subject:     "203.0.113.10",
		Status:      store.SecurityBlockStatusActive,
	}}}
	cache := newFakeCache()
	cache.blocks[SubjectIP+":203.0.113.10"] = true
	guard := NewGuard(repo, cache, fakeConfig{cfg: securityTestConfig(config.SecurityModeEnforce)}, nil)
	guard.now = func() time.Time { return time.Unix(1000, 0) }

	block, err := guard.ReleaseBlock(context.Background(), "block-1")
	if err != nil {
		t.Fatalf("ReleaseBlock 返回错误：%v", err)
	}
	if block.Status != store.SecurityBlockStatusReleased {
		t.Fatalf("期望状态 released，实际 %q", block.Status)
	}
	if cache.blocks[SubjectIP+":203.0.113.10"] {
		t.Fatal("解除封禁后应删除热封禁缓存")
	}
	if len(repo.events) != 1 || repo.events[0].EventType != EventReleased {
		t.Fatalf("解除封禁应记录释放事件，events=%+v", repo.events)
	}
}
