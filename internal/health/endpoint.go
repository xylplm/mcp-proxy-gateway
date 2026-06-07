package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 22.2）实现 Health_Service 的两个对外健康端点：
//
//   - 公开存活探针 /healthz：无需鉴权，仅返回服务自身存活状态（{status:"ok"}），
//     不泄露任何依赖与上游 MCP 连接明细（Req 20.6）。
//   - 详细健康端点 /api/admin/health：经管理员 JWT 鉴权后返回系统整体状态及各依赖
//     （PostgreSQL/Redis）与各上游 MCP / 小智接入点的当前健康状态明细（Req 20.7）；
//     未通过鉴权由注入的管理员鉴权中间件拦截并返回 401 UNAUTHORIZED（Req 20.8）。
//
// 设计要点：依赖能力沿用任务 22.1 的窄接口与可注入函数风格——PG/Redis 探测复用 Pinger，
// 上游集合及其连接状态复用 UpstreamListerFunc（domain.Upstream 已携带 State/LastError，
// 由 MCP_Manager.List 实时填充，Req 5.4），小智状态经 XiaoZhiStatusProvider 注入。各能力
// 均可缺省：缺省项在响应中相应略去，便于装配层（任务 27.2）按实际接线渐进启用，亦便于单测。
//
// 鉴权解耦：本包不直接 import auth 包，而是把管理员鉴权中间件以 gin.HandlerFunc 注入
// （Register 的 adminAuth 参数），既满足 /api/admin/health 的 401 语义（Req 20.8），又避免
// 健康检查与具体鉴权实现耦合。

// 健康状态字符串常量，用于 /healthz 与 /api/admin/health 响应体。
const (
	// StatusOK 表示存活或整体健康。
	StatusOK = "ok"
	// StatusDegraded 表示存在至少一项依赖/连接不健康，系统处于降级状态。
	StatusDegraded = "degraded"
	// StatusFailed 表示单项依赖探测失败。
	StatusFailed = "failed"
)

// defaultDependencyProbeTimeout 为详细健康端点单项依赖探测的默认超时，避免某一依赖
// 长时间无响应拖垮整个健康查询。
const defaultDependencyProbeTimeout = 3 * time.Second

// XiaoZhiStatusProvider 是查询小智接入当前状态的窄接口（Req 20.7）。
//
// *xiaozhi.Connector 天然满足该接口（其 Enabled/Endpoint/Running 方法），由装配层
// （任务 27.2）注入；为空时详细健康响应略去小智段。
type XiaoZhiStatusProvider interface {
	// Enabled 报告是否启用小智接入。
	Enabled() bool
	// Endpoint 返回当前生效的小智接入点地址。
	Endpoint() string
	// Running 报告与小智接入点的连接运行循环是否在运行（即当前是否处于已连接/重连中）。
	Running() bool
}

// DependencyHealth 为单项依赖（PostgreSQL/Redis）的当前健康状态。
type DependencyHealth struct {
	// Name 为依赖名称（PostgreSQL/Redis）。
	Name string `json:"name"`
	// Status 为探测结果（ok/failed）。
	Status string `json:"status"`
	// Reason 为探测失败原因；成功时为空。
	Reason string `json:"reason,omitempty"`
}

// UpstreamHealth 为单个上游 MCP 连接的当前健康状态明细。
type UpstreamHealth struct {
	// ID 为上游 MCP 唯一标识。
	ID string `json:"id"`
	// Name 为上游 MCP 名称。
	Name string `json:"name"`
	// Enabled 表示该上游是否启用。
	Enabled bool `json:"enabled"`
	// State 为当前连接生命周期状态（connecting/available/unavailable/suspended）。
	State domain.ConnState `json:"state"`
	// LastError 为最近一次连接失败原因（可用时为空）。
	LastError string `json:"lastError,omitempty"`
}

// XiaoZhiHealth 为小智接入点的当前健康状态明细。
type XiaoZhiHealth struct {
	// Enabled 表示是否启用小智接入。
	Enabled bool `json:"enabled"`
	// Endpoint 为小智 MCP 接入点地址。
	Endpoint string `json:"endpoint,omitempty"`
	// Connected 表示当前是否处于已连接（运行循环在运行）状态。
	Connected bool `json:"connected"`
}

// DetailReport 为详细健康端点的响应体：系统整体状态加各依赖与连接明细（Req 20.7）。
type DetailReport struct {
	// Status 为系统整体状态（ok/degraded）。
	Status string `json:"status"`
	// Dependencies 为各端依赖（PostgreSQL/Redis）的健康明细。
	Dependencies []DependencyHealth `json:"dependencies"`
	// Upstreams 为各上游 MCP 连接的健康明细。
	Upstreams []UpstreamHealth `json:"upstreams"`
	// XiaoZhi 为小智接入点健康明细；未注入小智状态时为 nil。
	XiaoZhi *XiaoZhiHealth `json:"xiaozhi,omitempty"`
}

// LivenessHandler 返回公开存活探针 /healthz 的处理器（Req 20.6）。
//
// 仅返回 {status:"ok"}，不查询任何依赖、不泄露上游/小智连接明细，使其可被探活探针高频调用
// 而无副作用。
func LivenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": StatusOK})
	}
}

// DetailReporterOptions 聚合详细健康端点的可注入依赖。
//
// 各能力均可缺省：缺省 Pinger 时响应不含 PG/Redis 段；缺省 ListUpstreams 时响应上游段为空；
// 缺省 XiaoZhi 时响应略去小智段。
type DetailReporterOptions struct {
	// Pinger 提供 PG 与 Redis 的连通性探测能力。
	Pinger Pinger
	// ListUpstreams 返回上游集合（含各自的连接状态与最近失败原因）。
	ListUpstreams UpstreamListerFunc
	// XiaoZhi 提供小智接入的当前状态。
	XiaoZhi XiaoZhiStatusProvider
	// ProbeTimeout 为单项依赖探测超时；<=0 时取默认值。
	ProbeTimeout time.Duration
}

// DetailReporter 汇总系统各依赖与连接的当前健康状态，驱动 /api/admin/health（Req 20.7）。
type DetailReporter struct {
	pinger        Pinger
	listUpstreams UpstreamListerFunc
	xiaozhi       XiaoZhiStatusProvider
	probeTimeout  time.Duration
}

// NewDetailReporter 构造详细健康汇总器。
func NewDetailReporter(opts DetailReporterOptions) *DetailReporter {
	timeout := opts.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultDependencyProbeTimeout
	}
	return &DetailReporter{
		pinger:        opts.Pinger,
		listUpstreams: opts.ListUpstreams,
		xiaozhi:       opts.XiaoZhi,
		probeTimeout:  timeout,
	}
}

// Report 即时探测并汇总系统整体状态及各依赖与连接明细（Req 20.7）。
//
// 整体状态判定：任一已探测依赖失败、或任一启用上游不可用（非 available）、或小智启用但未连接，
// 即判为 degraded；否则为 ok。无任何探测项时为 ok。
func (r *DetailReporter) Report(ctx context.Context) DetailReport {
	report := DetailReport{
		Status:       StatusOK,
		Dependencies: []DependencyHealth{},
		Upstreams:    []UpstreamHealth{},
	}
	degraded := false

	// 1) PG 与 Redis 连通性（Req 20.7）。
	if r.pinger != nil {
		pg := r.probeDependency(ctx, "PostgreSQL", r.pinger.PingPG)
		redis := r.probeDependency(ctx, "Redis", r.pinger.PingRedis)
		report.Dependencies = append(report.Dependencies, pg, redis)
		if pg.Status != StatusOK || redis.Status != StatusOK {
			degraded = true
		}
	}

	// 2) 各上游 MCP 连接明细（Req 20.7）。连接状态由 MCP_Manager 实时维护（Req 5.4）。
	if r.listUpstreams != nil {
		ups, err := r.listUpstreams(ctx)
		if err != nil {
			// 上游列表查询失败：整体降级，并以一条 failed 依赖项记录原因，便于运维定位。
			degraded = true
			report.Dependencies = append(report.Dependencies, DependencyHealth{
				Name:   "upstream-list",
				Status: StatusFailed,
				Reason: err.Error(),
			})
		} else {
			for _, up := range ups {
				report.Upstreams = append(report.Upstreams, UpstreamHealth{
					ID:        up.ID,
					Name:      up.Config.Name,
					Enabled:   up.Config.Enabled,
					State:     up.State,
					LastError: up.LastError,
				})
				if up.Config.Enabled && up.State != domain.ConnAvailable {
					degraded = true
				}
			}
		}
	}

	// 3) 小智接入点状态（Req 20.7）。
	if r.xiaozhi != nil {
		xz := XiaoZhiHealth{
			Enabled:   r.xiaozhi.Enabled(),
			Endpoint:  r.xiaozhi.Endpoint(),
			Connected: r.xiaozhi.Running(),
		}
		report.XiaoZhi = &xz
		if xz.Enabled && !xz.Connected {
			degraded = true
		}
	}

	if degraded {
		report.Status = StatusDegraded
	}
	return report
}

// probeDependency 在受限超时内探测单项依赖连通性并归一为 DependencyHealth。
func (r *DetailReporter) probeDependency(ctx context.Context, name string, ping func(context.Context) error) DependencyHealth {
	probeCtx, cancel := context.WithTimeout(ctx, r.probeTimeout)
	defer cancel()

	if err := ping(probeCtx); err != nil {
		return DependencyHealth{Name: name, Status: StatusFailed, Reason: err.Error()}
	}
	return DependencyHealth{Name: name, Status: StatusOK}
}

// Handler 返回详细健康端点 /api/admin/health 的处理器（Req 20.7）。
//
// 该处理器本身不做鉴权——鉴权由注册时置于其前的管理员中间件负责（见 Register，Req 20.8）。
func (r *DetailReporter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, r.Report(c.Request.Context()))
	}
}

// Register 在给定路由器上注册两个健康端点（Req 20.6、20.7、20.8）：
//
//   - GET /healthz：公开存活探针，无鉴权中间件（Req 20.6）。
//   - GET /api/admin/health：详细健康端点，前置 adminAuth 管理员鉴权中间件；未通过鉴权由该
//     中间件返回 401 UNAUTHORIZED（Req 20.8）。
//
// adminAuth 为管理员 JWT 鉴权中间件（通常为 auth.RequireAdmin 的返回值），以参数注入以避免本包
// 与具体鉴权实现耦合；为 nil 时该详细端点将不可达（不注册），以免误将其暴露为无保护。
// reporter 为 nil 时不注册详细端点。
func Register(router gin.IRouter, adminAuth gin.HandlerFunc, reporter *DetailReporter) {
	router.GET("/healthz", LivenessHandler())

	if reporter == nil || adminAuth == nil {
		return
	}
	router.GET("/api/admin/health", adminAuth, reporter.Handler())
}
