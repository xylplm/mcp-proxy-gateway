package app

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

// 本文件汇集装配层（任务 27.2）所需的「胶水适配器」：把各包导出的具体类型/窄接口
// 桥接到彼此期望的接口形态上，从而在不修改既有包的前提下完成接线。各适配器都很薄，
// 仅做形态转换与必要的状态持有，业务逻辑仍在各自的应用服务/领域核心内。

// --- 聚合服务数据访问适配器（store → aggregation 窄接口）---

// upstreamListerAdapter 把 *store.UpstreamRepo 适配为 aggregation.UpstreamLister。
//
// 仓储 List 返回 []store.UpstreamRow（已按 sort_order 升序），此处投影为聚合所需的
// []domain.Upstream（聚合服务自行筛选 Enabled）。
type upstreamListerAdapter struct {
	repo *store.UpstreamRepo
}

func (a upstreamListerAdapter) ListUpstreams(ctx context.Context) ([]domain.Upstream, error) {
	rows, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	ups := make([]domain.Upstream, 0, len(rows))
	for _, row := range rows {
		ups = append(ups, row.Upstream)
	}
	return ups, nil
}

// aliasListerAdapter 把 *store.AliasRepo 适配为 aggregation.AliasLister。
type aliasListerAdapter struct {
	repo *store.AliasRepo
}

func (a aliasListerAdapter) ListAliasesByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error) {
	return a.repo.ListByUpstream(ctx, upstreamID)
}

// mcpFilterListerAdapter 把 *store.FilterMCPRepo 适配为 aggregation.MCPFilterLister。
type mcpFilterListerAdapter struct {
	repo *store.FilterMCPRepo
}

func (a mcpFilterListerAdapter) ListMCPFiltersByUpstream(ctx context.Context, upstreamID string) ([]domain.FilterRule, error) {
	rows, err := a.repo.ListByUpstream(ctx, upstreamID)
	if err != nil {
		return nil, err
	}
	rules := make([]domain.FilterRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, row.FilterRule)
	}
	return rules, nil
}

// apiKeyFilterListerAdapter 把 *store.FilterAPIKeyRepo 适配为 aggregation.APIKeyFilterLister。
type apiKeyFilterListerAdapter struct {
	repo *store.FilterAPIKeyRepo
}

func (a apiKeyFilterListerAdapter) ListAPIKeyFiltersByAPIKey(ctx context.Context, apiKeyID string) ([]domain.FilterRule, error) {
	rows, err := a.repo.ListByAPIKey(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	rules := make([]domain.FilterRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, row.FilterRule)
	}
	return rules, nil
}

// --- 连通性探测适配器（GORM/redis → health.Pinger）---

// pinger 把 GORM PG 连接与 Redis 客户端适配为 health.Pinger，供启动连通性探测与详细健康端点复用。
type pinger struct {
	db  *gorm.DB
	rdb *redis.Client
}

func (p pinger) PingPG(ctx context.Context) error {
	return store.PingDB(ctx, p.db)
}

func (p pinger) PingRedis(ctx context.Context) error {
	return store.PingRedis(ctx, p.rdb)
}

// --- 连接拨号与会话注册（transport + manager → aggregation 调用路由）---

// sessionDialer 既满足 manager.Dialer（建立连接 + 维持生命周期），又作为
// aggregation.SessionProvider 为聚合调用提供可转发的会话。
//
// 设计：MCP_Manager 的 Dialer 仅返回 manager.Conn（Wait/Close），不暴露 CallTool；
// 而聚合调用需要按上游标识取出一个能 CallTool 的会话。故本适配器在 Dial 成功后，
// 把底层 transport.UpstreamSession 以上游标识登记到注册表，连接断开（Close）时注销，
// 从而让 SessionProvider.Session 能在连接可用期间取出对应会话转发调用（Req 10.3）。
type sessionDialer struct {
	factory transport.TransportFactory

	mu       sync.RWMutex
	sessions map[string]transport.UpstreamSession
}

// 编译期断言：sessionDialer 必须满足 manager.Dialer 与 aggregation.SessionProvider。
var (
	_ manager.Dialer              = (*sessionDialer)(nil)
	_ aggregation.SessionProvider = (*sessionDialer)(nil)
)

// newSessionDialer 构造拨号兼会话注册器。
func newSessionDialer(factory transport.TransportFactory) *sessionDialer {
	return &sessionDialer{
		factory:  factory,
		sessions: make(map[string]transport.UpstreamSession),
	}
}

// Dial 构造并连接一条上游会话，成功后登记到注册表并返回一个受管 manager.Conn。
func (d *sessionDialer) Dial(ctx context.Context, id string, cfg domain.UpstreamConfig) (manager.Conn, error) {
	sess, err := d.factory.NewSession(cfg)
	if err != nil {
		return nil, err
	}
	if err := sess.Connect(ctx); err != nil {
		_ = sess.Close()
		return nil, err
	}
	d.register(id, sess)
	return &dialedConn{dialer: d, id: id, session: sess}, nil
}

func (d *sessionDialer) register(id string, sess transport.UpstreamSession) {
	d.mu.Lock()
	d.sessions[id] = sess
	d.mu.Unlock()
}

func (d *sessionDialer) unregister(id string) {
	d.mu.Lock()
	delete(d.sessions, id)
	d.mu.Unlock()
}

// Session 实现 aggregation.SessionProvider：返回某上游当前已登记的可调用会话。
func (d *sessionDialer) Session(upstreamID string) (aggregation.ToolCaller, bool) {
	d.mu.RLock()
	sess, ok := d.sessions[upstreamID]
	d.mu.RUnlock()
	if !ok || sess == nil {
		return nil, false
	}
	return sess, true
}

// dialedConn 包装一条已连接的 transport.UpstreamSession，满足 manager.Conn：
//   - Wait 阻塞至 ctx 取消（transport 会话本身不暴露断开事件，连接生命周期由 ctx 驱动）；
//   - Close 关闭底层会话并从会话注册表注销。
type dialedConn struct {
	dialer  *sessionDialer
	id      string
	session transport.UpstreamSession
	once    sync.Once
}

// 编译期断言：dialedConn 必须满足 manager.Conn。
var _ manager.Conn = (*dialedConn)(nil)

func (c *dialedConn) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *dialedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.dialer.unregister(c.id)
		err = c.session.Close()
	})
	return err
}

// --- 同步服务工具拉取适配器（transport + manager → sync.ToolFetcher）---

// toolFetcher 满足 sync.ToolFetcher：按上游标识拉取其工具列表。
//
// 它优先复用 sessionDialer 注册表中已建立的会话（连接可用时零额外开销）；若该上游
// 当前无活跃会话，则依据其持久化配置临时建立一条会话拉取后立即关闭，以支持「缓存缺失
// 触发一次拉取」等场景（Req 6.3）。凭证明文随持久化配置直接携带，无需解密。
type toolFetcher struct {
	dialer  *sessionDialer
	factory transport.TransportFactory
	repo    *store.UpstreamRepo
}

func (f *toolFetcher) FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	// 优先使用已建立的活跃会话。
	if sess, ok := f.sessionFor(upstreamID); ok {
		return sess.ListTools(ctx)
	}

	// 无活跃会话：按持久化配置临时建立一条会话拉取后关闭。
	row, err := f.repo.Get(ctx, upstreamID)
	if err != nil {
		return nil, err
	}
	sess, err := f.factory.NewSession(row.Config)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Connect(ctx); err != nil {
		return nil, err
	}
	return sess.ListTools(ctx)
}

func (f *toolFetcher) sessionFor(upstreamID string) (transport.UpstreamSession, bool) {
	f.dialer.mu.RLock()
	sess, ok := f.dialer.sessions[upstreamID]
	f.dialer.mu.RUnlock()
	return sess, ok && sess != nil
}
