package xiaozhi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 21.1）实现小智接入服务（XiaoZhi_Connector）的核心：连接生命周期管理、
// 工具列表暴露与调用路由（Req 15.1、15.2、15.3、15.5）。
//
// 角色与方向：小智接入是一种「反向」的服务面——网关作为出站 WebSocket 客户端主动连出
// 到小智 MCP 接入点，但在该连接之上以 MCP「服务端」身份对话：小智（MCP 客户端）请求
// 工具列表与调用工具，网关把这些请求转交给聚合服务（Aggregation_Service）处理后原样回应。
//
// 可测试性设计（依赖倒置 + 窄接口）：
//   - 连接的建立与驱动抽象为 EndpointConnector 窄接口，生产实现为基于 coder/websocket 的
//     wsConnector（见 endpoint.go），测试可注入可控的 mock 接入点，无需真实网络。
//   - 工具暴露与调用路由抽象为 EndpointHandler，由 aggregationBridge 复用聚合服务实现：
//     小智接入没有 API Key 概念，故统一以空 apiKeyID 取「当前可见聚合集合」（Req 15.2）。
//   - 断线重连（指数退避）属任务 21.2，本任务仅定义 Reconnector 占位接口与「不重连」默认实现，
//     聚焦连接/暴露/路由；停用时通过取消上下文关闭连接并阻止后续重连（Req 15.5）。

// EndpointHandler 是小智接入点 MCP 请求到聚合能力的桥接窄接口。
//
// 它把接入点的两类请求映射为对聚合服务的操作：列出当前可见工具集合、按对外名转发调用。
type EndpointHandler interface {
	// ListTools 返回聚合服务输出的当前可见工具集合（已应用屏蔽与别名/描述重写，Req 15.2）。
	ListTools(ctx context.Context) ([]domain.ToolDef, error)
	// CallTool 将一次调用路由到聚合服务并原样返回成功/错误结果（Req 15.3）。
	CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error)
}

// EndpointConnector 抽象「连接到小智接入点并在其上驱动一个 MCP 会话」的能力（窄接口）。
//
// Serve 应当：建立到 endpoint 的连接 → 在该连接上以 MCP 服务端身份服务 handler（暴露工具、
// 路由调用）→ 阻塞直至连接断开或 ctx 被取消，然后返回。返回 nil 表示连接正常结束，返回非
// nil 表示连接建立失败或运行期异常断开。Serve 必须响应 ctx 取消以关闭连接（支撑 Req 15.5）。
type EndpointConnector interface {
	// Serve 连接到 endpoint 并服务 handler，阻塞至断开或 ctx 取消。
	Serve(ctx context.Context, endpoint string, handler EndpointHandler) error
}

// Reconnector 是断线重连调度的接口（Req 15.4 的指数退避实现见 backoff.go）。
//
// 连接结束后，run 循环据 NextDelay 决定是否及何时重连。生产默认实现为 backoffReconnector
// （指数退避，Req 15.4）；noReconnect 为「从不重连」的实现，主要用于测试。
type Reconnector interface {
	// NextDelay 返回下一次重连前的等待时长，以及是否应当重连。
	NextDelay() (time.Duration, bool)
}

// noReconnect 是「从不重连」的 Reconnector：连接结束即停止。主要用于测试与停用语义验证。
type noReconnect struct{}

// NextDelay 始终返回不重连。
func (noReconnect) NextDelay() (time.Duration, bool) { return 0, false }

// aggregationBridge 以聚合服务实现 EndpointHandler。
//
// 小智接入无 API Key，故 BuildToolSet/InvokeTool 均以空 apiKeyID 调用：得到的是全局可见
// 聚合集合（已过 MCP 级屏蔽与别名/描述重写，但不含 API Key 级过滤），与 Req 15.2 一致。
type aggregationBridge struct {
	agg domain.Aggregation_Service
}

// newAggregationBridge 构造聚合桥接器。
func newAggregationBridge(agg domain.Aggregation_Service) *aggregationBridge {
	return &aggregationBridge{agg: agg}
}

// 编译期断言：aggregationBridge 满足 EndpointHandler 契约。
var _ EndpointHandler = (*aggregationBridge)(nil)

// ListTools 返回当前可见聚合工具集合（Req 15.2）。
func (b *aggregationBridge) ListTools(ctx context.Context) ([]domain.ToolDef, error) {
	return b.agg.BuildToolSet(ctx, "")
}

// CallTool 把调用路由到聚合服务并原样返回结果（Req 15.3）。
//
// 可见性校验、别名反向映射与上游路由均由聚合服务负责；不可见工具会被聚合服务以
// TOOL_NOT_FOUND 拒绝，本桥接器原样上抛。
func (b *aggregationBridge) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	return b.agg.InvokeTool(ctx, "", name, args)
}

// Connector 是小智接入服务（XiaoZhi_Connector）。
//
// 它持有接入点配置与连接编排，负责：启用时连接（Start）、停用时关闭连接并停止重连（Stop）、
// 以及在连接之上将工具列表请求与调用请求路由到聚合服务（经注入的 EndpointConnector 与
// EndpointHandler）。
type Connector struct {
	// mu 保护启停相关的可变状态（cancel/started）。
	mu sync.Mutex

	// endpoint 为小智 MCP 接入点地址（ws:// 或 wss://）。地址格式校验见 validate.go（Req 15.6）。
	endpoint string
	// enabled 表示是否启用小智接入：为 false 时 Start 不建立任何连接（Req 15.5）。
	enabled bool
	// mode 为小智使用的 MCP 模式（smart/full）。
	mode string

	// handler 为工具暴露/调用路由桥接器（默认基于聚合服务）。
	handler EndpointHandler
	// serverBuildFn 用于按当前对外模式构建 MCP 服务端（生产用）。
	// 当设置时优先使用，覆盖自动遵循（smart 显示 4 个网关工具，full 显示全部）。
	serverBuildFn ServerBuildFn
	// connector 为连接建立与会话驱动器（默认 wsConnector）。
	connector EndpointConnector
	// reconnector 为重连调度器（默认指数退避 backoffReconnector，Req 15.4）。
	reconnector Reconnector
	// log 为结构化日志器。
	log *slog.Logger

	// cancel 取消当前运行循环的上下文：Stop 调用它以关闭连接并停止重连（Req 15.5）。
	cancel context.CancelFunc
	// started 标记运行循环是否在运行，保证 Start/Stop 幂等。
	started bool
	// wg 等待运行循环退出，使 Stop 返回时连接确已收束。
	wg sync.WaitGroup
}

// ServerBuildFn 构造一个 MCP 服务端实例，供小智连接的 WebSocket 连接使用。
// 返回的 *mcp.Server 会被挂载到 WebSocket 连接并提供服务。
type ServerBuildFn func(ctx context.Context, mode string) (*mcp.Server, error)

// Option 为 Connector 的可选配置项（函数式选项）。
type Option func(*Connector)

// WithConnector 注入自定义的连接驱动器（测试可注入 mock 接入点）。
func WithConnector(c EndpointConnector) Option {
	return func(cn *Connector) {
		if c != nil {
			cn.connector = c
		}
	}
}

// WithReconnector 注入自定义的重连调度器（主要用于测试，如注入 noReconnect/总是重连）。
func WithReconnector(r Reconnector) Option {
	return func(cn *Connector) {
		if r != nil {
			cn.reconnector = r
		}
	}
}

// WithBackoffPolicy 注入小智重连的指数退避策略（Req 15.4）。
//
// 装配层（任务 27.2）据 config 把初始退避/上限/倍数映射为 BackoffPolicy 注入；非法/越界
// 参数由 BackoffPolicy.normalize 钳制到合法范围与默认值。
func WithBackoffPolicy(policy BackoffPolicy) Option {
	return func(cn *Connector) {
		cn.reconnector = newBackoffReconnector(policy)
	}
}

// WithLogger 注入自定义日志器。
func WithLogger(l *slog.Logger) Option {
	return func(cn *Connector) {
		if l != nil {
			cn.log = l
		}
	}
}

// WithHandler 注入自定义的 EndpointHandler，覆盖默认的聚合桥接器（主要用于测试）。
func WithHandler(h EndpointHandler) Option {
	return func(cn *Connector) {
		if h != nil {
			cn.handler = h
		}
	}
}

// WithServerBuildFn 注入按模式构建 MCP 服务端的函数（生产用）。
// 优先于基于 EndpointHandler 的路径；当设置时小智接入将遵循网关服务的当前对外模式。
func WithServerBuildFn(fn ServerBuildFn) Option {
	return func(cn *Connector) {
		if fn != nil {
			cn.serverBuildFn = fn
		}
	}
}

// NewConnector 构造小智接入服务。
//
//   - endpoint/enabled 来自配置（config.XiaoZhiConfig）；
//   - agg 为聚合服务，默认据其构造 aggregationBridge 作为 EndpointHandler；
//   - 默认连接驱动器为基于 WebSocket 的 wsConnector，默认重连调度器为指数退避（Req 15.4）。
//
// 选项可覆盖连接驱动器、重连调度器、退避策略、日志器与 EndpointHandler，便于测试注入。
func NewConnector(endpoint string, enabled bool, agg domain.Aggregation_Service, opts ...Option) *Connector {
	c := &Connector{
		endpoint:    endpoint,
		enabled:     enabled,
		connector:   newWSConnector(),
		reconnector: newBackoffReconnector(DefaultBackoffPolicy()),
		log:         slog.Default(),
	}
	if agg != nil {
		c.handler = newAggregationBridge(agg)
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start 在启用时建立到小智接入点的连接（Req 15.1）。
//
// 行为约定：
//   - 未启用或地址为空：不建立任何连接，直接返回（Req 15.5 维持断开状态）。
//   - 已在运行：幂等返回，不重复连接。
//   - 否则：派生可取消的运行上下文并启动后台 run 循环，由其经 EndpointConnector 连接并服务。
//
// 传入的 ctx 应为应用生命周期级上下文：ctx 被取消或调用 Stop 均会停止连接与重连。
func (c *Connector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.enabled || c.endpoint == "" {
		return nil
	}
	if c.started {
		return nil
	}
	if c.handler == nil {
		if c.serverBuildFn == nil {
			// 没有可路由的处理器且未注入 serverBuildFn 时不启动，避免空指针。
			return domain.NewError(domain.CodeValidation, "小智接入未配置聚合服务或处理器")
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.started = true

	// 每个启用周期从初始退避间隔重新起算，避免沿用上一周期已增长的退避（Req 15.4/15.5）。
	if r, ok := c.reconnector.(resettableReconnector); ok {
		r.Reset()
	}

	c.wg.Add(1)
	go c.run(runCtx)
	return nil
}

// Stop 关闭与小智接入点的连接并停止自动重连（Req 15.5）。
//
// 通过取消运行上下文实现：EndpointConnector.Serve 响应取消而关闭连接并返回，run 循环
// 据此退出且不再发起重连。Stop 幂等，未启动时为无操作；返回时连接确已收束。
func (c *Connector) Stop() {
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

// Running 报告运行循环当前是否在运行（主要用于测试与状态查询）。
func (c *Connector) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

// Endpoint 返回当前生效的接入点地址（主要用于状态查询与测试）。
func (c *Connector) Endpoint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.endpoint
}

// Enabled 报告当前是否启用小智接入（主要用于状态查询与测试）。
func (c *Connector) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// Mode 返回当前小智接入的 MCP 模式。
func (c *Connector) Mode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// Reconfigure 校验并应用新的接入点地址与启用状态（Req 15.4、15.6）。
//
// 地址校验语义（Req 15.6）：当 enabled 为 true 时，endpoint 必须为 ws:// 或 wss:// 合法
// WebSocket URL；非法则拒绝应用、保持当前已生效配置不变，并返回指示地址格式无效的错误。
// 当 enabled 为 false 时不校验地址（停用不依赖地址），仅记录传入地址以便再次启用时复用。
//
// 本方法只更新连接器持有的配置快照，不负责持久化（持久化由 config.Manager 与管理 API 负责，
// 二者各自以相同规则校验，形成纵深防御）。调用方应在 Reconfigure 成功后据新状态调用
// Start/Stop 以使配置生效；本方法不主动改变运行状态，避免与生命周期管理耦合。
func (c *Connector) Reconfigure(endpoint string, enabled bool, mode string) error {
	if enabled {
		if err := ValidateEndpoint(endpoint); err != nil {
			// 校验失败：保持原配置不变并返回地址格式无效错误（Req 15.6）。
			return err
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.endpoint = endpoint
	c.enabled = enabled
	if mode == "" {
		mode = "full"
	}
	c.mode = mode
	return nil
}

// run 是后台运行循环：连接 → 服务 → （据重连策略）重连，直至上下文取消（Req 15.1/15.5）。
//
// 每轮调用 EndpointConnector.Serve 阻塞至连接断开或 ctx 取消：
//   - 若 ctx 已取消（Stop 或父上下文结束），立即返回且不重连（Req 15.5）；
//   - 否则据 Reconnector.NextDelay 决定是否及何时重连（默认 noReconnect 即不重连，
//     指数退避属任务 21.2，Req 15.4）。
//
// 循环退出时（无论因取消还是不再重连）通过 markStopped 复位运行状态，使 Running 如实反映。
func (c *Connector) run(ctx context.Context) {
	defer c.wg.Done()
	defer c.markStopped()

	for {
		if ctx.Err() != nil {
			return
		}

		var err error
		if c.serverBuildFn != nil {
			err = c.serveWithBuilder(ctx)
		} else {
			err = c.connector.Serve(ctx, c.endpoint, c.handler)
		}

		// 停用/父上下文取消：关闭连接后不再重连（Req 15.5）。
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			c.log.Warn("小智接入点连接结束", "endpoint", c.endpoint, "error", err)
		} else {
			c.log.Info("小智接入点连接已断开", "endpoint", c.endpoint)
		}

		delay, ok := c.reconnector.NextDelay()
		if !ok {
			return
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *Connector) serveWithBuilder(ctx context.Context) error {
	srv, err := c.serverBuildFn(ctx, c.mode)
	if err != nil {
		return fmt.Errorf("构建 MCP 服务端失败：%w", err)
	}
	return serveServer(ctx, c.endpoint, srv)
}

// markStopped 在运行循环退出时复位启停状态，并取消可能残留的运行上下文。
//
// 仅当当前仍标记为运行（started == true）时才复位，避免与 Stop 的显式停止竞争产生误判：
// Stop 会先置 started=false 并取消上下文，此处再次进入则为无操作。
func (c *Connector) markStopped() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.started = false
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}
