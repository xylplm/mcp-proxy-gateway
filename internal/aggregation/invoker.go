package aggregation

import (
	"context"
	"encoding/json"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// UpstreamInvoker 是聚合服务转发工具调用所需的窄接口。
//
// 聚合服务在通过可见性校验、并经反向映射把对外名称还原为 (上游标识, 原始名) 之后，
// 通过本接口把调用转发到对应上游 MCP，并将其返回结果原样回传（Req 10.3、10.6）。
//
// 仅声明转发调用这一最小能力，不耦合具体的上游会话/连接管理实现，便于：
//   - 本任务（4.6）以接口占位，先完成「可见性校验 + 反向映射 + 路由」骨架；
//   - 任务 11.1 提供真实实现（连接不可用判断、调用超时控制、结果原样透传，
//     对应 Req 10.5、10.8）；
//   - 属性测试以内存实现替换（mock），验证可见性与可逆性等不变量。
type UpstreamInvoker interface {
	// CallUpstream 把一次工具调用转发到指定上游 MCP。
	//
	//   - upstreamID：目标上游 MCP 的标识（由反向映射还原得到）。
	//   - originalName：该工具在上游 MCP 中的原始名称（转发依据，Req 10.6）。
	//   - args：调用方提供的原始入参，原样透传，聚合层不做任何改写（Req 10.3）。
	//
	// 返回上游 MCP 的执行结果（无论成功或上游报告的错误均原样返回）。
	// 连接不可用、调用超时等错误语义由实现方（任务 11.1）负责。
	CallUpstream(ctx context.Context, upstreamID, originalName string, args json.RawMessage) (domain.ToolResult, error)
}

// UpstreamAvailability 是 UpstreamInvoker 可选实现的候选可用性探测能力。
//
// 聚合路由在同名工具有多个来源时用它跳过当前不可用来源，从而让优先可用上游、
// 轮询等策略可以自然溢出到其他健康来源。未实现该接口时，聚合层仍按候选顺序调用，
// 由 UpstreamInvoker 自身返回不可用错误。
type UpstreamAvailability interface {
	UpstreamAvailable(upstreamID string) bool
}

// RecoveryAwareInvoker 标记调用器能够在真正分发工具请求前协调按需恢复。聚合路由
// 仅在没有健康候选时选择一个兼容来源交给该调用器，避免绕过多来源健康回退策略。
type RecoveryAwareInvoker interface {
	SupportsOnDemandRecovery() bool
}

// PreDispatchInvoker 在确认上游会话可用、但尚未发送 tools/call 时执行回调。它让
// 聚合层把额度预占放在真实分发边界，避免连接恢复失败的请求消耗来源额度。
type PreDispatchInvoker interface {
	CallUpstreamWithPreDispatch(
		ctx context.Context,
		upstreamID, originalName string,
		args json.RawMessage,
		beforeDispatch func(context.Context) error,
	) (domain.ToolResult, error)
}

// PreDispatchToolCaller 由受管会话实现：它在会话仍被固定、且实际 CallTool 即将
// 发出时执行回调，避免连接恢复后的额度预占与真正分发之间出现关闭竞态。
type PreDispatchToolCaller interface {
	ToolCaller
	CallToolWithPreDispatch(
		ctx context.Context,
		name string,
		args json.RawMessage,
		beforeDispatch func(context.Context) error,
	) (domain.ToolResult, error)
}

// PreDispatchError 表示工具尚未发往上游时就结束的恢复/预占错误。聚合层可安全
// 尝试下一个恢复候选；该错误绝不能出现在已经调用 CallTool 的路径中。
type PreDispatchError struct {
	Err error
}

func (e *PreDispatchError) Error() string {
	if e == nil || e.Err == nil {
		return "调用前准备失败"
	}
	return e.Err.Error()
}

func (e *PreDispatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
