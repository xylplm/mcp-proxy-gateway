package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

// 本文件汇集装配层所需的薄适配器：仅桥接各层窄接口，业务规则仍由所属服务实现。

// --- 聚合服务数据访问适配器（store → aggregation 窄接口）---

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

type aliasListerAdapter struct {
	repo *store.AliasRepo
}

func (a aliasListerAdapter) ListAliasesByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error) {
	return a.repo.ListByUpstream(ctx, upstreamID)
}

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

type toolPolicyListerAdapter struct {
	repo *store.ToolPolicyRepo
}

func (a toolPolicyListerAdapter) ListToolPolicies(ctx context.Context) ([]domain.ToolPolicyRule, error) {
	return a.repo.List(ctx)
}

// --- 连通性探测适配器（GORM/redis → health.Pinger）---

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

// --- 上游保存前测试适配器（transport → httpapi.UpstreamTester）---

type upstreamTester struct {
	factory      transport.TransportFactory
	previewLimit int
}

func (t upstreamTester) Test(ctx context.Context, cfg domain.UpstreamConfig) (domain.UpstreamTestResult, error) {
	if t.factory == nil {
		return domain.UpstreamTestResult{}, domain.NewError(domain.CodeInternal, "上游测试服务未就绪")
	}
	testCtx, cancel := upstreamTestContext(ctx)
	defer cancel()

	start := time.Now()
	sess, err := t.factory.NewSession(cfg)
	if err != nil {
		return domain.UpstreamTestResult{}, err
	}
	defer func() { _ = sess.Close() }()
	if err := sess.Connect(testCtx); err != nil {
		return t.failedResult("connect", start, err), nil
	}
	tools, err := sess.ListTools(testCtx)
	if err != nil {
		return t.failedResult("list_tools", start, err), nil
	}

	previewLimit := t.previewLimit
	if previewLimit <= 0 {
		previewLimit = 8
	}
	preview := tools
	if len(preview) > previewLimit {
		preview = tools[:previewLimit]
	}
	if preview == nil {
		preview = []domain.ToolDef{}
	}
	return domain.UpstreamTestResult{
		OK:         true,
		Stage:      "ok",
		DurationMS: time.Since(start).Milliseconds(),
		Count:      len(tools),
		Tools:      preview,
	}, nil
}

func upstreamTestContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, transport.DefaultConnectTimeout+15*time.Second)
}

func (t upstreamTester) failedResult(stage string, start time.Time, err error) domain.UpstreamTestResult {
	return domain.UpstreamTestResult{
		OK:         false,
		Stage:      stage,
		DurationMS: time.Since(start).Milliseconds(),
		Message:    upstreamTestErrorMessage(err),
		Tools:      []domain.ToolDef{},
	}
}

func upstreamTestErrorMessage(err error) string {
	if apiErr, ok := errors.AsType[*domain.APIError](err); ok {
		return apiErr.Message
	}
	if err != nil {
		return err.Error()
	}
	return "测试失败"
}

// --- 连接拨号与会话注册（transport + manager → aggregation 调用路由）---

// sessionDialer 同时是 Manager 的拨号器和聚合调用的会话提供者。注册受管连接而非
// 裸 session，使运行期 RPC 发现会话终态时可以可靠地反馈给唯一的连接循环。
type sessionDialer struct {
	factory transport.TransportFactory

	mu       sync.RWMutex
	sessions map[string]*dialedConn
}

var (
	_ manager.Dialer              = (*sessionDialer)(nil)
	_ aggregation.SessionProvider = (*sessionDialer)(nil)
)

func newSessionDialer(factory transport.TransportFactory) *sessionDialer {
	return &sessionDialer{factory: factory, sessions: make(map[string]*dialedConn)}
}

func (d *sessionDialer) Dial(ctx context.Context, id string, cfg domain.UpstreamConfig) (manager.Conn, error) {
	sess, err := d.factory.NewSession(cfg)
	if err != nil {
		return nil, err
	}
	if err := sess.Connect(ctx); err != nil {
		_ = sess.Close()
		return nil, err
	}
	conn := newDialedConn(d, id, sess)
	d.register(id, conn)
	return conn, nil
}

func (d *sessionDialer) register(id string, conn *dialedConn) {
	d.mu.Lock()
	d.sessions[id] = conn
	d.mu.Unlock()
}

// unregisterIfCurrent 防止旧连接迟到的 Close/失败通知删除已经成功登记的新会话。
func (d *sessionDialer) unregisterIfCurrent(id string, expected *dialedConn) {
	d.mu.Lock()
	if d.sessions[id] == expected {
		delete(d.sessions, id)
	}
	d.mu.Unlock()
}

func (d *sessionDialer) Session(upstreamID string) (aggregation.ToolCaller, bool) {
	d.mu.RLock()
	conn := d.sessions[upstreamID]
	d.mu.RUnlock()
	if conn == nil {
		return nil, false
	}
	return conn, true
}

func (d *sessionDialer) sessionFor(upstreamID string) (*dialedConn, bool) {
	d.mu.RLock()
	conn := d.sessions[upstreamID]
	d.mu.RUnlock()
	return conn, conn != nil
}

// dialedConn 将真实会话与其运行期失效通知关联。brokenCh 有缓冲且只写一次，因而
// 调用路径不会因 Manager 暂未进入 Wait 而阻塞或泄漏 goroutine。
type dialedConn struct {
	dialer  *sessionDialer
	id      string
	session transport.UpstreamSession

	brokenCh  chan error
	closeOnce sync.Once

	// callMu 将“调用前预占 + 实际 CallTool”与生命周期失效/Close 串行化。普通调用
	// 保持原有并发能力；只有按需恢复候选使用该窄路径，确保额度不早于发送边界。
	callMu sync.Mutex
	broken bool
}

var (
	_ manager.Conn                      = (*dialedConn)(nil)
	_ aggregation.ToolCaller            = (*dialedConn)(nil)
	_ aggregation.PreDispatchToolCaller = (*dialedConn)(nil)
)

func newDialedConn(dialer *sessionDialer, id string, session transport.UpstreamSession) *dialedConn {
	return &dialedConn{dialer: dialer, id: id, session: session, brokenCh: make(chan error, 1)}
}

func (c *dialedConn) Wait(ctx context.Context) error {
	// 能暴露 SDK Session.Wait 的传输在空闲期断连时也会主动通知 Manager；不支持
	// 生命周期通知的会话仍依赖 CallTool/ListTools 失败写入 brokenCh，保持兼容。
	waiter, ok := c.session.(transport.LifecycleWaiter)
	if !ok {
		return c.waitBroken(ctx)
	}

	probeCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
	probeErr := waiter.WaitClosed(probeCtx)
	cancel()
	if errors.Is(probeErr, transport.ErrLifecycleWaitUnsupported) {
		return c.waitBroken(ctx)
	}
	if probeErr != nil && !errors.Is(probeErr, context.DeadlineExceeded) && !errors.Is(probeErr, context.Canceled) {
		// 生命周期在探测窗口内已结束时同样先失效当前注册，避免极短竞态下
		// Manager 重连后仍有并发请求取得该旧会话。
		c.markBroken(probeErr)
		return probeErr
	}

	lifecycleDone := make(chan error, 1)
	go func() { lifecycleDone <- waiter.WaitClosed(ctx) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-c.brokenCh:
		return err
	case err := <-lifecycleDone:
		if err == nil {
			err = transport.ErrSessionLost
		}
		// 先撤销注册，令并发请求转入 Manager 的单飞恢复路径，而不是继续命中
		// 已由 SDK 标记关闭的旧 session。
		c.markBroken(err)
		return err
	}
}

func (c *dialedConn) waitBroken(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-c.brokenCh:
		return err
	}
}

func (c *dialedConn) markBroken(err error) {
	if err == nil {
		return
	}
	c.callMu.Lock()
	defer c.callMu.Unlock()
	c.markBrokenLocked(err)
}

func (c *dialedConn) markBrokenLocked(err error) {
	if err == nil || c.broken {
		return
	}
	c.broken = true
	c.dialer.unregisterIfCurrent(c.id, c)
	c.brokenCh <- err
}

func (c *dialedConn) ListTools(ctx context.Context) ([]domain.ToolDef, error) {
	tools, err := c.session.ListTools(ctx)
	if transport.IsSessionLost(err) {
		c.markBroken(err)
	}
	return tools, err
}

func (c *dialedConn) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	result, err := c.session.CallTool(ctx, name, args)
	if transport.IsSessionLost(err) {
		c.markBroken(err)
	}
	return result, err
}

// CallToolWithPreDispatch 在受管会话仍持有调用锁时完成最后的本地准备与真实发送。
// lifecycle Close 同样取得该锁，因此不会发生“额度预占后、工具尚未发送便关闭”的竞态。
func (c *dialedConn) CallToolWithPreDispatch(
	ctx context.Context,
	name string,
	args json.RawMessage,
	beforeDispatch func(context.Context) error,
) (domain.ToolResult, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if c.broken {
		return domain.ToolResult{}, &aggregation.PreDispatchError{Err: transport.ErrSessionLost}
	}
	if beforeDispatch != nil {
		if err := beforeDispatch(ctx); err != nil {
			return domain.ToolResult{}, &aggregation.PreDispatchError{Err: err}
		}
	}
	result, err := c.session.CallTool(ctx, name, args)
	if transport.IsSessionLost(err) {
		c.markBrokenLocked(err)
	}
	return result, err
}

func (c *dialedConn) Close() error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	var err error
	c.closeOnce.Do(func() {
		c.dialer.unregisterIfCurrent(c.id, c)
		err = c.session.Close()
	})
	return err
}

// --- 同步服务工具拉取适配器（transport + manager → sync.ToolFetcher）---

type toolFetcher struct {
	dialer  *sessionDialer
	factory transport.TransportFactory
	repo    *store.UpstreamRepo
}

func (f *toolFetcher) FetchTools(ctx context.Context, upstreamID string) ([]domain.ToolDef, error) {
	if sess, ok := f.dialer.sessionFor(upstreamID); ok {
		return sess.ListTools(ctx)
	}

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
