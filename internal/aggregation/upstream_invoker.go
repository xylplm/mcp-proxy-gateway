package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 11.1）提供 UpstreamInvoker 的真实实现，把聚合服务在反向映射后还原出的
// (上游标识, 原始名) 转发到对应上游会话，并落实以下调用语义（Req 10.3/10.5/10.8）：
//
//   - 连接不可用：转发前先判定连接状态，非 available 即返回 UPSTREAM_UNAVAILABLE 且
//     不发起任何上游调用（Req 10.5）；
//   - 调用超时：以 context.WithTimeout 约束上游调用，超时即中止本次调用、不返回部分
//     结果并返回 UPSTREAM_TIMEOUT（默认 30s，范围 1-600s，Req 10.8）；
//   - 结果透传：成功结果与上游报告的错误结果均原样返回（Req 10.3）。
//
// 解耦设计（避免 import 循环）：UpstreamInvoker 接口由本聚合包定义，因此真实实现亦置于
// 本包，仅依赖两个窄接口——ConnStateProvider（查询连接状态）与 SessionProvider（获取可
// 调用会话）。聚合包不直接 import manager/transport：
//   - *manager.Manager 的 GetState(id) (domain.ConnState, string) 天然满足 ConnStateProvider；
//   - transport.UpstreamSession 的 CallTool(ctx, name, args) 天然满足 ToolCaller；
// 二者由装配层（任务 27.2）以适配器形式注入，从而把连接生命周期与具体传输与聚合层隔离。

// DefaultUpstreamCallTimeout 为上游工具调用的默认超时时长（Req 10.8，默认 30s）。
// 当注入的超时为非正值时回退到该默认值。可配置范围 1-600s 由 Config_Manager 校验保证。
const DefaultUpstreamCallTimeout = 30 * time.Second

// ConnStateProvider 是查询某上游 MCP 当前连接状态的窄接口（Req 10.5）。
//
// 返回该上游的连接生命周期状态与最近一次失败原因；仅当状态为 available 时聚合调用才会
// 转发。*manager.Manager 已实现 GetState(id string) (domain.ConnState, string)，天然满足
// 本接口，无需额外适配。
type ConnStateProvider interface {
	// GetState 返回上游 MCP 的当前连接状态与最近失败原因（可用时为空）。
	GetState(upstreamID string) (domain.ConnState, string)
}

// ToolCaller 表示一条可向上游转发工具调用的会话能力（窄接口，Req 10.3）。
//
// 仅声明聚合转发实际需要的 CallTool。transport.UpstreamSession 的同名方法签名一致，
// 天然满足本接口；本聚合包据此无需 import transport，避免不必要的耦合与潜在循环。
type ToolCaller interface {
	// CallTool 转发一次工具调用，原始参数原样透传（Req 10.3）。
	CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error)
}

// SessionProvider 按上游标识获取一条可调用工具的会话（窄接口）。
//
// 返回的会话用于在连接可用时转发调用；找不到会话（第二个返回值为 false）时，聚合调用
// 将其视为「连接不可用」处理（Req 10.5）。由装配层基于连接管理器/传输层实现并注入。
type SessionProvider interface {
	// Session 返回某上游 MCP 当前可用于转发调用的会话；不存在时第二个返回值为 false。
	Session(upstreamID string) (ToolCaller, bool)
}

// SessionInvoker 是 UpstreamInvoker 的真实实现：接入连接状态判定与会话调用超时控制。
type SessionInvoker struct {
	// states 为连接状态查询能力（Req 10.5）。
	states ConnStateProvider
	// sessions 为按上游获取可调用会话的能力。
	sessions SessionProvider
	// callTimeout 为单次上游调用的超时时长（Req 10.8）。
	callTimeout time.Duration
}

// 编译期断言：SessionInvoker 必须满足 UpstreamInvoker 接口契约。
var _ UpstreamInvoker = (*SessionInvoker)(nil)

// NewSessionInvoker 构造真实的上游调用转发器。
//
// callTimeout 为上游调用超时（Req 10.8）：非正值时回退到 DefaultUpstreamCallTimeout
// （默认 30s）。装配层（任务 27.2）负责将 config.AggregationConfig.UpstreamCallTimeoutS
// 转换为 time.Duration 并经此注入，再通过 Service.SetInvoker 接入 InvokeTool。
func NewSessionInvoker(states ConnStateProvider, sessions SessionProvider, callTimeout time.Duration) *SessionInvoker {
	if callTimeout <= 0 {
		callTimeout = DefaultUpstreamCallTimeout
	}
	return &SessionInvoker{
		states:      states,
		sessions:    sessions,
		callTimeout: callTimeout,
	}
}

// CallUpstream 把一次工具调用转发到指定上游 MCP（Req 10.3/10.5/10.8）。
//
// 执行步骤：
//  1. 连接可用性判定：状态非 available 则不转发，返回 UPSTREAM_UNAVAILABLE（Req 10.5）。
//  2. 会话获取：无可用会话同样按不可用处理（Req 10.5）。
//  3. 受超时约束转发：以 context.WithTimeout 约束调用，超时则中止、不返回部分结果并返回
//     UPSTREAM_TIMEOUT（Req 10.8）；成功或上游报告的错误结果原样返回（Req 10.3）。
func (in *SessionInvoker) CallUpstream(ctx context.Context, upstreamID, originalName string, args json.RawMessage) (domain.ToolResult, error) {
	// 步骤 1：连接可用性判定（Req 10.5）。非 available 不发起任何上游调用。
	state, lastErr := in.states.GetState(upstreamID)
	if state != domain.ConnAvailable {
		msg := "上游 MCP 连接不可用"
		if lastErr != "" {
			msg = msg + "：" + lastErr
		}
		return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, msg)
	}

	// 步骤 2：获取可调用会话；无会话视为不可用（Req 10.5）。
	session, ok := in.sessions.Session(upstreamID)
	if !ok || session == nil {
		return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 当前无可用会话")
	}

	// 步骤 3：受超时约束转发调用（Req 10.8）。
	timeout := in.callTimeout
	if timeout <= 0 {
		timeout = DefaultUpstreamCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// outcome 承载上游调用的返回值，经缓冲通道在协程间传递。
	type outcome struct {
		result domain.ToolResult
		err    error
	}
	// 缓冲为 1：即便本方法因超时提前返回，后台协程仍可非阻塞写入后退出，不会泄漏。
	// cancel（defer）会取消 callCtx，向遵循上下文的底层调用传播中止信号。
	done := make(chan outcome, 1)
	go func() {
		res, err := session.CallTool(callCtx, originalName, args)
		done <- outcome{result: res, err: err}
	}()

	// classify 将上游调用结果归一化：因超时导致的错误统一映射为 UPSTREAM_TIMEOUT（Req 10.8），
	// 成功或上游报告的错误结果原样返回（Req 10.3），其余传输/上游错误原样回传。
	classify := func(o outcome) (domain.ToolResult, error) {
		if o.err != nil {
			if errors.Is(o.err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
				return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamTimeout, "上游 MCP 调用超时")
			}
			return domain.ToolResult{}, o.err
		}
		return o.result, nil
	}

	select {
	case o := <-done:
		// 调用在超时前完成：原样透传成功/上游错误结果（Req 10.3）。
		return classify(o)
	case <-callCtx.Done():
		// 上下文到期或被取消：优先采纳「恰好同时完成」的结果，避免在边界误报超时；
		// 否则按超时/取消中止本次调用，不返回部分结果（Req 10.8）。
		select {
		case o := <-done:
			return classify(o)
		default:
			if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
				return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamTimeout, "上游 MCP 调用超时")
			}
			return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 调用被取消")
		}
	}
}
