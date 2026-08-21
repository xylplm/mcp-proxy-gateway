package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
)

// 本文件实现上游连接生命周期状态机与自愈协调：
//
//   - 每个上游始终只有一条后台连接循环，负责唯一的拨号、关闭和指数退避；
//   - 连续失败达到阈值后进入 suspended 降频状态，但仍按最大退避间隔持续探测；
//   - 工具调用可请求一次受冷却保护的提前探测，并等待同一条连接循环的结果；
//   - Conn.Wait 返回运行期断连后，循环自动淘汰旧连接并重建，避免遗留失效 SSE 会话。

// Dialer 负责建立并维持单条上游 MCP 连接，屏蔽具体传输与 SDK 细节。
type Dialer interface {
	// Dial 尝试建立连接并完成 MCP 初始化握手；成功返回一条已建立的连接，失败返回错误。
	Dial(ctx context.Context, id string, cfg domain.UpstreamConfig) (Conn, error)
}

// Conn 表示一条已建立的上游 MCP 连接。
type Conn interface {
	// Wait 阻塞直至连接断开或 ctx 被取消；返回断开原因（因 ctx 取消而结束时返回 ctx 错误）。
	Wait(ctx context.Context) error
	// Close 关闭连接并释放资源。重复调用应安全。
	Close() error
}

// ConnStatus 是连接状态查询结果。所有字段均为内存运行期观测数据，进程重启后重新计算。
type ConnStatus struct {
	State               domain.ConnState
	LastError           string
	FailureCount        int
	NextRetryAt         *time.Time
	EffectiveBackoffCap time.Duration
}

// Connection 维护单条上游 MCP 的连接生命周期状态机。所有可变字段均由 mu 保护。
type Connection struct {
	id string

	mu       sync.Mutex
	cfg      domain.UpstreamConfig
	state    domain.ConnState
	failures int
	lastErr  string

	// nextRetryAt 仅在后台等待下一次拨号时存在；connecting/available 时为空。
	nextRetryAt *time.Time
	// stateCh 在状态、失败原因或下次重试时间改变时关闭并替换，供多个等待者共享本轮结果。
	stateCh chan struct{}
	// lastDemandAt 对真实调用触发的提前重连做每上游冷却，避免调用风暴。
	lastDemandAt time.Time

	running bool
	// wake 为提前结束当前退避等待的缓冲信号。它不是连接完成通知。
	wake chan struct{}

	cancel context.CancelFunc
	done   chan struct{}
}

func newConnection(id string) *Connection {
	return &Connection{
		id:      id,
		state:   domain.ConnUnavailable,
		wake:    make(chan struct{}, 1),
		stateCh: make(chan struct{}),
	}
}

func (c *Connection) publishLocked() {
	close(c.stateCh)
	c.stateCh = make(chan struct{})
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func (c *Connection) setConfig(cfg domain.UpstreamConfig) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

func (c *Connection) config() domain.UpstreamConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

func (c *Connection) setState(state domain.ConnState) {
	c.mu.Lock()
	c.state = state
	c.nextRetryAt = nil
	c.publishLocked()
	c.mu.Unlock()
}

func (c *Connection) beginConnecting() {
	c.mu.Lock()
	c.state = domain.ConnConnecting
	c.nextRetryAt = nil
	c.publishLocked()
	c.mu.Unlock()
}

func (c *Connection) succeed() {
	c.mu.Lock()
	c.state = domain.ConnAvailable
	c.failures = 0
	c.lastErr = ""
	c.nextRetryAt = nil
	c.lastDemandAt = time.Time{}
	c.publishLocked()
	c.mu.Unlock()
}

// recordFailure 记录一次失败，并返回是否处于 suspended 及是否刚刚首次进入该降频状态。
func (c *Connection) recordFailure(reason string, threshold int) (bool, bool) {
	if threshold < 1 {
		threshold = 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	wasSuspended := c.state == domain.ConnSuspended
	c.failures++
	c.lastErr = reason
	if c.failures >= threshold {
		c.state = domain.ConnSuspended
	} else {
		c.state = domain.ConnUnavailable
	}
	c.nextRetryAt = nil
	c.publishLocked()
	return c.state == domain.ConnSuspended, !wasSuspended && c.state == domain.ConnSuspended
}

// fail 保留为状态机测试使用的简化入口。
func (c *Connection) fail(reason string, threshold int) bool {
	suspended, _ := c.recordFailure(reason, threshold)
	return suspended
}

func (c *Connection) resetForManual() {
	c.mu.Lock()
	c.failures = 0
	c.lastErr = ""
	c.nextRetryAt = nil
	c.lastDemandAt = time.Time{}
	c.state = domain.ConnConnecting
	c.publishLocked()
	c.mu.Unlock()
}

// snapshot 保持既有内部测试所需的三元组语义。
func (c *Connection) snapshot() (domain.ConnState, string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.lastErr, c.failures
}

func (c *Connection) statusSnapshot() (domain.ConnState, string, int, *time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.lastErr, c.failures, cloneTime(c.nextRetryAt)
}

func (c *Connection) waitSnapshot() (domain.ConnState, string, bool, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.lastErr, c.cfg.Enabled, c.stateCh
}

func (c *Connection) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Connection) signalWake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// requestDemandReconnect 仅允许在不可用状态下按冷却窗口投递一次提前探测信号。
// 已有 connecting 状态代表该上游正在拨号，调用方应只等待同一轮结果而不重复唤醒。
func (c *Connection) requestDemandReconnect(now time.Time, cooldown time.Duration) bool {
	if cooldown <= 0 {
		cooldown = time.Second
	}
	c.mu.Lock()
	if !c.cfg.Enabled || c.state == domain.ConnAvailable || c.state == domain.ConnConnecting ||
		(!c.lastDemandAt.IsZero() && now.Sub(c.lastDemandAt) < cooldown) {
		c.mu.Unlock()
		return false
	}
	c.lastDemandAt = now
	c.mu.Unlock()
	c.signalWake()
	return true
}

func (c *Connection) scheduleRetry(delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	next := time.Now().Add(delay)
	c.mu.Lock()
	c.nextRetryAt = &next
	c.publishLocked()
	c.mu.Unlock()
}

func (c *Connection) clearScheduledRetry() {
	c.mu.Lock()
	if c.nextRetryAt != nil {
		c.nextRetryAt = nil
		c.publishLocked()
	}
	c.mu.Unlock()
}

// waitBackoff 等待下一次后台连接尝试；真实调用可经 wake 立即短路。无论是否进入
// suspended，调用方都会使用该方法，因此 suspended 是降频状态而非永久停止状态。
func (c *Connection) waitBackoff(ctx context.Context, policy RetryPolicy) bool {
	state, _, failures := c.snapshot()
	attempt := failures - 1
	if attempt < 0 {
		attempt = 0
	}
	delay := ComputeBackoff(attempt, policy.InitialBackoff, policy.MaxBackoff, policy.Multiplier)
	// suspended 代表已经告警的长期故障：立即降为最大间隔的持续探测，而不是继续
	// 在指数曲线中高频拨号；真实调用仍可受冷却保护地提前唤醒一次。
	if state == domain.ConnSuspended {
		delay = policy.MaxBackoff
	}
	c.scheduleRetry(delay)

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-c.wake:
		c.clearScheduledRetry()
		return true
	case <-timer.C:
		c.clearScheduledRetry()
		return true
	}
}

func (m *Manager) ConnStatus(id string) ConnStatus {
	backoffCap := m.policy.MaxBackoff
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()
	if c == nil {
		return ConnStatus{State: domain.ConnUnavailable, EffectiveBackoffCap: backoffCap}
	}
	state, lastErr, failures, nextRetryAt := c.statusSnapshot()
	return ConnStatus{
		State:               state,
		LastError:           lastErr,
		FailureCount:        failures,
		NextRetryAt:         nextRetryAt,
		EffectiveBackoffCap: backoffCap,
	}
}

// WaitForAvailable 是调用链的按需恢复入口。它不会自行拨号，而是至多唤醒每上游
// 唯一的后台循环一次，并让所有并发调用等待同一状态变化，避免重连风暴。
func (m *Manager) WaitForAvailable(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-m.baseCtx.Done():
		return domain.NewError(domain.CodeUpstreamUnavailable, "网关正在停止，无法恢复上游连接")
	default:
	}
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()
	if c == nil {
		return domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 当前未登记连接")
	}

	waitCtx, cancel := context.WithTimeout(ctx, m.policy.DemandReconnectWait)
	defer cancel()
	for {
		state, lastErr, enabled, changed := c.waitSnapshot()
		if state == domain.ConnAvailable {
			return nil
		}
		if !enabled {
			return domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 已停用")
		}
		if state != domain.ConnConnecting && c.requestDemandReconnect(time.Now(), m.policy.DemandReconnectCooldown) {
			m.logger.Debug("工具调用请求提前探测上游连接", "upstreamID", id)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.baseCtx.Done():
			return domain.NewError(domain.CodeUpstreamUnavailable, "网关正在停止，无法恢复上游连接")
		case <-waitCtx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return context.DeadlineExceeded
			}
			message := "上游 MCP 正在恢复连接，请稍后重试"
			if lastErr != "" {
				message += "：" + lastErr
			}
			return domain.NewError(domain.CodeUpstreamUnavailable, message)
		case <-changed:
		}
	}
}

func (m *Manager) rebuildConnection(id string, cfg domain.UpstreamConfig) {
	m.connsMu.Lock()
	c, ok := m.conns[id]
	if !ok {
		c = newConnection(id)
		m.conns[id] = c
	}
	m.connsMu.Unlock()

	m.stopLoop(c)
	c.setConfig(cfg)
	if cfg.Enabled {
		m.startLoop(c)
	} else {
		c.setState(domain.ConnUnavailable)
	}
}

func (m *Manager) removeConnection(id string) {
	m.connsMu.Lock()
	c := m.conns[id]
	delete(m.conns, id)
	m.connsMu.Unlock()
	if c != nil {
		m.stopLoop(c)
	}
}

func (m *Manager) startLoop(c *Connection) {
	if m.dialer == nil {
		c.setState(domain.ConnConnecting)
		return
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.baseCtx)
	done := make(chan struct{})
	c.cancel = cancel
	c.done = done
	c.running = true
	c.state = domain.ConnConnecting
	c.failures = 0
	c.lastErr = ""
	c.nextRetryAt = nil
	c.lastDemandAt = time.Time{}
	c.publishLocked()
	c.mu.Unlock()

	m.loopWG.Add(1)
	go func() {
		defer m.loopWG.Done()
		defer close(done)
		defer func() {
			c.mu.Lock()
			if c.done == done {
				c.running = false
				c.cancel = nil
				c.done = nil
				c.publishLocked()
			}
			c.mu.Unlock()
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				safego.LogRecovered(m.logger, "上游连接生命周期 worker panic 已恢复", recovered, "upstreamID", c.id)
				m.onFailure(c, fmt.Sprintf("连接生命周期异常：%v", recovered))
			}
		}()
		m.runLoop(ctx, c)
	}()
}

func (m *Manager) stopLoop(c *Connection) {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	cancel := c.cancel
	done := c.done
	c.running = false
	c.cancel = nil
	c.done = nil
	c.nextRetryAt = nil
	c.publishLocked()
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (m *Manager) runLoop(ctx context.Context, c *Connection) {
	for {
		if ctx.Err() != nil {
			return
		}

		c.beginConnecting()
		conn, err := m.safeDial(ctx, c)
		if err == nil && conn == nil {
			err = fmt.Errorf("拨号器返回空连接")
		}

		if err == nil {
			if ctx.Err() != nil {
				_ = m.safeClose(c.id, conn)
				return
			}
			c.succeed()

			waitErr := m.safeWait(ctx, c.id, conn)
			_ = m.safeClose(c.id, conn)
			if ctx.Err() != nil {
				return
			}
			reason := "运行期连接断开"
			if waitErr != nil {
				reason = waitErr.Error()
			}
			m.onFailure(c, reason)
		} else {
			if ctx.Err() != nil {
				return
			}
			m.onFailure(c, err.Error())
		}

		if !c.waitBackoff(ctx, m.policy) {
			return
		}
	}
}

func (m *Manager) safeDial(ctx context.Context, c *Connection) (conn Conn, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			safego.LogRecovered(m.logger, "上游连接拨号 panic 已恢复", recovered, "upstreamID", c.id)
			err = fmt.Errorf("上游连接拨号异常：%v", recovered)
		}
	}()
	return m.dialer.Dial(ctx, c.id, c.config())
}

func (m *Manager) safeWait(ctx context.Context, upstreamID string, conn Conn) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			safego.LogRecovered(m.logger, "上游连接等待 panic 已恢复", recovered, "upstreamID", upstreamID)
			err = fmt.Errorf("上游连接等待异常：%v", recovered)
		}
	}()
	return conn.Wait(ctx)
}

func (m *Manager) safeClose(upstreamID string, conn Conn) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			safego.LogRecovered(m.logger, "上游连接关闭 panic 已恢复", recovered, "upstreamID", upstreamID)
			err = fmt.Errorf("上游连接关闭异常：%v", recovered)
		}
	}()
	return conn.Close()
}

func (m *Manager) onFailure(c *Connection, reason string) {
	suspended, enteredSuspended := c.recordFailure(reason, m.policy.FailureThreshold)
	if enteredSuspended {
		m.logger.Warn("上游 MCP 连续失败达到阈值，已进入降频持续探测状态",
			"upstreamID", c.id,
			"threshold", m.policy.FailureThreshold,
			"lastError", reason,
			"maxBackoff", m.policy.MaxBackoff,
		)
		return
	}
	if suspended {
		m.logger.Debug("上游 MCP 仍处于降频恢复状态，等待下一次探测", "upstreamID", c.id)
	}
}

func (m *Manager) Shutdown() {
	m.baseCancel()
	m.loopWG.Wait()

	// 通过状态版本通知唤醒仍在 WaitForAvailable 中的请求，避免关闭期间等待到自身超时。
	m.connsMu.Lock()
	conns := make([]*Connection, 0, len(m.conns))
	for _, c := range m.conns {
		conns = append(conns, c)
	}
	m.connsMu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		c.publishLocked()
		c.mu.Unlock()
	}
}

// RetryUnavailable 唤醒所有处于失败状态的已启用上游立即重试一次，返回被唤醒数量。
//
// 用于「外部原因刚被修好」的场景：最典型的是用户在运行环境里补装了缺失的 npm/pip
// 依赖。此时后台循环虽然永不放弃，但降频状态下要等到最大退避间隔（默认 5 分钟）
// 才会再探测一次，用户会以为没生效而去逐个点重连。
//
// 只唤醒既有循环，不新建连接、不重置失败计数，也不触碰 available/connecting 的
// 上游，因此不会打断正在服务的会话，也不会造成重连风暴。非阻塞：signalWake 写的是
// 容量 1 的缓冲通道，已有待处理唤醒时直接丢弃。
func (m *Manager) RetryUnavailable() int {
	m.connsMu.Lock()
	conns := make([]*Connection, 0, len(m.conns))
	for _, c := range m.conns {
		conns = append(conns, c)
	}
	m.connsMu.Unlock()

	woken := 0
	for _, c := range conns {
		state, _, enabled, _ := c.waitSnapshot()
		if !enabled {
			continue
		}
		if state != domain.ConnUnavailable && state != domain.ConnSuspended {
			continue
		}
		c.signalWake()
		woken++
	}
	if woken > 0 {
		m.logger.Info("已唤醒失败中的上游立即重试", "count", woken)
	}
	return woken
}
