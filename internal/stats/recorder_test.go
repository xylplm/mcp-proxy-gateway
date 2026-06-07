package stats

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件为任务 17.1「实现异步统计写入与降级」的单元测试，覆盖以下核心行为
// （Req 16.1、16.8、16.9）：
//   - RecordAsync 非阻塞提交：即便落库侧阻塞，主流程提交也不被阻塞，队列满静默丢弃；
//   - 后台 worker 经 Redis 缓冲（LPUSH→RPOP）批量落库 call_stat；
//   - 未注入 Redis 缓冲时降级为本地队列直接批量落库；
//   - 写入失败（Push/Pop/Insert 任一环节）静默丢弃，不 panic、不影响主流程。
//
// 测试均以内存 fake（fakeBuffer / fakeWriter）替换 Redis 与仓储，脱离真实基础设施验证逻辑。

// --- 测试替身 ---

// fakeBuffer 是 StatBuffer 的内存实现：以切片模拟 Redis List（头部 LPUSH、尾部 RPOP）。
type fakeBuffer struct {
	mu       sync.Mutex
	items    []string // items[0] 为 List 头部，末尾为尾部（RPOP 取末尾）
	pushErr  error
	popErr   error
	pushCnt  int
	popCount int
}

func (b *fakeBuffer) Push(_ context.Context, items ...string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pushCnt++
	if b.pushErr != nil {
		return b.pushErr
	}
	// LPUSH 将元素依次插入头部：与 go-redis 语义一致，多元素逆序进入。
	for _, it := range items {
		b.items = append([]string{it}, b.items...)
	}
	return nil
}

func (b *fakeBuffer) PopBatch(_ context.Context, max int) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.popCount++
	if b.popErr != nil {
		return nil, b.popErr
	}
	if len(b.items) == 0 {
		return nil, nil
	}
	n := max
	if n > len(b.items) {
		n = len(b.items)
	}
	// RPOP 自尾部取出 n 个。
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		last := len(b.items) - 1
		out = append(out, b.items[last])
		b.items = b.items[:last]
	}
	return out, nil
}

func (b *fakeBuffer) remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

// fakeWriter 是 StatWriter 的内存实现：累计落库记录；可注入写入失败与阻塞。
type fakeWriter struct {
	mu        sync.Mutex
	records   []store.CallStatRecord
	insertErr error
	block     chan struct{} // 非 nil 时 Insert 阻塞直至该通道关闭/收到信号
	calls     int
}

func (w *fakeWriter) Insert(_ context.Context, records []store.CallStatRecord) error {
	if w.block != nil {
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.insertErr != nil {
		return w.insertErr
	}
	w.records = append(w.records, records...)
	return nil
}

func (w *fakeWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.records)
}

// sampleRecord 构造一条测试用调用统计记录。
func sampleRecord(original string) store.CallStatRecord {
	return store.CallStatRecord{
		OriginalName: original,
		ExposedName:  original,
		CalledAt:     time.Now().UTC(),
		LatencyMS:    1,
		Success:      true,
	}
}

// waitFor 轮询条件直至成立或超时，避免对异步 worker 使用固定 sleep。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待条件超时（%s）", timeout)
}

// --- 测试用例 ---

// TestRecordAsyncDoesNotBlockWhenWriterBlocks 验证：即便落库侧完全阻塞，RecordAsync
// 仍迅速返回不被阻塞——异步提交不阻塞主流程（Req 16.8）。
func TestRecordAsyncDoesNotBlockWhenWriterBlocks(t *testing.T) {
	writer := &fakeWriter{block: make(chan struct{})}
	r := New(nil, writer, WithQueueSize(8), WithBatchSize(4), WithFlushInterval(time.Hour))
	r.Start(context.Background())
	defer func() {
		close(writer.block) // 解除阻塞，便于 worker 收尾退出
		r.Stop()
	}()

	// 连续非阻塞提交：即便 worker 卡在 Insert，提交也应在很短时间内全部返回。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 8; i++ {
			r.RecordAsync(context.Background(), sampleRecord("t"))
		}
		close(done)
	}()

	select {
	case <-done:
		// 提交未被阻塞，符合预期。
	case <-time.After(time.Second):
		t.Fatal("RecordAsync 被阻塞：异步提交不应等待落库（Req 16.8）")
	}
}

// TestRecordAsyncDropsWhenQueueFull 验证：队列满时静默丢弃，不阻塞、不 panic（Req 16.9）。
func TestRecordAsyncDropsWhenQueueFull(t *testing.T) {
	// 不启动 worker，使队列只进不出，从而可靠地填满。
	writer := &fakeWriter{}
	r := New(nil, writer, WithQueueSize(2))

	// 提交远超容量的记录；多余的应被静默丢弃且不阻塞。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.RecordAsync(context.Background(), sampleRecord("t"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("队列满时 RecordAsync 不应阻塞（Req 16.9）")
	}

	if got := len(r.queue); got > 2 {
		t.Fatalf("队列长度超出容量：got=%d cap=2", got)
	}
}

// TestWorkerFlushesThroughBufferToDB 验证：注入 Redis 缓冲时，worker 经 LPUSH→RPOP
// 将记录批量落库到 call_stat（Req 16.8）。
func TestWorkerFlushesThroughBufferToDB(t *testing.T) {
	buffer := &fakeBuffer{}
	writer := &fakeWriter{}
	r := New(buffer, writer, WithQueueSize(64), WithBatchSize(3), WithFlushInterval(10*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	const n = 7
	for i := 0; i < n; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}

	waitFor(t, 2*time.Second, func() bool { return writer.count() == n })

	if buffer.pushCnt == 0 {
		t.Error("应至少经过一次 LPUSH 缓冲（Req 16.8）")
	}
	if buffer.remaining() != 0 {
		t.Errorf("缓冲应被 worker 清空，剩余 %d 条", buffer.remaining())
	}
}

// TestWorkerDirectWriteWithoutBuffer 验证：未注入 Redis 缓冲时降级为本地队列直接落库。
func TestWorkerDirectWriteWithoutBuffer(t *testing.T) {
	writer := &fakeWriter{}
	r := New(nil, writer, WithQueueSize(64), WithBatchSize(2), WithFlushInterval(10*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	for i := 0; i < 5; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}
	waitFor(t, 2*time.Second, func() bool { return writer.count() == 5 })
}

// TestInsertFailureSilentlyDropped 验证：落库失败时静默丢弃、不影响主流程、不 panic（Req 16.9）。
func TestInsertFailureSilentlyDropped(t *testing.T) {
	buffer := &fakeBuffer{}
	writer := &fakeWriter{insertErr: errors.New("db down")}
	r := New(buffer, writer, WithQueueSize(64), WithBatchSize(2), WithFlushInterval(10*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	for i := 0; i < 4; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}

	// 等待 worker 至少尝试落库若干次；落库恒失败，记录被丢弃，count 始终为 0。
	waitFor(t, 2*time.Second, func() bool { return writer.calls > 0 })
	if writer.count() != 0 {
		t.Errorf("落库失败的记录不应被计入，got=%d", writer.count())
	}
	// 缓冲中的记录已被 RPOP 取出（随后 INSERT 失败丢弃），不应残留累积。
	waitFor(t, time.Second, func() bool { return buffer.remaining() == 0 })
}

// TestPushFailureSilentlyDropped 验证：LPUSH 失败时静默丢弃该批，不落库、不报错（Req 16.9）。
func TestPushFailureSilentlyDropped(t *testing.T) {
	buffer := &fakeBuffer{pushErr: errors.New("redis down")}
	writer := &fakeWriter{}
	r := New(buffer, writer, WithQueueSize(64), WithBatchSize(2), WithFlushInterval(10*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	for i := 0; i < 4; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}
	waitFor(t, 2*time.Second, func() bool { return buffer.pushCnt > 0 })
	// Push 恒失败：记录未进入缓冲，亦无从落库。
	time.Sleep(50 * time.Millisecond)
	if writer.count() != 0 {
		t.Errorf("LPUSH 失败时不应有记录落库，got=%d", writer.count())
	}
}

// TestPopFailureSilentlyDropped 验证：RPOP 失败时本轮放弃落库，不 panic、不报错（Req 16.9）。
func TestPopFailureSilentlyDropped(t *testing.T) {
	buffer := &fakeBuffer{popErr: errors.New("redis read err")}
	writer := &fakeWriter{}
	r := New(buffer, writer, WithQueueSize(64), WithBatchSize(2), WithFlushInterval(10*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	for i := 0; i < 4; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}
	waitFor(t, 2*time.Second, func() bool { return buffer.popCount > 0 })
	if writer.count() != 0 {
		t.Errorf("RPOP 失败时不应有记录落库，got=%d", writer.count())
	}
}

// TestStopDrainsRemainingRecords 验证：Stop 收尾时尽力把队列剩余记录落库（Req 16.8）。
func TestStopDrainsRemainingRecords(t *testing.T) {
	buffer := &fakeBuffer{}
	writer := &fakeWriter{}
	// 刷新周期设得很长，使停止前主要依赖 Stop 的收尾落库路径。
	r := New(buffer, writer, WithQueueSize(64), WithBatchSize(100), WithFlushInterval(time.Hour))
	r.Start(context.Background())

	const n = 5
	for i := 0; i < n; i++ {
		r.RecordAsync(context.Background(), sampleRecord("tool"))
	}
	r.Stop() // 触发收尾落库并等待 worker 退出

	if writer.count() != n {
		t.Errorf("Stop 收尾应落库全部剩余记录：want=%d got=%d", n, writer.count())
	}
}

// TestRecordAsyncFillsTimestamp 验证：CalledAt 为零值时回填当前时刻，保证时间戳有效（Req 16.1）。
func TestRecordAsyncFillsTimestamp(t *testing.T) {
	writer := &fakeWriter{}
	r := New(nil, writer, WithQueueSize(8), WithBatchSize(1), WithFlushInterval(5*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	r.RecordAsync(context.Background(), store.CallStatRecord{OriginalName: "tool"}) // CalledAt 零值
	waitFor(t, 2*time.Second, func() bool { return writer.count() == 1 })

	writer.mu.Lock()
	rec := writer.records[0]
	writer.mu.Unlock()
	if rec.CalledAt.IsZero() {
		t.Error("CalledAt 零值时应被回填为当前时刻（Req 16.1）")
	}
}

// TestBufferRoundTripJSON 验证：经缓冲落库的记录在 JSON 序列化往返后字段保持一致。
func TestBufferRoundTripJSON(t *testing.T) {
	buffer := &fakeBuffer{}
	writer := &fakeWriter{}
	r := New(buffer, writer, WithQueueSize(8), WithBatchSize(1), WithFlushInterval(5*time.Millisecond))
	r.Start(context.Background())
	defer r.Stop()

	want := store.CallStatRecord{
		OriginalName: "search",
		ExposedName:  "web_search",
		LatencyMS:    42,
		Success:      false,
		CalledAt:     time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	r.RecordAsync(context.Background(), want)
	waitFor(t, 2*time.Second, func() bool { return writer.count() == 1 })

	writer.mu.Lock()
	got := writer.records[0]
	writer.mu.Unlock()

	if got.OriginalName != want.OriginalName || got.ExposedName != want.ExposedName ||
		got.LatencyMS != want.LatencyMS || got.Success != want.Success || !got.CalledAt.Equal(want.CalledAt) {
		// 用 JSON 对比辅助定位差异。
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("缓冲往返后记录不一致：\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}
