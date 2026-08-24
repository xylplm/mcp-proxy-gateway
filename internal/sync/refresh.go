package syncsvc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ToolFetcher 是从指定上游 MCP 拉取当前工具列表的窄接口（拉取入口）。
//
// 它刻意保持最小：仅暴露「按上游标识拉取工具列表」这一项能力，屏蔽底层会话获取、
// 连接选择与传输细节。周期同步（任务 10.3）与手动刷新（本任务 10.5）共用同一拉取
// 语义，二者应实现/复用同一 ToolFetcher 抽象，避免重复实现拉取逻辑。
//
// 注：若任务 10.3 已暴露等价的拉取函数/方法，应改为直接复用之；在其尚不可见时，
// 本接口作为最小可复用的拉取入口，与 design.md「同步调度」一节描述的
// session(upstreamID).ListTools() 语义保持一致。
type ToolFetcher interface {
	// FetchTools 拉取指定上游 MCP 的完整工具列表。
	//
	// 拉取失败（连接不可用、超时、协议错误等）返回错误，由调用方决定降级策略。
	FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error)
}

// ToolCatalogObserver 在缓存成功替换后接收风险目录对账通知。
// 实现错误只记录，不影响同步成功结果。
type ToolCatalogObserver interface {
	ToolsReplaced(ctx context.Context, upstreamID string, tools []domain.ToolDef) error
}

// Refresher 实现手动刷新某上游 MCP 工具列表的能力（Req 6.4、6.5）。
//
// 它复用与周期同步相同的「拉取 → 整列表替换缓存」逻辑（见 pullAndReplace）：
//   - 拉取成功：立即以最新工具列表整列表替换缓存，不等待下一个自动同步周期（Req 6.4）；
//   - 拉取或写入失败：保留该上游最近一次成功缓存的工具列表，并返回指示刷新失败的
//     错误（Req 6.5）。
type Refresher struct {
	fetcher  ToolFetcher
	cache    domain.Tool_Cache
	timeout  time.Duration
	logger   *slog.Logger
	observer ToolCatalogObserver
}

func (r *Refresher) SetObserver(observer ToolCatalogObserver) *Refresher {
	r.observer = observer
	return r
}

// NewRefresher 构造手动刷新器。
//
// timeout 为单次拉取的超时上限（通常取 config 的 sync.timeout_s），<=0 表示不额外
// 限制；logger 为空时回退到 slog.Default()。
func NewRefresher(fetcher ToolFetcher, cache domain.Tool_Cache, timeout time.Duration, logger *slog.Logger) *Refresher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Refresher{fetcher: fetcher, cache: cache, timeout: timeout, logger: logger}
}

// Refresh 手动刷新指定上游 MCP 的工具列表（Req 6.4、6.5）。
//
// 走与周期同步相同的拉取逻辑：成功则立即整列表替换缓存并返回最新列表；失败则保留
// 旧缓存（绝不写入）并返回刷新失败错误。
func (r *Refresher) Refresh(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	tools, err := pullAndReplace(ctx, r.fetcher, r.cache, upstreamID, r.timeout, r.logger)
	if err == nil {
		notifyObserver(ctx, r.observer, upstreamID, tools, r.logger)
	}
	return tools, err
}

func notifyObserver(ctx context.Context, observer ToolCatalogObserver, upstreamID string, tools []domain.ToolDef, logger *slog.Logger) {
	if observer == nil {
		return
	}
	snapshot := append([]domain.ToolDef(nil), tools...)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("风险目录对账回调 panic 已恢复", "upstreamID", upstreamID, "panic", recovered)
			}
		}()
		if err := observer.ToolsReplaced(context.WithoutCancel(ctx), upstreamID, snapshot); err != nil {
			logger.Warn("风险目录异步对账失败，不影响工具同步", "upstreamID", upstreamID, "error", err)
		}
	}()
}

// pullAndReplace 是周期同步与手动刷新共用的最小拉取入口：从上游拉取工具列表，
// 成功则整列表替换缓存，失败则保留旧缓存并返回错误。
//
// 该函数集中实现「成功即整列表替换、失败保留旧缓存」语义（Req 6.1、6.4、6.5），
// 供手动刷新（任务 10.5）直接调用；周期同步（任务 10.3）亦可复用，避免重复实现。
func pullAndReplace(ctx context.Context, fetcher ToolFetcher, cache domain.Tool_Cache, upstreamID string, timeout time.Duration, logger *slog.Logger) ([]domain.ToolDef, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if fetcher == nil || cache == nil {
		return nil, domain.NewError(domain.CodeValidation, "刷新器未正确初始化")
	}
	if upstreamID == "" {
		return nil, domain.NewValidationError("刷新失败：上游标识无效", map[string]string{
			"upstreamID": "上游标识不能为空",
		})
	}

	// 应用拉取超时（若配置）。超时通过 context 传递，底层会话据此中止拉取。
	pullCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		pullCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 1) 拉取：失败则保留旧缓存（不触碰 cache）并返回刷新失败错误（Req 6.5）。
	tools, err := fetcher.FetchTools(pullCtx, upstreamID)
	if err != nil {
		logger.Warn("手动刷新拉取上游工具列表失败，保留旧缓存", "upstreamID", upstreamID, "error", err)
		return nil, refreshFailure(pullCtx, err)
	}

	// 2) 整列表替换缓存：成功则不等下一周期立即生效（Req 6.4、6.1）。
	//    注意此处用原始 ctx 而非 pullCtx：拉取已完成，写缓存不应受拉取超时影响。
	if err := cache.Replace(ctx, upstreamID, tools); err != nil {
		logger.Error("手动刷新写入工具缓存失败，保留旧缓存", "upstreamID", upstreamID, "error", err)
		return nil, refreshFailure(ctx, err)
	}
	return tools, nil
}

// refreshFailure 将底层拉取/写入错误包装为指示刷新失败的统一错误（Req 6.5）。
//
// 尽量保留底层语义：
//   - 若拉取因超时被取消，归类为 UPSTREAM_TIMEOUT；
//   - 若底层已是 *domain.APIError，沿用其错误码；
//   - 否则归类为 UPSTREAM_UNAVAILABLE。
func refreshFailure(ctx context.Context, cause error) error {
	code := domain.CodeUpstreamUnavailable
	switch {
	case errors.Is(cause, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)):
		code = domain.CodeUpstreamTimeout
	default:
		if apiErr, ok := errors.AsType[*domain.APIError](cause); ok {
			code = apiErr.Code
		}
	}
	return &domain.APIError{
		Code:    code,
		Message: "手动刷新工具列表失败：" + cause.Error(),
	}
}
