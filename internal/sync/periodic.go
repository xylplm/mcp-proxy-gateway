package syncsvc

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
)

// UpstreamLister 是周期同步枚举待同步上游的窄接口（上游列表注入）。
//
// 它刻意保持最小：仅暴露「列出全部已配置上游 MCP 及其当前状态」这一项能力，
// 由周期同步器据此筛选「启用且开启自动同步」的上游（Req 7.1、7.2）与「已启用但
// 缺失缓存」的上游（Req 6.3）。连接管理器 *manager.Manager 的 List 方法满足该接口，
// 装配层（任务 27.2）负责注入，从而避免本包反向依赖 manager 包造成循环依赖。
type UpstreamLister interface {
	// List 返回全部已配置的上游 MCP 服务及其当前连接状态；无数据返回空切片。
	List(ctx context.Context) ([]domain.Upstream, error)
}

// PeriodicSyncer 实现工具自动同步的周期触发（Req 6.3、7.1、7.2、7.5、7.8）。
//
// 它复用与手动刷新相同的「拉取 → 整列表替换缓存」逻辑（见 refresh.go 的
// pullAndReplace 与 ToolFetcher），不重复实现拉取语义，区别仅在于触发来源：
//   - cron 调度到达触发点时，对「启用且开启自动同步」的上游各触发一次同步（Req 7.1）；
//     关闭自动同步的上游不参与周期性同步（Req 7.2）。
//   - 已启用上游缺失工具缓存（Get 返回 false）时触发一次拉取（Req 6.3）。
//
// 并发去重：用进程内 sync.Map 记录「正在同步」的上游标识；若某上游上一次同步尚未
// 完成而新的触发时间点到达，则跳过本次该上游的同步触发（Req 7.8）。
//
// 失败降级：拉取失败或超时（默认 30s，可配置 5-300s）时，pullAndReplace 绝不触碰
// 缓存，从而保留上一次成功的缓存工具列表；本同步器在此基础上记录包含失败原因的
// 同步失败事件（Req 7.5）。
type PeriodicSyncer struct {
	// fetcher 为上游工具列表拉取入口（与手动刷新共用同一窄接口）。
	fetcher ToolFetcher
	// cache 为工具缓存，成功同步以整列表替换写入（Req 6.1）。
	cache domain.Tool_Cache
	// lister 为待同步上游枚举入口（窄接口注入）。
	lister UpstreamLister
	// timeout 为单次同步拉取的超时上限（通常取 config 的 sync.timeout_s，
	// 默认 30s、可配置 5-300s）；<=0 表示不额外限制，语义与 Refresher 一致。
	timeout time.Duration
	// logger 用于记录同步成功/跳过/失败事件。
	logger *slog.Logger

	// inFlight 为并发去重标志：键为上游标识，存在即表示该上游正在同步（Req 7.8）。
	inFlight sync.Map
}

// NewPeriodicSyncer 构造周期同步器。
//
// fetcher、cache、lister 为必需依赖；timeout 为单次拉取超时上限（<=0 表示不额外
// 限制）；logger 为空时回退到 slog.Default()。
func NewPeriodicSyncer(fetcher ToolFetcher, cache domain.Tool_Cache, lister UpstreamLister, timeout time.Duration, logger *slog.Logger) *PeriodicSyncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &PeriodicSyncer{
		fetcher: fetcher,
		cache:   cache,
		lister:  lister,
		timeout: timeout,
		logger:  logger,
	}
}

// SyncOne 对单个上游 MCP 执行一次同步，并以 sync.Map 防止同一上游并发重入（Req 7.8）。
//
// 返回值 ran 表示本次是否实际执行了同步：当该上游的上一次同步尚未完成时，本次触发
// 被跳过，返回 (false, nil)；否则执行拉取并整列表替换缓存，返回 (true, err)。失败/超时
// 时 err 非空且缓存保持不变（保留旧缓存，Req 7.5）。
//
// 复用 refresh.go 的 pullAndReplace，确保「成功即整列表替换、失败保留旧缓存」语义与
// 手动刷新完全一致（Req 6.1、6.4、6.5、7.5）。
func (s *PeriodicSyncer) SyncOne(ctx context.Context, upstreamID string) (ran bool, err error) {
	if upstreamID == "" {
		return false, domain.NewValidationError("同步失败：上游标识无效", map[string]string{
			"upstreamID": "上游标识不能为空",
		})
	}

	// 并发去重：上一次同步尚未完成则跳过本次触发（Req 7.8）。
	if _, loaded := s.inFlight.LoadOrStore(upstreamID, struct{}{}); loaded {
		s.logger.Info("上游上一次同步尚未完成，跳过本次同步触发", "upstreamID", upstreamID)
		return false, nil
	}
	defer s.inFlight.Delete(upstreamID)

	// 拉取 → 成功整列表替换缓存；失败/超时不触碰缓存（保留旧缓存）。
	tools, err := pullAndReplace(ctx, s.fetcher, s.cache, upstreamID, s.timeout, s.logger)
	if err != nil {
		// 记录包含失败原因的同步失败事件（Req 7.5）。
		s.logger.Warn("周期同步失败，保留上一次成功的缓存工具列表",
			"upstreamID", upstreamID, "error", err)
		return true, err
	}
	s.logger.Info("周期同步成功，已整列表替换工具缓存",
		"upstreamID", upstreamID, "toolCount", len(tools))
	return true, nil
}

// SyncEnabledAutoSync 是 cron 调度的同步入口：枚举「启用且开启自动同步」的上游，
// 各触发一次同步（Req 7.1、7.2）。
//
// 该方法供调度器（Scheduler.UpdateSchedule 的 job 回调）调用，典型用法：
//
//	scheduler.UpdateSchedule(cron, func() { syncer.SyncEnabledAutoSync(ctx) })
//
// 仅对 Enabled 且 AutoSync 均为 true 的上游触发同步；关闭自动同步或已停用的上游
// 被跳过（Req 7.2）。各上游并发同步、互不阻塞，单个上游的并发重入由 SyncOne 的
// sync.Map 去重（Req 7.8）。本方法阻塞至本轮所有上游同步结束。
func (s *PeriodicSyncer) SyncEnabledAutoSync(ctx context.Context) {
	s.runUpstreams(ctx, func(up domain.Upstream) bool {
		return up.Config.Enabled && up.Config.AutoSync
	}, s.SyncOne)
}

// EnsureCached 在指定上游缺失工具缓存时触发一次拉取（Req 6.3）。
//
// 先读缓存：命中则无需拉取，返回 (false, nil)；未命中（Get 返回 false）则触发一次
// 同步拉取，返回 SyncOne 的结果。并发去重同样由 SyncOne 保证。
func (s *PeriodicSyncer) EnsureCached(ctx context.Context, upstreamID string) (ran bool, err error) {
	if upstreamID == "" {
		return false, domain.NewValidationError("同步失败：上游标识无效", map[string]string{
			"upstreamID": "上游标识不能为空",
		})
	}
	if _, _, found := s.cache.Get(ctx, upstreamID); found {
		return false, nil
	}
	s.logger.Info("已启用上游缺失工具缓存，触发一次拉取", "upstreamID", upstreamID)
	return s.SyncOne(ctx, upstreamID)
}

// EnsureCachedForEnabled 枚举所有「已启用」上游，对其中缺失工具缓存者各触发一次拉取
// （Req 6.3）。
//
// 与 SyncEnabledAutoSync 不同，本方法的筛选条件仅为 Enabled（不要求开启自动同步），
// 因为缓存缺失补拉是聚合可用性的兜底，与是否开启周期自动同步无关。各上游并发处理，
// 本方法阻塞至本轮处理结束。
func (s *PeriodicSyncer) EnsureCachedForEnabled(ctx context.Context) {
	s.runUpstreams(ctx, func(up domain.Upstream) bool {
		return up.Config.Enabled
	}, s.EnsureCached)
}

// runUpstreams 枚举上游、按 predicate 筛选，并对每个命中上游并发执行 action。
//
// 枚举失败时记录错误并返回（不触碰缓存）。action 返回的错误已在其内部记录失败事件，
// 此处不重复处理；本方法阻塞至本轮所有上游处理结束，便于调用方（含测试）确定性等待。
func (s *PeriodicSyncer) runUpstreams(ctx context.Context, predicate func(domain.Upstream) bool, action func(context.Context, string) (bool, error)) {
	ups, err := s.lister.List(ctx)
	if err != nil {
		s.logger.Error("枚举待同步上游失败，跳过本轮同步", "error", err)
		return
	}

	var wg sync.WaitGroup
	for i := range ups {
		up := ups[i]
		if !predicate(up) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					safego.LogRecovered(s.logger, "上游同步 worker panic 已恢复", recovered, "upstreamID", up.ID)
				}
			}()
			// action 内部已记录成功/跳过/失败事件，错误无需在此再处理。
			_, _ = action(ctx, up.ID)
		}()
	}
	wg.Wait()
}
