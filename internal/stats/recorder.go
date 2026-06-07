package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// StatBufferKey 为统计异步缓冲在 Redis 中的键，遵循设计文档「Redis 缓存键设计」的
// mpg: 分域约定（Req 16.8）。主流程将调用记录 LPUSH 入该 List，后台 worker 批量 RPOP
// 落库到 call_stat。
const StatBufferKey = "mpg:stats:buffer"

// 异步缓冲与批量落库的默认参数。
const (
	// defaultQueueSize 为主流程与后台 worker 之间的本地缓冲队列容量。
	//
	// 该队列是「非阻塞提交」的关键：RecordAsync 对其做非阻塞写入，队列满时静默丢弃，
	// 从而保证主流程附加耗时极小且绝不阻塞（Req 16.8、16.9）。
	defaultQueueSize = 4096
	// defaultBatchSize 为单批 LPUSH 与单批 RPOP/INSERT 的记录条数上限。
	defaultBatchSize = 128
	// defaultFlushInterval 为后台 worker 的定时刷新周期：周期性将待发批次推入缓冲、
	// 并从缓冲批量落库，避免低流量时记录长时间滞留。
	defaultFlushInterval = time.Second
	// shutdownDrainTimeout 为停止时收尾落库的最长等待时间。
	shutdownDrainTimeout = 5 * time.Second
)

// CallRecord 是一条异步提交的调用统计记录。
//
// 复用 store.CallStatRecord 作为统计维度载体（稳定标识 (UpstreamID, OriginalName)、
// 所用 API Key、毫秒时间戳、耗时与成功/失败状态，Req 16.1），避免重复定义与多余转换。
type CallRecord = store.CallStatRecord

// StatBuffer 是统计异步缓冲（Redis List mpg:stats:buffer）的窄接口（Req 16.8）。
//
// 仅声明异步缓冲实际需要的两个操作：Push 批量入队（LPUSH）、PopBatch 批量出队（RPOP）。
// 以接口而非具体 Redis 客户端依赖，便于单元测试以内存 fake 替换，使批量缓冲与落库的
// 核心逻辑可脱离真实 Redis 验证。*redis.Client 经 NewRedisStatBuffer 适配后满足该接口。
type StatBuffer interface {
	// Push 将一批已序列化的记录入队（LPUSH 到 List 头部）。空切片为无操作。
	Push(ctx context.Context, items ...string) error
	// PopBatch 从队尾（RPOP）批量取出至多 max 条记录；队列为空时返回空切片而非错误。
	PopBatch(ctx context.Context, max int) ([]string, error)
}

// StatWriter 是调用统计落库（call_stat 表）的窄接口（Req 16.1）。
//
// 仅声明后台 worker 实际需要的批量写入能力。*store.CallStatRepo 满足该接口；以接口依赖
// 便于在单元测试中以内存 fake 替换并注入写入失败，验证「写入失败静默丢弃」（Req 16.9）。
type StatWriter interface {
	// Insert 批量写入调用统计记录。空切片为无操作。
	Insert(ctx context.Context, records []store.CallStatRecord) error
}

// Recorder 实现统计服务（Statistics_Service）的异步写入与降级（Req 16.1、16.8、16.9）。
//
// 数据流（见设计文档「统计异步写入与降级」）：
//
//	主流程 RecordAsync → 本地缓冲队列（非阻塞，<50ms）
//	后台 worker → 批量 LPUSH mpg:stats:buffer → 批量 RPOP → 批量 INSERT call_stat
//	  任一环节失败 → 静默丢弃该批记录，不影响主流程、不返回错误（Req 16.9）
//
// 设计要点：
//   - RecordAsync 仅对本地缓冲队列做非阻塞写入，绝不进行 Redis/DB I/O，故主流程附加耗时
//     极小且永不阻塞（Req 16.8）；队列满时静默丢弃（Req 16.9）。
//   - Redis List 作为跨阶段的持久缓冲：进程在 LPUSH 之后、INSERT 之前重启，记录仍存于
//     Redis；多实例部署下任一实例的 worker 均可消费缓冲（解耦主流程与落库）。
//   - 当未注入 buffer（buffer 为 nil）时降级为「本地队列 → 直接批量 INSERT」，使 Redis
//     不可用时仍可异步落库，符合任务允许的「本地有缓冲 channel + 后台 worker」方案。
type Recorder struct {
	// buffer 为 Redis 异步缓冲；为 nil 时降级为本地队列直接落库。
	buffer StatBuffer
	// writer 为 call_stat 批量写入仓储。
	writer StatWriter
	// queue 为主流程与 worker 之间的本地非阻塞缓冲队列。
	queue chan store.CallStatRecord
	// batchSize 为单批缓冲入队与单批落库的记录条数上限。
	batchSize int
	// flushInterval 为 worker 定时刷新周期。
	flushInterval time.Duration
	// log 为结构化日志器；记录降级丢弃事件，便于观测（不向调用方报错）。
	log *slog.Logger

	// mu 保护启停相关的可变状态（started/cancel）。
	mu sync.Mutex
	// started 标记 worker 是否在运行，保证 Start/Stop 幂等。
	started bool
	// cancel 取消 worker 运行上下文：Stop 调用它以停止 worker。
	cancel context.CancelFunc
	// wg 等待 worker 退出，使 Stop 返回时收尾落库确已完成。
	wg sync.WaitGroup
}

// Option 为 Recorder 的可选配置项（函数式选项）。
type Option func(*Recorder)

// WithQueueSize 设置本地缓冲队列容量（<=0 时回退默认值）。
func WithQueueSize(n int) Option {
	return func(r *Recorder) {
		if n > 0 {
			r.queue = make(chan store.CallStatRecord, n)
		}
	}
}

// WithBatchSize 设置单批缓冲入队与落库的记录条数上限（<=0 时回退默认值）。
func WithBatchSize(n int) Option {
	return func(r *Recorder) {
		if n > 0 {
			r.batchSize = n
		}
	}
}

// WithFlushInterval 设置 worker 定时刷新周期（<=0 时回退默认值）。
func WithFlushInterval(d time.Duration) Option {
	return func(r *Recorder) {
		if d > 0 {
			r.flushInterval = d
		}
	}
}

// WithLogger 注入自定义日志器（为空时回退到 slog.Default()）。
func WithLogger(l *slog.Logger) Option {
	return func(r *Recorder) {
		if l != nil {
			r.log = l
		}
	}
}

// New 构造统计异步写入器。
//
// buffer 为 Redis 异步缓冲（可为 nil，降级为本地队列直接落库）；writer 为 call_stat
// 批量写入仓储（必需）。选项可覆盖队列容量、批次大小、刷新周期与日志器。
func New(buffer StatBuffer, writer StatWriter, opts ...Option) *Recorder {
	r := &Recorder{
		buffer:        buffer,
		writer:        writer,
		queue:         make(chan store.CallStatRecord, defaultQueueSize),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		log:           slog.Default(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RecordAsync 以非阻塞方式提交一条调用统计记录（Req 16.8、16.9）。
//
// 行为约定：
//   - 仅对本地缓冲队列做非阻塞写入并立即返回，不进行任何 Redis/DB I/O，主流程附加耗时
//     极小且永不阻塞（Req 16.8）。
//   - 队列已满（落库侧滞后形成背压）时静默丢弃该记录，不阻塞、不报错，绝不影响主流程
//     结果返回（Req 16.9）。
//   - CalledAt 为零值时回填当前时刻，保证记录的毫秒时间戳始终有效（Req 16.1）。
//
// ctx 预留用于与调用方生命周期对齐；本方法不因 ctx 取消而阻塞（写入恒为非阻塞）。
func (r *Recorder) RecordAsync(ctx context.Context, rec store.CallStatRecord) {
	_ = ctx
	if rec.CalledAt.IsZero() {
		rec.CalledAt = time.Now().UTC()
	}
	select {
	case r.queue <- rec:
	default:
		// 队列满：静默丢弃，绝不阻塞主流程（Req 16.9）。
		r.log.Warn("统计缓冲队列已满，丢弃调用统计记录",
			"upstreamID", rec.UpstreamID, "originalName", rec.OriginalName)
	}
}

// Start 启动后台落库 worker（幂等）。
//
// 传入的 ctx 应为应用生命周期级上下文：ctx 被取消或调用 Stop 均会停止 worker，
// 停止前会尽力将剩余记录收尾落库。
func (r *Recorder) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.started = true
	r.wg.Add(1)
	go r.run(runCtx)
}

// Stop 停止后台 worker 并等待其收尾退出（幂等）。
//
// 通过取消运行上下文实现：worker 响应取消后尽力把队列与缓冲中的剩余记录落库再退出。
// Stop 返回时 worker 确已收束。未启动时为无操作。
func (r *Recorder) Stop() {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	r.started = false
	r.cancel = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

// Running 报告 worker 当前是否在运行（主要用于测试与状态查询）。
func (r *Recorder) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// run 是后台落库循环：累积待发记录 → 批量入缓冲 → 批量落库，直至上下文取消（Req 16.8）。
//
// 触发批量的两种时机：待发批次达到 batchSize、或定时刷新周期到达。上下文取消时执行一次
// 收尾落库，尽力不丢失已入队记录（队列满被丢弃的记录除外，属正常背压降级）。
func (r *Recorder) run(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()

	pending := make([]store.CallStatRecord, 0, r.batchSize)

	for {
		select {
		case <-ctx.Done():
			// 收尾：尽力把队列中剩余记录拉出并落库后退出。
			r.shutdown(pending)
			return

		case rec := <-r.queue:
			pending = append(pending, rec)
			if len(pending) >= r.batchSize {
				r.flush(ctx, pending)
				pending = pending[:0]
			}

		case <-ticker.C:
			if len(pending) > 0 {
				r.flush(ctx, pending)
				pending = pending[:0]
			}
			// 即便本地无待发记录，也尝试消费缓冲中可能由其他实例写入的记录。
			r.drainBuffer(ctx)
		}
	}
}

// shutdown 在停止时执行收尾落库：非阻塞拉空队列后做一次完整刷新（Req 16.8）。
//
// 使用独立的限时上下文（与已取消的运行上下文解耦），保证收尾的 Redis/DB I/O 有机会完成。
func (r *Recorder) shutdown(pending []store.CallStatRecord) {
	for {
		select {
		case rec := <-r.queue:
			pending = append(pending, rec)
		default:
			drainCtx, cancel := context.WithTimeout(context.Background(), shutdownDrainTimeout)
			defer cancel()
			r.flush(drainCtx, pending)
			return
		}
	}
}

// flush 将一批待发记录推入缓冲并随即批量落库（Req 16.8）。
//
//   - 注入了 buffer：先 LPUSH 入 Redis 缓冲，再从缓冲批量 RPOP 落库（持久缓冲，解耦主
//     流程与落库，支持跨实例消费）。
//   - 未注入 buffer：降级为直接批量落库（本地队列 + 后台 worker 方案）。
//
// 任一环节失败均静默丢弃、不向调用方报错（Req 16.9）。
func (r *Recorder) flush(ctx context.Context, pending []store.CallStatRecord) {
	if r.buffer == nil {
		r.writeDirect(ctx, pending)
		return
	}
	r.pushToBuffer(ctx, pending)
	r.drainBuffer(ctx)
}

// pushToBuffer 将一批记录序列化后 LPUSH 入 Redis 缓冲；失败静默丢弃（Req 16.9）。
func (r *Recorder) pushToBuffer(ctx context.Context, recs []store.CallStatRecord) {
	if r.buffer == nil || len(recs) == 0 {
		return
	}
	items := make([]string, 0, len(recs))
	for _, rec := range recs {
		raw, err := json.Marshal(rec)
		if err != nil {
			// 序列化失败：丢弃该条，不影响其余记录与主流程（Req 16.9）。
			r.log.Warn("序列化调用统计记录失败，丢弃", "error", err)
			continue
		}
		items = append(items, string(raw))
	}
	if len(items) == 0 {
		return
	}
	if err := r.buffer.Push(ctx, items...); err != nil {
		// LPUSH 失败：静默丢弃该批，不影响主流程（Req 16.9）。
		r.log.Warn("LPUSH 统计缓冲失败，丢弃该批记录", "count", len(items), "error", err)
	}
}

// drainBuffer 从 Redis 缓冲批量 RPOP 记录并落库，直至缓冲清空或出错（Req 16.8、16.9）。
//
// 注意：RPOP 已将记录从缓冲移除，若随后 INSERT 失败则该批记录丢失——这正是「写入失败
// 静默丢弃、不影响主流程」的预期降级行为（Req 16.9）。
func (r *Recorder) drainBuffer(ctx context.Context) {
	if r.buffer == nil {
		return
	}
	for {
		items, err := r.buffer.PopBatch(ctx, r.batchSize)
		if err != nil {
			// 读取缓冲失败：本轮放弃落库，不报错（Req 16.9）。
			r.log.Warn("RPOP 统计缓冲失败", "error", err)
			return
		}
		if len(items) == 0 {
			return
		}
		recs := make([]store.CallStatRecord, 0, len(items))
		for _, it := range items {
			var rec store.CallStatRecord
			if uerr := json.Unmarshal([]byte(it), &rec); uerr != nil {
				// 反序列化失败：丢弃该条（Req 16.9）。
				r.log.Warn("反序列化统计记录失败，丢弃", "error", uerr)
				continue
			}
			recs = append(recs, rec)
		}
		r.insert(ctx, recs)
		// 不足一批说明缓冲已基本清空，结束本轮以避免空转。
		if len(items) < r.batchSize {
			return
		}
	}
}

// writeDirect 在未注入 Redis 缓冲时，将待发批次直接落库（降级路径，Req 16.9）。
func (r *Recorder) writeDirect(ctx context.Context, recs []store.CallStatRecord) {
	r.insert(ctx, recs)
}

// insert 批量写入 call_stat；失败静默丢弃、不报错（Req 16.9）。
func (r *Recorder) insert(ctx context.Context, recs []store.CallStatRecord) {
	if len(recs) == 0 {
		return
	}
	if err := r.writer.Insert(ctx, recs); err != nil {
		// 落库失败：丢弃该批，不影响主流程（Req 16.9）。
		r.log.Warn("批量写入 call_stat 失败，丢弃该批记录", "count", len(recs), "error", err)
	}
}
