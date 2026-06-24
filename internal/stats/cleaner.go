package stats

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
)

// 统计保留期约束：默认 90 天，可配置范围 1 至 3650 天（Req 16.10）。
const (
	// minStatRetentionDays 为统计保留天数下界。
	minStatRetentionDays = 1
	// maxStatRetentionDays 为统计保留天数上界（约 10 年）。
	maxStatRetentionDays = 3650
	// defaultStatRetentionDays 为统计保留天数默认值。
	defaultStatRetentionDays = 90
)

// 清理任务调度默认参数。
const (
	// defaultCleanInterval 为保留期清理任务的默认执行周期。
	//
	// 调用统计已改为每日聚合事实表，数据粒度为 UTC 日期；每日清理足以及时回收超期聚合行。
	defaultCleanInterval = 24 * time.Hour
)

// DailyAggregateMaintainer 是统计保留期清理依赖的仓储窄接口（Req 16.10）。
//
// 调用明细表与月分区已移除，清理仅需删除早于保留边界的每日聚合行。
type DailyAggregateMaintainer interface {
	// DeleteOlderThan 删除早于 cutoff 所在 UTC 日期的每日聚合统计，返回删除行数。
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// CleanerConfigProvider 是清理任务读取保留期配置的窄接口。
//
// 仅声明本组件实际使用的方法：读取当前 YAML 配置快照（以获取 statistics.retention_days）。
// *config.Manager 满足该接口；以接口依赖便于在单元测试中注入固定保留期。
type CleanerConfigProvider interface {
	// Config 返回当前 YAML 常规配置的快照副本。
	Config() config.YAMLConfig
}

// Cleaner 实现统计保留期清理（Req 16.10）：按配置保留期定时回收 call_stat_daily 超期数据。
//
// 清理策略：定时触发后按 cutoff = now - retention_days 删除早于 cutoff 所在 UTC 日期的
// 每日聚合行。调用明细表与月分区已移除，清理逻辑不再维护分区。
//
// 设计要点：
//   - 保留期取自配置 statistics.retention_days，越界或缺失时回退默认 90 天，使清理边界
//     始终落在合法范围（Req 16.10）。
//   - 清理任务独立于统计写入主流程，其失败仅记录日志、不影响写入与查询。
type Cleaner struct {
	// repo 为每日聚合统计维护仓储。
	repo DailyAggregateMaintainer
	// cfg 为配置存储，提供统计保留期配置。
	cfg CleanerConfigProvider
	// interval 为清理任务执行周期。
	interval time.Duration
	// now 返回当前时间，便于在测试中注入可控时钟。
	now func() time.Time
	// log 为结构化日志器，记录清理结果与失败，便于观测。
	log *slog.Logger

	// mu 保护启停相关的可变状态（started/cancel）。
	mu sync.Mutex
	// started 标记清理循环是否在运行，保证 Start/Stop 幂等。
	started bool
	// cancel 取消清理循环运行上下文：Stop 调用它以停止循环。
	cancel context.CancelFunc
	// wg 等待清理循环退出，使 Stop 返回时循环确已收束。
	wg sync.WaitGroup
}

// CleanerOption 为 Cleaner 的可选配置项（函数式选项）。
type CleanerOption func(*Cleaner)

// WithCleanInterval 设置清理任务执行周期（<=0 时回退默认值）。
func WithCleanInterval(d time.Duration) CleanerOption {
	return func(c *Cleaner) {
		if d > 0 {
			c.interval = d
		}
	}
}

// WithCleanerClock 注入自定义时钟（为空时回退到 time.Now）。
func WithCleanerClock(now func() time.Time) CleanerOption {
	return func(c *Cleaner) {
		if now != nil {
			c.now = now
		}
	}
}

// WithCleanerLogger 注入自定义日志器（为空时回退到 slog.Default()）。
func WithCleanerLogger(l *slog.Logger) CleanerOption {
	return func(c *Cleaner) {
		if l != nil {
			c.log = l
		}
	}
}

// NewCleaner 构造统计保留期清理器。
//
// repo 为每日聚合维护仓储（必需），cfg 为配置存储（必需，提供保留期）。选项可覆盖执行周期、
// 时钟与日志器。
func NewCleaner(repo DailyAggregateMaintainer, cfg CleanerConfigProvider, opts ...CleanerOption) *Cleaner {
	c := &Cleaner{
		repo:     repo,
		cfg:      cfg,
		interval: defaultCleanInterval,
		now:      time.Now,
		log:      slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Cleanup 执行一次每日聚合统计保留期清理（Req 16.10）。
//
// 返回删除的聚合行数。调用方（定时循环）据此记录日志，但不应因清理失败而中断服务。
func (c *Cleaner) Cleanup(ctx context.Context) (deletedRows int64, err error) {
	now := c.now().UTC()
	cutoff := now.AddDate(0, 0, -c.retentionDays())
	return c.repo.DeleteOlderThan(ctx, cutoff)
}

// Start 启动后台定时清理循环（幂等）。
//
// 传入的 ctx 应为应用生命周期级上下文：ctx 被取消或调用 Stop 均会停止循环。启动后
// 立即执行一次清理（使重启后能及时回收超期数据），随后按 interval 周期执行。
func (c *Cleaner) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.started = true
	c.wg.Add(1)
	go c.run(runCtx)
}

// Stop 停止后台清理循环并等待其退出（幂等）。未启动时为无操作。
func (c *Cleaner) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	c.started = false
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

// Running 报告清理循环当前是否在运行（主要用于测试与状态查询）。
func (c *Cleaner) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// run 是后台清理循环：启动即清理一次，随后每 interval 执行一次，直至上下文取消（Req 16.10）。
func (c *Cleaner) run(ctx context.Context) {
	defer c.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			safego.LogRecovered(c.log, "统计保留期清理 worker panic 已恢复", recovered)
			c.mu.Lock()
			c.started = false
			c.cancel = nil
			c.mu.Unlock()
		}
	}()

	c.runOnce(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

// runOnce 执行单次清理并记录结果；失败仅记日志、不中断循环（Req 16.10）。
func (c *Cleaner) runOnce(ctx context.Context) {
	deleted, err := c.Cleanup(ctx)
	if err != nil {
		c.log.Warn("统计保留期清理失败", "error", err)
		return
	}
	if deleted > 0 {
		c.log.Info("统计保留期清理完成",
			"deletedRows", deleted, "retentionDays", c.retentionDays())
	}
}

// retentionDays 返回生效的统计保留天数，对越界或未配置的值回退为默认 90 天（Req 16.10）。
func (c *Cleaner) retentionDays() int {
	d := c.cfg.Config().Statistics.RetentionDays
	if d < minStatRetentionDays || d > maxStatRetentionDays {
		return defaultStatRetentionDays
	}
	return d
}
