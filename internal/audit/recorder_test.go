package audit

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// recorderTestRepo 是 BatchWriter 窄接口的并发安全内存实现，用于在不触碰真实数据库的情况下
// 验证异步 Recorder 的非阻塞提交、批量落库、队列满丢弃与收尾语义。
type recorderTestRepo struct {
	mu     sync.Mutex
	rows   []store.AuditRecord
	nextID int64

	insertErr   error        // 注入 Insert 失败。
	failedCount atomic.Int64 // 因失败被丢弃的条数（由 Insert 返回错误计数）。
	panicOnce   bool
	panicCount  atomic.Int64
}

func (r *recorderTestRepo) Insert(_ context.Context, rec store.AuditRecord) (store.AuditRecord, error) {
	if r.panicOnce {
		r.panicOnce = false
		r.panicCount.Add(1)
		panic("audit repo panic")
	}
	if r.insertErr != nil {
		r.failedCount.Add(1)
		return store.AuditRecord{}, r.insertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	rec.ID = r.nextID
	if rec.OccurredAt.IsZero() {
		rec.OccurredAt = time.Now()
	}
	r.rows = append(r.rows, rec)
	return rec, nil
}

func (r *recorderTestRepo) snapshot() []store.AuditRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]store.AuditRecord, len(r.rows))
	copy(out, r.rows)
	return out
}

func (r *recorderTestRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

// newTestRecorder 构造一个使用内存仓储、短刷新周期与小批次的异步写入器，便于测试快速收敛。
func newTestRecorder(repo *recorderTestRepo, opts ...Option) *Recorder {
	opts = append([]Option{
		WithQueueSize(16),
		WithBatchSize(4),
		WithFlushInterval(10 * time.Millisecond),
	}, opts...)
	return NewRecorder(repo, opts...)
}

func TestRecorder_RecordLogin_EnqueuesEvent(t *testing.T) {
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo)
	rec.Start(context.Background())
	defer rec.Stop()

	if err := rec.RecordLogin(context.Background(), "admin", true); err != nil {
		t.Fatalf("RecordLogin 不应返回错误：%v", err)
	}

	if !waitFor(func() bool { return repo.count() == 1 }, time.Second) {
		t.Fatalf("应落库 1 条登录审计，实际 %d 条", repo.count())
	}

	rows := repo.snapshot()
	if rows[0].EventType != store.AuditEventLogin {
		t.Errorf("事件类型应为 %q，实际 %q", store.AuditEventLogin, rows[0].EventType)
	}
	if rows[0].Target != "admin" {
		t.Errorf("目标应为 admin，实际 %q", rows[0].Target)
	}
	if rows[0].OccurredAt.IsZero() {
		t.Error("发生时间戳不应为零值")
	}
}

func TestRecorder_RecordChange_WritesResourceDetail(t *testing.T) {
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo)
	rec.Start(context.Background())
	defer rec.Stop()

	if err := rec.RecordCreate(context.Background(), ResourceUpstream, "上游A"); err != nil {
		t.Fatalf("RecordCreate 不应返回错误：%v", err)
	}
	if err := rec.RecordUpdate(context.Background(), ResourceRule, "rule-1"); err != nil {
		t.Fatalf("RecordUpdate 不应返回错误：%v", err)
	}
	if err := rec.RecordDelete(context.Background(), ResourceAPIKey, "key-1"); err != nil {
		t.Fatalf("RecordDelete 不应返回错误：%v", err)
	}

	if !waitFor(func() bool { return repo.count() == 3 }, time.Second) {
		t.Fatalf("应落库 3 条审计，实际 %d 条", repo.count())
	}

	rows := repo.snapshot()
	wantKinds := []string{string(ResourceUpstream), string(ResourceRule), string(ResourceAPIKey)}
	for i, want := range wantKinds {
		detail := decodeDetail(t, rows[i].Detail)
		if detail["resource"] != want {
			t.Errorf("第 %d 条明细 resource 应为 %q，实际 %v", i, want, detail["resource"])
		}
	}
}

func TestRecorder_RecordAccessDenied_WithAndWithoutReason(t *testing.T) {
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo)
	rec.Start(context.Background())
	defer rec.Stop()

	if err := rec.RecordAccessDenied(context.Background(), "/api/admin/upstreams", "令牌无效"); err != nil {
		t.Fatalf("RecordAccessDenied 不应返回错误：%v", err)
	}
	if err := rec.RecordAccessDenied(context.Background(), "/api/admin/keys", ""); err != nil {
		t.Fatalf("RecordAccessDenied 不应返回错误：%v", err)
	}

	if !waitFor(func() bool { return repo.count() == 2 }, time.Second) {
		t.Fatalf("应落库 2 条审计，实际 %d 条", repo.count())
	}

	rows := repo.snapshot()
	if rows[0].Target != "/api/admin/upstreams" {
		t.Errorf("目标应为请求路径，实际 %q", rows[0].Target)
	}
	detail := decodeDetail(t, rows[0].Detail)
	if detail["reason"] != "令牌无效" {
		t.Errorf("明细 reason 应为 令牌无效，实际 %v", detail["reason"])
	}
	if len(rows[1].Detail) != 0 {
		t.Errorf("原因为空时不应写入明细，实际 %s", rows[1].Detail)
	}
}

func TestRecorder_QueueFull_DropsSilently(t *testing.T) {
	// 队列容量 4，不启动 worker：提交超过容量的记录，多余的被静默丢弃，绝不阻塞或 panic。
	repo := &recorderTestRepo{}
	rec := NewRecorder(repo, WithQueueSize(4), WithFlushInterval(time.Hour))
	// 故意不 Start，记录滞留队列。

	for range 20 {
		// 提交应快速返回，不阻塞。
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = rec.RecordLogin(context.Background(), "admin", true)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("队列满时 Submit 不应阻塞")
		}
	}

	// 未 Start 故无落库；启动后只消费队列中幸存的记录（至多 4 条）。
	rec.Start(context.Background())
	defer rec.Stop()

	if !waitFor(func() bool { return repo.count() <= 4 }, time.Second) {
		t.Fatalf("幸存记录应不超过队列容量 4，实际 %d 条", repo.count())
	}
}

func TestRecorder_InsertError_DropsSilently(t *testing.T) {
	// 落库失败应静默丢弃，不影响 worker 继续运行、不 panic、不报错。
	repo := &recorderTestRepo{insertErr: errors.New("db down")}
	rec := newTestRecorder(repo)
	rec.Start(context.Background())
	defer rec.Stop()

	for range 5 {
		if err := rec.RecordLogin(context.Background(), "admin", true); err != nil {
			t.Fatalf("RecordLogin 不应返回错误：%v", err)
		}
	}

	// 等待 worker 消费：无落库记录但失败计数 > 0。
	if !waitFor(func() bool { return repo.failedCount.Load() > 0 }, time.Second) {
		t.Fatalf("落库失败应被静默丢弃，失败计数应 > 0")
	}
	if repo.count() != 0 {
		t.Errorf("落库失败时不应写入任何记录，实际 %d 条", repo.count())
	}
}

func TestRecorder_InsertPanic_ContinuesWorker(t *testing.T) {
	repo := &recorderTestRepo{panicOnce: true}
	rec := newTestRecorder(repo, WithBatchSize(1), WithFlushInterval(5*time.Millisecond))
	rec.Start(context.Background())
	defer rec.Stop()

	if err := rec.RecordLogin(context.Background(), "panic-once", true); err != nil {
		t.Fatalf("RecordLogin 不应返回错误：%v", err)
	}
	if !waitFor(func() bool { return repo.panicCount.Load() == 1 }, time.Second) {
		t.Fatal("应观察到一次审计落库 panic")
	}
	if !rec.Running() {
		t.Fatal("审计落库 panic 不应导致 worker 退出")
	}

	if err := rec.RecordLogin(context.Background(), "after-panic", true); err != nil {
		t.Fatalf("RecordLogin 不应返回错误：%v", err)
	}
	if !waitFor(func() bool { return repo.count() == 1 }, time.Second) {
		t.Fatalf("panic 后的审计记录应继续落库，实际 %d 条", repo.count())
	}
}

func TestRecorder_StartStop_Idempotent(t *testing.T) {
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo)

	// 重复 Start 幂等。
	rec.Start(context.Background())
	rec.Start(context.Background())
	if !rec.Running() {
		t.Fatal("Start 后应处于运行状态")
	}

	// 重复 Stop 幂等。
	rec.Stop()
	rec.Stop()
	if rec.Running() {
		t.Fatal("Stop 后应处于停止状态")
	}
}

func TestRecorder_Stop_DrainsPending(t *testing.T) {
	// Stop 前提交一批记录，Stop 应收尾落库（不丢失已入队记录）。
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo, WithFlushInterval(time.Hour)) // 长 flush，确保只能靠 Stop 收尾。

	rec.Start(context.Background())
	for range 5 {
		if err := rec.RecordLogin(context.Background(), "admin", true); err != nil {
			t.Fatalf("RecordLogin 不应返回错误：%v", err)
		}
	}
	rec.Stop() // 收尾应落库这 5 条。

	if repo.count() != 5 {
		t.Fatalf("Stop 收尾应落库全部 5 条，实际 %d 条", repo.count())
	}
}

func TestRecorder_ContextCancel_StopsWorker(t *testing.T) {
	repo := &recorderTestRepo{}
	rec := newTestRecorder(repo)

	ctx, cancel := context.WithCancel(context.Background())
	rec.Start(ctx)

	if err := rec.RecordLogin(context.Background(), "admin", true); err != nil {
		t.Fatalf("RecordLogin 不应返回错误：%v", err)
	}
	if !waitFor(func() bool { return repo.count() == 1 }, time.Second) {
		t.Fatalf("应落库 1 条，实际 %d 条", repo.count())
	}

	cancel() // 取消运行上下文应使 worker 退出并收尾。
	// 给 worker 收尾退出留出时间。
	time.Sleep(50 * time.Millisecond)

	// worker 退出后提交的记录暂存队列不再被消费，落库数不再增长。
	before := repo.count()
	for range 3 {
		if err := rec.RecordLogin(context.Background(), "admin", true); err != nil {
			t.Fatalf("RecordLogin 不应返回错误：%v", err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if repo.count() != before {
		t.Fatalf("ctx 取消后 worker 应已退出，不再落库；提交前后均为 %d，实际 %d", before, repo.count())
	}
	// 显式 Stop 收尾（幂等），避免 goroutine 泄漏。
	rec.Stop()
}

// waitFor 轮询 cond 直至返回 true 或超时，用于等待异步落库收敛。
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// decodeDetail 将明细 JSON 反序列化为 map，便于断言字段。
func decodeDetail(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("明细应为合法 JSON：%v", err)
	}
	return out
}
