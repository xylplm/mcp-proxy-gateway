package xiaozhi

import (
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
)

// 本文件（任务 21.2）实现小智接入服务的指数退避重连调度器（Req 15.4）。
//
// 复用任务 9.5 已实现的退避计算纯函数 manager.ComputeBackoff
// （backoff = min(initial × 倍数^n, 上限)），在其之上仅维护「连续重连尝试次数」这一最小状态，
// 作为任务 21.1 预留的 Reconnector 接缝的生产实现，替换默认的 noReconnect。
//
// 与上游 MCP 连接状态机（MCP_Manager）不同，小智接入没有「失败阈值后挂起」语义：只要管理员
// 保持启用，就应持续按退避重连，直至停用（停用通过 Connector 取消上下文实现，Req 15.5）。

// 小智退避策略的默认值与可配置取值范围（Req 15.4）。
const (
	// defaultInitialBackoff 为默认初始退避间隔（Req 15.4：默认 1 秒）。
	defaultInitialBackoff = 1 * time.Second
	// defaultMaxBackoff 为默认退避上限（Req 15.4：默认 60 秒）。
	defaultMaxBackoff = 60 * time.Second
	// defaultMultiplier 为默认退避倍数（Req 15.4：倍数 2）。
	defaultMultiplier = 2

	// minInitialBackoff/maxInitialBackoff 为初始退避的可配置范围（Req 15.4：1 至 60 秒）。
	minInitialBackoff = 1 * time.Second
	maxInitialBackoff = 60 * time.Second
	// minMaxBackoff/maxMaxBackoff 为退避上限的可配置范围（Req 15.4：1 至 3600 秒）。
	minMaxBackoff = 1 * time.Second
	maxMaxBackoff = 3600 * time.Second
)

// BackoffPolicy 为小智重连的指数退避参数（Req 15.4）。
//
// 装配层（任务 27.2）据 config.XiaoZhiConfig / config.ConnectionConfig 构造该策略并通过
// WithBackoffPolicy 注入；各字段越界或为零时由 normalize 钳制到合法范围与默认值。
type BackoffPolicy struct {
	// Initial 为初始退避间隔，默认 1s、范围 1-60s。
	Initial time.Duration
	// Max 为退避间隔上限，默认 60s、范围 1-3600s。
	Max time.Duration
	// Multiplier 为退避倍数，默认 2、需大于等于 1。
	Multiplier int
}

// DefaultBackoffPolicy 返回与需求默认值一致的退避策略（初始 1s、上限 60s、倍数 2）。
func DefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicy{
		Initial:    defaultInitialBackoff,
		Max:        defaultMaxBackoff,
		Multiplier: defaultMultiplier,
	}
}

// normalize 将非法/越界字段钳制到合法范围与默认值，保证退避计算稳健（Req 15.4 的取值范围）。
//
// 钳制语义：低于下界回落到默认值（更安全的保守退避），高于上界钳到上界。该归一化是纯防御性的
// （正常路径下配置已在 config 层按 1-60s / 1-3600s 校验并拒绝非法值），仅用于防范编程错误。
//
// 注意：不强制 Max >= Initial——若上限小于初始值，ComputeBackoff 会统一钳到上限，
// 与 backoff = min(initial × 倍数^n, 上限) 的语义一致。
func (p BackoffPolicy) normalize() BackoffPolicy {
	switch {
	case p.Initial < minInitialBackoff:
		p.Initial = defaultInitialBackoff
	case p.Initial > maxInitialBackoff:
		p.Initial = maxInitialBackoff
	}
	switch {
	case p.Max < minMaxBackoff:
		p.Max = defaultMaxBackoff
	case p.Max > maxMaxBackoff:
		p.Max = maxMaxBackoff
	}
	if p.Multiplier < 1 {
		p.Multiplier = defaultMultiplier
	}
	return p
}

// resettableReconnector 是可重置退避状态的可选接口；Connector 在启用时据此把退避归零。
type resettableReconnector interface {
	Reset()
}

// backoffReconnector 是基于 manager.ComputeBackoff 的指数退避重连调度器（Req 15.4）。
//
// 它维护连续重连尝试次数 attempt：第 n 次重连（n 自 0 起）等待 min(initial × 倍数^n, 上限)。
// 小智在启用期间应持续重连直至管理员停用，故 NextDelay 始终返回 true（停止重连由 Connector
// 在停用时取消运行上下文实现，Req 15.5）。该类型对并发使用安全。
type backoffReconnector struct {
	policy BackoffPolicy

	mu      sync.Mutex
	attempt int
}

// newBackoffReconnector 据退避策略构造重连调度器，非法/越界参数以默认值与范围钳制。
func newBackoffReconnector(policy BackoffPolicy) *backoffReconnector {
	return &backoffReconnector{policy: policy.normalize()}
}

// 编译期断言：backoffReconnector 满足 Reconnector 与 resettableReconnector 契约。
var (
	_ Reconnector           = (*backoffReconnector)(nil)
	_ resettableReconnector = (*backoffReconnector)(nil)
)

// NextDelay 返回下一次重连前的退避间隔并推进尝试计数；始终请求重连（Req 15.4）。
func (r *backoffReconnector) NextDelay() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := manager.ComputeBackoff(r.attempt, r.policy.Initial, r.policy.Max, r.policy.Multiplier)
	r.attempt++
	return d, true
}

// Reset 将重连尝试计数清零，使后续重连从初始退避间隔重新开始。
//
// Connector 在每次 Start（启用/重新启用）时调用它，保证每个启用周期的退避从初始值起算
// （Req 15.5：停用后再次启用时不应沿用上一周期已增长的退避）。
func (r *backoffReconnector) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempt = 0
}
