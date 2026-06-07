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
