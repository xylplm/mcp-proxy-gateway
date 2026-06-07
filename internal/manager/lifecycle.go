package manager

import (
	"context"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 9.5）实现连接生命周期状态机与指数退避重试，是 MCP_Manager 连接编排
// 的核心：
//
//   - 状态机：connecting → available / unavailable / suspended，转移见 Connection 各方法；
//   - 指数退避：复用 backoff.go 的 ComputeBackoff，退避间隔 min(initial × 2^n, max)（Req 5.1/5.2/5.3）；
//   - 失败计数：建立失败或运行期断开均累计连续失败次数并据退避重试（Req 5.1/5.2/5.4）；
//   - 暂停自动重试：连续失败达阈值转 suspended、记录告警并暂停自动重试，待管理员手动
//     重连（或后续任务 10 的 cron 同步）唤醒（Req 5.6）；
//   - 恢复可用：重连成功将连续失败计数重置为 0 并恢复可用（Req 5.5）。
//
// 设计要点：
//   - 真实拨号通过 Dialer 接口注入（装配层/任务 11.1、27.2 提供），使本状态机与具体传输
//     解耦、便于单元测试以 fake dialer 驱动；未注入 Dialer 时连接条目仅作占位登记，
//     不启动后台重试循环。
//   - Connection 持有该上游的配置（含明文凭证，仅存内存），因此 Create/Update/Reconnect
//     无需重新解密即可重建连接；进程冷启动时从持久化重建连接需解密，属装配层职责。

// Dialer 负责建立并维持单条上游 MCP 连接，屏蔽具体传输与 SDK 细节。
//
// 接入点（任务 11.1/27.2）：实现方通常基于 transport.TransportFactory 构造会话、
// 调用 Connect 完成 MCP 初始化握手并返回一个 Conn。建立失败返回错误（计为一次失败）。
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

// ConnStatus 为连接状态查询结果，体现状态、最近失败原因与当前生效退避上限（Req 5.3、5.4）。
type ConnStatus struct {
	// State 为当前连接生命周期状态。
	State domain.ConnState
	// LastError 为最近一次连接失败的原因（可用时为空）。
	LastError string
	// EffectiveBackoffCap 为当前生效的退避间隔上限（Req 5.3）。
	EffectiveBackoffCap time.Duration
}

// Connection 维护单条上游 MCP 的连接生命周期状态机。
//
// 它持有该上游的配置（含明文凭证，仅存内存）、当前状态、连续失败次数与最近失败原因，
// 并通过 wake 通道接收手动重连/唤醒信号。所有可变字段由 mu 保护，转移方法均为并发安全。
type Connection struct {
	// id 为上游 MCP 标识。
	id string

	mu sync.Mutex
	// cfg 为该上游配置（含明文凭证，仅内存），供拨号与重建使用。
	cfg domain.UpstreamConfig
	// state 为当前连接状态。
	state domain.ConnState
	// failures 为当前连续失败次数；重连成功后归零（Req 5.5）。
	failures int
	// lastErr 为最近一次失败原因（Req 5.4）。
	lastErr string
	// running 表示后台重试循环是否正在运行。
	running bool

	// wake 为唤醒信号通道（缓冲 1）：手动重连或退避短路时投递。
	wake chan struct{}
	// cancel 取消当前循环的上下文。
	cancel context.CancelFunc
	// done 在当前循环退出时关闭。
	done chan struct{}
}

// newConnection 构造一个初始为 unavailable 的连接记录。
func newConnection(id string) *Connection {
	return &Connection{
		id:    id,
		state: domain.ConnUnavailable,
		wake:  make(chan struct{}, 1),
	}
}

// setConfig 更新连接持有的上游配置（含凭证）。
func (c *Connection) setConfig(cfg domain.UpstreamConfig) {
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

// config 返回连接当前持有配置的副本。
func (c *Connection) config() domain.UpstreamConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// setState 仅更新状态（用于停用/占位等非失败路径）。
func (c *Connection) setState(state domain.ConnState) {
	c.mu.Lock()
	c.state = state
	c.mu.Unlock()
}

// beginConnecting 将状态置为 connecting（每次发起连接尝试前调用）。
func (c *Connection) beginConnecting() {
	c.mu.Lock()
	c.state = domain.ConnConnecting
	c.mu.Unlock()
}

// succeed 在连接建立成功时恢复可用并重置连续失败计数为 0（Req 5.5）。
func (c *Connection) succeed() {
	c.mu.Lock()
	c.state = domain.ConnAvailable
	c.failures = 0
	c.lastErr = ""
	c.mu.Unlock()
}

// fail 记录一次失败：连续失败次数加一并记录原因；达到阈值转 suspended（暂停自动重试），
// 否则转 unavailable。返回是否已转入 suspended（Req 5.4、5.6）。
//
// threshold < 1 时归一化为 1，确保阈值语义稳健。
func (c *Connection) fail(reason string, threshold int) bool {
	if threshold < 1 {
		threshold = 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	c.lastErr = reason
	if c.failures >= threshold {
		c.state = domain.ConnSuspended
		return true
	}
	c.state = domain.ConnUnavailable
	return false
}

// resetForManual 为管理员手动重连准备：将连续失败计数清零并置为 connecting，
// 使其获得一轮全新的重试预算（Req 5.6）。最终是否恢复可用仍取决于本次重连是否成功。
func (c *Connection) resetForManual() {
	c.mu.Lock()
	c.failures = 0
	c.lastErr = ""
	c.state = domain.ConnConnecting
	c.mu.Unlock()
}

// snapshot 返回状态、最近失败原因与当前连续失败次数。
func (c *Connection) snapshot() (domain.ConnState, string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.lastErr, c.failures
}

// isRunning 报告后台重试循环是否正在运行。
func (c *Connection) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// signalWake 以非阻塞方式投递一次唤醒信号（用于手动重连或短路退避等待）。
func (c *Connection) signalWake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// waitBackoff 在退避间隔内等待，期间可被唤醒信号短路或被 ctx 取消。
//
// 退避间隔依据当前连续失败次数计算：ComputeBackoff(failures-1, ...)，即首次失败后
// 为 initial，其后逐次乘以倍数并以 max 为上限（Req 5.1/5.2/5.3）。返回是否应继续重试
// （false 表示 ctx 已取消、循环应退出）。
func (c *Connection) waitBackoff(ctx context.Context, policy RetryPolicy) bool {
	_, _, failures := c.snapshot()
	attempt := failures - 1
	if attempt < 0 {
		attempt = 0
	}
	d := ComputeBackoff(attempt, policy.InitialBackoff, policy.MaxBackoff, policy.Multiplier)

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-c.wake:
		return true
	case <-timer.C:
		return true
	}
}

// waitWake 在 suspended 状态下阻塞，直至收到唤醒信号（手动重连）或 ctx 取消。
// 返回是否被唤醒（false 表示 ctx 已取消、循环应退出）。
func (c *Connection) waitWake(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.wake:
		return true
	}
}

// ConnStatus 返回指定上游的连接状态查询结果（Req 5.3、5.4）。
//
// 未登记的上游返回默认 unavailable 状态、空失败原因与当前生效退避上限。
func (m *Manager) ConnStatus(id string) ConnStatus {
	backoffCap := m.policy.MaxBackoff
	m.connsMu.Lock()
	c := m.conns[id]
	m.connsMu.Unlock()
	if c == nil {
		return ConnStatus{State: domain.ConnUnavailable, LastError: "", EffectiveBackoffCap: backoffCap}
	}
	state, lastErr, _ := c.snapshot()
	return ConnStatus{State: state, LastError: lastErr, EffectiveBackoffCap: backoffCap}
}

// rebuildConnection 登记或重建某上游的连接：停止既有循环、写入新配置，并按 Enabled
// 启动重试循环（启用）或置为不可用（停用）。用于 Create 与 Update（按新配置重建连接）。
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

// removeConnection 停止并移除某上游的连接条目（用于 Delete）。
func (m *Manager) removeConnection(id string) {
	m.connsMu.Lock()
	c := m.conns[id]
	delete(m.conns, id)
	m.connsMu.Unlock()
	if c != nil {
		m.stopLoop(c)
	}
}

// startLoop 启动某连接的后台重试循环（若尚未运行）。
//
// 未注入 Dialer 时不启动真实循环，仅将状态置为 connecting 作为占位登记（连接的实际
// 拨号由装配层在注入 Dialer 后驱动）。
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
	c.mu.Unlock()

	m.loopWG.Add(1)
	go func() {
		defer m.loopWG.Done()
		defer close(done)
		m.runLoop(ctx, c)
	}()
}

// stopLoop 停止某连接正在运行的后台循环并等待其退出。未运行时为无操作。
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
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// runLoop 是单条连接的重试退避主循环（Req 5.1-5.6）。
//
// 循环语义：
//  1. 置 connecting 并尝试拨号；
//  2. 成功 → 置 available 并重置失败计数（Req 5.5），随后阻塞等待运行期断开；断开后
//     按失败处理；
//  3. 失败（建立失败或运行期断开）→ 累计失败、记录原因（Req 5.4）；达阈值转 suspended、
//     记录告警并阻塞等待手动重连唤醒（Req 5.6）；未达阈值则退避后重试（Req 5.1/5.2）；
//  4. ctx 取消（停用/删除/Shutdown）时退出循环。
func (m *Manager) runLoop(ctx context.Context, c *Connection) {
	for {
		if ctx.Err() != nil {
			return
		}

		c.beginConnecting()
		conn, err := m.dialer.Dial(ctx, c.id, c.config())

		if err == nil {
			if ctx.Err() != nil {
				_ = conn.Close()
				return
			}
			// 连接建立成功：恢复可用并重置失败计数（Req 5.5）。
			c.succeed()

			// 阻塞等待运行期断开或 ctx 取消（Req 5.2）。
			waitErr := conn.Wait(ctx)
			_ = conn.Close()
			if ctx.Err() != nil {
				return
			}

			reason := "运行期连接断开"
			if waitErr != nil {
				reason = waitErr.Error()
			}
			if m.onFailure(c, reason) {
				if !c.waitWake(ctx) {
					return
				}
				continue
			}
		} else {
			if ctx.Err() != nil {
				return
			}
			if m.onFailure(c, err.Error()) {
				if !c.waitWake(ctx) {
					return
				}
				continue
			}
		}

		// 未转 suspended：按指数退避等待后重试（可被手动重连唤醒短路）。
		if !c.waitBackoff(ctx, m.policy) {
			return
		}
	}
}

// onFailure 记录一次连接失败并在达到阈值转 suspended 时记录告警事件（Req 5.4、5.6）。
// 返回是否已转入 suspended。
func (m *Manager) onFailure(c *Connection, reason string) bool {
	suspended := c.fail(reason, m.policy.FailureThreshold)
	if suspended {
		m.logger.Warn("上游 MCP 连续失败达到阈值，记录告警并暂停自动重试（待管理员手动重连或自动同步唤醒）",
			"upstreamID", c.id,
			"threshold", m.policy.FailureThreshold,
			"lastError", reason,
		)
	}
	return suspended
}

// Shutdown 取消所有连接的后台重试循环并等待其全部退出，用于进程优雅停机。
func (m *Manager) Shutdown() {
	m.baseCancel()
	m.loopWG.Wait()
}
