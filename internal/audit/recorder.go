package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 异步审计写入的默认参数（与统计异步写入保持一致的节流语义，参见 stats 包）。
const (
	// defaultQueueSize 为本地缓冲队列容量。
	//
	// 该队列是「非阻塞提交」的关键：Submit 对其做非阻塞写入，队列满时静默丢弃，
	// 保证主流程（HTTP 处理器、鉴权中间件）附加耗时极小且绝不阻塞。
	defaultQueueSize = 2048
	// defaultBatchSize 为单批批量落库的记录条数上限。
	defaultBatchSize = 64
	// defaultFlushInterval 为后台 worker 的定时刷新周期：低流量时避免记录长时间滞留。
	defaultFlushInterval = time.Second
	// shutdownDrainTimeout 为停止时收尾落库的最长等待时间。
	shutdownDrainTimeout = 3 * time.Second
)

// BatchWriter 是审计记录批量落库的窄接口。
//
// 仅声明异步写入实际需要的单条插入能力（仓储当前只暴露 Insert，无批量方法；审计作为旁路
// 写入量小，逐条 Insert 已足够）。*store.AuditRepo 满足该接口；以接口而非具体类型依赖，
// 便于在单元测试中以内存 fake 替换并注入写入失败。
type BatchWriter interface {
	// Insert 写入一条审计日志并回填生成标识与发生时间。
	Insert(ctx context.Context, rec store.AuditRecord) (store.AuditRecord, error)
}

// Recorder 实现审计日志的异步写入与降级。
//
// 数据流：
//
//	主流程（HTTP handler / 鉴权中间件）
//	  → Record* 构造 store.AuditRecord（含发生时间戳）→ Submit 非阻塞入本地队列
//	后台 worker → 批量逐条 Insert 到 audit_log
//	  任一环节失败 → 静默丢弃该批，不影响主流程、不返回错误
//
// 设计要点：
//   - Submit 仅对本地缓冲队列做非阻塞写入，绝不进行 DB I/O，故主流程附加耗时极小且永不阻塞；
//     队列满时静默丢弃（审计是旁路，宁可丢记录也不能拖垮主请求）。
//   - Record* 方法在调用线程完成明细 JSON 序列化与时间戳标注，避免把可测的纯逻辑下沉到 worker；
//     worker 仅负责把已组装好的记录逐条落库。
//   - 未调用 Start（worker 未运行）时提交的记录暂存队列，Start 后由 worker 消费；
//     若进程在 Start 前退出，这些记录会丢失，属可接受的旁路降级。
//
// 与 audit.Service 的关系：Service 提供同步写（service.go）与查询/清理；Recorder 复用 Service
// 的明细序列化逻辑（buildRecord）实现异步写，二者共享同一仓储。
type Recorder struct {
	// repo 为审计日志批量落库仓储。
	repo BatchWriter
	// queue 为主流程与 worker 之间的本地非阻塞缓冲队列。
	queue chan store.AuditRecord
	// batchSize 为单批落库的记录条数上限。
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
			r.queue = make(chan store.AuditRecord, n)
		}
	}
}

// WithBatchSize 设置单批落库的记录条数上限（<=0 时回退默认值）。
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

// NewRecorder 构造异步审计写入器。repo 为必需依赖；选项可覆盖队列容量、批次大小、刷新周期与日志器。
//
// 构造不启动 worker（Start 由装配层在应用就绪后调用）。未 Start 时 Submit 仍可入队暂存。
func NewRecorder(repo BatchWriter, opts ...Option) *Recorder {
	r := &Recorder{
		repo:          repo,
		queue:         make(chan store.AuditRecord, defaultQueueSize),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		log:           slog.Default(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RecordLogin 构造一条登录审计记录并入队（detail 携带 success），返回 nil 表示已入队。
//
// 与 Service.RecordLogin 的区别：本方法异步落库、永不阻塞、永不向调用方报错（吞错在 Submit）。
// 调用方（登录 handler）据此旁路记录登录结果，不影响登录响应。
func (r *Recorder) RecordLogin(_ context.Context, username string, success bool) error {
	rec, err := buildRecord(store.AuditEventLogin, username, map[string]any{"success": success})
	if err != nil {
		return err
	}
	rec.OccurredAt = time.Now()
	r.Submit(rec)
	return nil
}

// RecordCreate 构造一条资源创建审计记录并入队。
func (r *Recorder) RecordCreate(_ context.Context, kind ResourceKind, target string) error {
	return r.recordChange(store.AuditEventCreate, kind, target)
}

// RecordUpdate 构造一条资源更新审计记录并入队。
//
// 语义上覆盖"更新"与近似的写操作（启停、重排序、重连、设置保存、限流配置更新等），
// 通过 detail.resource 区分资源类别，不新增事件类型枚举。
func (r *Recorder) RecordUpdate(_ context.Context, kind ResourceKind, target string) error {
	return r.recordChange(store.AuditEventUpdate, kind, target)
}

// RecordDelete 构造一条资源删除审计记录并入队。
func (r *Recorder) RecordDelete(_ context.Context, kind ResourceKind, target string) error {
	return r.recordChange(store.AuditEventDelete, kind, target)
}

// RecordAccessDenied 构造一条鉴权被拒审计记录并入队。
//
// target 通常为被尝试访问的请求路径；reason 为空时不写明细。
func (r *Recorder) RecordAccessDenied(_ context.Context, target, reason string) error {
	var detail map[string]any
	if reason != "" {
		detail = map[string]any{"reason": reason}
	}
	rec, err := buildRecord(store.AuditEventAccessDenied, target, detail)
	if err != nil {
		return err
	}
	rec.OccurredAt = time.Now()
	r.Submit(rec)
	return nil
}

// recordChange 构造一条资源增改删审计记录并入队（与 Service.recordChange 对应，但走异步路径）。
func (r *Recorder) recordChange(eventType string, kind ResourceKind, target string) error {
	rec, err := buildRecord(eventType, target, map[string]any{"resource": string(kind)})
	if err != nil {
		return err
	}
	rec.OccurredAt = time.Now()
	r.Submit(rec)
	return nil
}

// Submit 以非阻塞方式提交一条已组装的审计记录到本地队列。
//
//   - 仅对本地队列做非阻塞写入，不进行任何 DB I/O，主流程附加耗时极小且永不阻塞。
//   - 队列已满（落库侧滞后形成背压）时静默丢弃该记录，不阻塞、不报错，绝不影响主请求
//     结果返回（审计作为旁路，宁可丢记录也不能拖垮主请求）。
//
// 主要供测试或需要直接提交已组装记录的内部路径使用；业务侧通常调用 Record* 方法。
func (r *Recorder) Submit(rec store.AuditRecord) {
	if rec.OccurredAt.IsZero() {
		rec.OccurredAt = time.Now()
	}
	select {
	case r.queue <- rec:
	default:
		// 队列满：静默丢弃，绝不阻塞主流程。
		r.log.Warn("审计缓冲队列已满，丢弃审计记录",
			"eventType", rec.EventType, "target", rec.Target)
	}
}

// Start 启动后台落库 worker（幂等）。
//
// 传入的 ctx 应为应用生命周期级上下文：ctx 被取消或调用 Stop 均会停止 worker，
// 停止前会尽力将剩余记录收尾落库。未调用 Start 时提交的记录暂存队列，Start 后消费。
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
	r.log.Info("审计落库 worker 已启动", "batchSize", r.batchSize, "flushInterval", r.flushInterval)
	go r.run(runCtx)
}

// Stop 停止后台 worker 并等待其收尾退出（幂等）。
//
// 通过取消运行上下文实现：worker 响应取消后尽力把队列中剩余记录落库再退出。
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
	r.log.Info("审计落库 worker 已停止")
}

// Running 报告 worker 当前是否在运行（主要用于测试与状态查询）。
func (r *Recorder) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// run 是后台落库循环：累积待发记录 → 批量逐条落库，直至上下文取消。
//
// 触发批量的两种时机：待发批次达到 batchSize、或定时刷新周期到达。上下文取消时执行一次
// 收尾落库，尽力不丢失已入队记录（队列满被丢弃的记录除外，属正常背压降级）。
func (r *Recorder) run(ctx context.Context) {
	defer r.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			safego.LogRecovered(r.log, "审计落库 worker panic 已恢复", recovered)
			r.mu.Lock()
			r.started = false
			r.cancel = nil
			r.mu.Unlock()
		}
	}()

	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()

	pending := make([]store.AuditRecord, 0, r.batchSize)

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
		}
	}
}

// shutdown 在停止时执行收尾落库：非阻塞拉空队列后做一次完整刷新。
//
// 使用独立的限时上下文（与已取消的运行上下文解耦），保证收尾的 DB I/O 有机会完成。
func (r *Recorder) shutdown(pending []store.AuditRecord) {
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

// flush 逐条落库一批待发记录；任一条失败均静默丢弃该条、不向调用方报错（审计旁路降级）。
func (r *Recorder) flush(ctx context.Context, pending []store.AuditRecord) {
	if len(pending) == 0 {
		return
	}
	r.log.Debug("批量落库审计记录", "count", len(pending))
	for _, rec := range pending {
		if _, err := r.repo.Insert(ctx, rec); err != nil {
			// 落库失败：丢弃该条，不影响主流程（审计旁路）。
			r.log.Warn("写入审计记录失败，丢弃",
				"eventType", rec.EventType, "target", rec.Target, "error", err)
		}
	}
}
