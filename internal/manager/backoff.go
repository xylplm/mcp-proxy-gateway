package manager

import "time"

// RetryPolicy 为连接重试退避策略参数。
//
// 各字段语义对应 config.ConnectionConfig，但在此以 time.Duration 表达，便于直接
// 用于定时器与退避计算（避免上层与 manager 之间的强耦合）。装配层（任务 27.2）
// 负责把 config.ConnectionConfig 映射为 RetryPolicy 并通过 WithRetryPolicy 注入。
type RetryPolicy struct {
	// ConnectTimeout 为单次连接建立超时（Req 4.9）。
	ConnectTimeout time.Duration
	// InitialBackoff 为初始退避间隔（Req 5.1）。
	InitialBackoff time.Duration
	// MaxBackoff 为退避间隔上限（Req 5.3）。
	MaxBackoff time.Duration
	// Multiplier 为退避倍数，每次重试将上一次退避乘以该倍数（Req 5.1）。
	Multiplier int
	// FailureThreshold 为连续失败阈值，达到后转 suspended 暂停自动重试（Req 5.6）。
	FailureThreshold int
}

// DefaultRetryPolicy 返回与设计默认值一致的退避策略。
//
// 对应 config 默认值：连接超时 30s、初始退避 1s、退避上限 60s、倍数 2、失败阈值 5。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		ConnectTimeout:   30 * time.Second,
		InitialBackoff:   1 * time.Second,
		MaxBackoff:       60 * time.Second,
		Multiplier:       2,
		FailureThreshold: 5,
	}
}

// normalize 用合理默认值填充非法/零值字段，保证退避计算与状态机稳健运行。
//
// 注意：不强制 MaxBackoff >= InitialBackoff——配置允许上限小于初始值
// （如 initial=60s、max=1s），此时 ComputeBackoff 会统一钳到 MaxBackoff，符合
// backoff = min(initial × multiplier^n, max) 的语义。
func (p RetryPolicy) normalize() RetryPolicy {
	if p.ConnectTimeout <= 0 {
		p.ConnectTimeout = 30 * time.Second
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = time.Second
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 60 * time.Second
	}
	if p.Multiplier < 1 {
		p.Multiplier = 2
	}
	if p.FailureThreshold < 1 {
		p.FailureThreshold = 5
	}
	return p
}

// ComputeBackoff 计算第 attempt 次重试的指数退避间隔：min(initial × multiplier^attempt, max)。
//
// 该函数为纯函数，是属性测试（任务 9.6 / Property 14）的主要目标，保证以下不变量：
//   - 结果始终不超过 max（当 max > 0 时）；
//   - 关于 attempt 单调非递减（更晚的重试退避不小于更早的重试）；
//   - 结果非负且不会因连乘溢出 int64。
//
// attempt 自 0 起计：attempt=0 返回 min(initial, max)。multiplier < 1 视为 1，
// initial/max 的负值按 0 处理。
func ComputeBackoff(attempt int, initial, max time.Duration, multiplier int) time.Duration {
	if initial < 0 {
		initial = 0
	}
	if max < 0 {
		max = 0
	}
	if multiplier < 1 {
		multiplier = 1
	}

	// 初始值已不小于上限时直接钳到上限（涵盖 attempt=0 时 min(initial,max)=max 的情形）。
	backoff := initial
	if backoff >= max {
		return max
	}

	mult := time.Duration(multiplier)
	for i := 0; i < attempt; i++ {
		// 若再乘以 multiplier 会达到或超过上限（或可能溢出），直接钳到上限。
		// backoff <= max/mult（整数下取整）可保证 backoff*mult <= max，既防溢出又防越界。
		if backoff > max/mult {
			return max
		}
		backoff *= mult
		if backoff >= max {
			return max
		}
	}
	return backoff
}
