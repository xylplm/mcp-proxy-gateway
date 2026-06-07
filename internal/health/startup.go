package health

import (
	"context"
	"log/slog"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 22.1）实现 Health_Service 的「启动连通性探测与结构化日志」：
// 系统启动时按序探测 PostgreSQL、Redis、各启用上游 MCP、（若启用）小智接入点的
// 连通性，并用 log/slog 结构化记录每项结果与失败原因（Req 20.1-20.5）。
//
// 公开存活探针与经鉴权的详细健康端点属任务 22.2，故本文件仅聚焦启动探测，
// 标识符均以 Startup* 前缀命名以避免与 22.2 的端点实现冲突。
//
// 设计要点：各依赖的「探测能力」通过窄接口或可注入函数注入（Pinger、上游探测、
// 小智探测、上游列表），既使依赖关系一目了然，又便于单元测试以 mock 驱动，
// 而无需真实 PG/Redis/上游连接。探测结果汇总为 StartupReport 供装配层（任务 27.2）
// 据此决定后续启动流程。

// 探测组件类别名常量，用作结构化日志与汇总结果中的 Component 字段取值。
const (
	// ComponentPostgres 表示对 PostgreSQL 的连通性探测（Req 20.1）。
	ComponentPostgres = "postgres"
	// ComponentRedis 表示对 Redis 的连通性探测（Req 20.2）。
	ComponentRedis = "redis"
	// ComponentUpstream 表示对单个上游 MCP 服务的连通性探测（Req 20.3）。
	ComponentUpstream = "upstream"
	// ComponentXiaoZhi 表示对小智 MCP 接入点的连通性探测（Req 20.5）。
	ComponentXiaoZhi = "xiaozhi"
)

// ProbeStatus 表示单项连通性探测的结果状态。
type ProbeStatus string

const (
	// ProbeOK 表示该项依赖/连接探测成功。
	ProbeOK ProbeStatus = "ok"
	// ProbeFailed 表示该项依赖/连接探测失败（Req 20.4）。
	ProbeFailed ProbeStatus = "failed"
)

// Pinger 是依赖连通性探测的窄接口，提供 PostgreSQL 与 Redis 的探测能力（Req 20.1、20.2）。
//
// 装配层（任务 27.2）通常以基于 *pgxpool.Pool 与 *redis.Client 的实现满足该接口；
// 单元测试可注入 mock，无需真实连接。
type Pinger interface {
	// PingPG 探测 PostgreSQL 连通性，连通失败返回包含原因的错误。
	PingPG(ctx context.Context) error
	// PingRedis 探测 Redis 连通性，连通失败返回包含原因的错误。
	PingRedis(ctx context.Context) error
}

// UpstreamListerFunc 返回参与启动探测的上游 MCP 集合（通常为全部已配置上游，
// 探测时按 Enabled 过滤，仅探测启用的上游，Req 20.3）。
type UpstreamListerFunc func(ctx context.Context) ([]domain.Upstream, error)

// UpstreamProbeFunc 探测单个上游 MCP 的连通性，连通失败返回包含原因的错误（Req 20.3、20.4）。
type UpstreamProbeFunc func(ctx context.Context, up domain.Upstream) error

// XiaoZhiProbeFunc 探测小智 MCP 接入点的连通性，连通失败返回包含原因的错误（Req 20.5）。
type XiaoZhiProbeFunc func(ctx context.Context, endpoint string) error

// ConfigProvider 提供 YAML 常规配置快照，用于读取小智接入的启停与接入点地址。
//
// *config.Manager 满足该接口。
type ConfigProvider interface {
	Config() config.YAMLConfig
}

// ProbeResult 为单项连通性探测的结果。
type ProbeResult struct {
	// Component 为探测的组件类别（postgres/redis/upstream/xiaozhi）。
	Component string
	// Name 为该项的具体名称（依赖名、上游 MCP 名称或小智接入点地址）。
	Name string
	// Status 为探测结果状态。
	Status ProbeStatus
	// Reason 为失败原因；成功时为空（Req 20.4）。
	Reason string
	// Latency 为本次探测耗时。
	Latency time.Duration
}

// OK 报告该项探测是否成功。
func (r ProbeResult) OK() bool {
	return r.Status == ProbeOK
}

// StartupReport 为启动连通性探测的汇总结果，供调用方（装配层 27.2）据此决策。
type StartupReport struct {
	// Results 按探测顺序（PG → Redis → 各启用上游 → 小智）排列。
	Results []ProbeResult
}

// AllOK 报告是否所有探测项均成功。无任何探测项时返回 true。
func (r StartupReport) AllOK() bool {
	for _, res := range r.Results {
		if !res.OK() {
			return false
		}
	}
	return true
}

// Failures 返回所有失败的探测项。
func (r StartupReport) Failures() []ProbeResult {
	var failures []ProbeResult
	for _, res := range r.Results {
		if !res.OK() {
			failures = append(failures, res)
		}
	}
	return failures
}

// add 追加一项探测结果。
func (r *StartupReport) add(res ProbeResult) {
	r.Results = append(r.Results, res)
}

// Options 聚合 StartupProber 的可注入依赖。
//
// 各探测能力均可缺省：缺省 Pinger 时跳过 PG/Redis 探测；缺省上游列表或上游探测
// 时跳过上游探测；缺省 ConfigProvider 或小智探测时跳过小智探测。缺省项会在日志中
// 以告警体现，便于装配层发现接线遗漏。
type Options struct {
	// Pinger 提供 PG 与 Redis 的连通性探测能力。
	Pinger Pinger
	// ListUpstreams 返回参与探测的上游集合。
	ListUpstreams UpstreamListerFunc
	// ProbeUpstream 探测单个上游 MCP 的连通性。
	ProbeUpstream UpstreamProbeFunc
	// Config 提供小智接入的启停与接入点地址。
	Config ConfigProvider
	// ProbeXiaoZhi 探测小智接入点的连通性。
	ProbeXiaoZhi XiaoZhiProbeFunc
	// Logger 为结构化日志记录器；为空时回退到 slog.Default()。
	Logger *slog.Logger
}

// StartupProber 执行启动连通性探测并用 slog 结构化记录结果（Req 20.1-20.5）。
type StartupProber struct {
	pinger        Pinger
	listUpstreams UpstreamListerFunc
	probeUpstream UpstreamProbeFunc
	cfg           ConfigProvider
	probeXiaoZhi  XiaoZhiProbeFunc
	logger        *slog.Logger
	// now 用于度量探测耗时；以字段形式持有便于测试替换。
	now func() time.Time
}

// NewStartupProber 构造启动连通性探测器。
func NewStartupProber(opts Options) *StartupProber {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &StartupProber{
		pinger:        opts.Pinger,
		listUpstreams: opts.ListUpstreams,
		probeUpstream: opts.ProbeUpstream,
		cfg:           opts.Config,
		probeXiaoZhi:  opts.ProbeXiaoZhi,
		logger:        logger,
		now:           time.Now,
	}
}

// ProbeStartup 按序探测 PG、Redis、各启用上游 MCP 与（若启用）小智接入点，
// 用 slog 结构化记录每项结果（成功/失败 + 失败原因），并返回汇总结果（Req 20.1-20.5）。
//
// 探测严格按序进行：先 PostgreSQL、再 Redis、随后按上游列表顺序探测各启用上游，
// 最后在小智接入启用时探测小智接入点。任一项失败仅记录该项失败原因并继续后续探测，
// 不中断整体流程；是否因失败终止启动由调用方依据返回的 StartupReport 决定。
func (p *StartupProber) ProbeStartup(ctx context.Context) StartupReport {
	var report StartupReport

	// 1) PostgreSQL 与 Redis 连通性（Req 20.1、20.2、20.4）。
	if p.pinger != nil {
		report.add(p.probeOne(ctx, ComponentPostgres, "PostgreSQL", func(ctx context.Context) error {
			return p.pinger.PingPG(ctx)
		}))
		report.add(p.probeOne(ctx, ComponentRedis, "Redis", func(ctx context.Context) error {
			return p.pinger.PingRedis(ctx)
		}))
	} else {
		p.logger.Warn("未注入依赖探测能力（Pinger），跳过 PostgreSQL 与 Redis 连通性探测")
	}

	// 2) 各启用上游 MCP 连通性（Req 20.3、20.4）。
	p.probeUpstreams(ctx, &report)

	// 3) 小智接入点连通性（仅在启用时探测，Req 20.5）。
	p.probeXiaoZhiEndpoint(ctx, &report)

	return report
}

// probeUpstreams 按上游列表顺序探测各启用上游的连通性，结果追加至 report。
//
// 仅探测 Enabled 为 true 的上游（Req 20.3）；获取上游列表失败时记录错误并跳过上游探测。
func (p *StartupProber) probeUpstreams(ctx context.Context, report *StartupReport) {
	if p.listUpstreams == nil || p.probeUpstream == nil {
		p.logger.Warn("未注入上游探测能力，跳过上游 MCP 连通性探测")
		return
	}

	ups, err := p.listUpstreams(ctx)
	if err != nil {
		p.logger.Error("获取上游 MCP 列表失败，跳过上游连通性探测", "error", err.Error())
		return
	}

	for _, up := range ups {
		if !up.Config.Enabled {
			continue
		}
		up := up // 捕获循环变量副本，供闭包安全引用。
		report.add(p.probeOne(ctx, ComponentUpstream, up.Config.Name, func(ctx context.Context) error {
			return p.probeUpstream(ctx, up)
		}))
	}
}

// probeXiaoZhiEndpoint 在小智接入启用时探测其接入点连通性，结果追加至 report（Req 20.5）。
func (p *StartupProber) probeXiaoZhiEndpoint(ctx context.Context, report *StartupReport) {
	if p.cfg == nil {
		return
	}
	xz := p.cfg.Config().XiaoZhi
	if !xz.Enabled {
		// 小智接入未启用：无需探测，记录一条信息便于运维确认（Req 20.5）。
		p.logger.Info("小智接入未启用，跳过小智 MCP 接入点连通性探测")
		return
	}
	if p.probeXiaoZhi == nil {
		p.logger.Warn("小智接入已启用但未注入探测能力，跳过小智 MCP 接入点连通性探测",
			"endpoint", xz.Endpoint)
		return
	}

	endpoint := xz.Endpoint
	report.add(p.probeOne(ctx, ComponentXiaoZhi, endpoint, func(ctx context.Context) error {
		return p.probeXiaoZhi(ctx, endpoint)
	}))
}

// probeOne 执行单项探测：度量耗时、用 slog 结构化记录成功/失败（含失败原因），
// 并返回对应的 ProbeResult（Req 20.4）。
func (p *StartupProber) probeOne(ctx context.Context, component, name string, fn func(context.Context) error) ProbeResult {
	start := p.now()
	err := fn(ctx)
	latency := p.now().Sub(start)

	res := ProbeResult{Component: component, Name: name, Latency: latency}
	if err != nil {
		res.Status = ProbeFailed
		res.Reason = err.Error()
		p.logger.Error("启动连通性探测失败",
			"component", component,
			"name", name,
			"status", string(ProbeFailed),
			"latencyMs", latency.Milliseconds(),
			"error", res.Reason,
		)
		return res
	}

	res.Status = ProbeOK
	p.logger.Info("启动连通性探测成功",
		"component", component,
		"name", name,
		"status", string(ProbeOK),
		"latencyMs", latency.Milliseconds(),
	)
	return res
}
