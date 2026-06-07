package transport

import (
	"context"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 8.3）实现 stdio 传输会话：以子进程方式启动上游 MCP，
// 基于 MCP Go SDK 的 CommandTransport 完成 initialize、tools/list、tools/call（Req 4.1）。

// stdioSession 是 stdio 传输的上游会话。
//
// 它内嵌 *baseSession 复用统一的状态机、超时与 ListTools/CallTool/Close 语义，
// 自身仅实现 Connect——在其中构造 SDK 的 CommandTransport（子进程启动上游 MCP），
// 并通过 establish 注入连接。
type stdioSession struct {
	*baseSession
}

// newStdioSession 构造 stdio 传输会话。连接参数（command/args）已由 baseSession 解析与校验。
func newStdioSession(cfg domain.UpstreamConfig) (UpstreamSession, error) {
	base, err := newBaseSession(cfg, connectTimeoutOf(cfg))
	if err != nil {
		return nil, err
	}
	return &stdioSession{baseSession: base}, nil
}

// Connect 以子进程方式启动上游 MCP 并完成 MCP 初始化握手（Req 4.1、4.9）。
//
// dial 回调中：
//   - 用解析得到的 command/args 构造 *exec.Cmd（子进程即上游 MCP 服务）；
//   - 将其包装为 mcp.CommandTransport，由 SDK 通过子进程的 stdin/stdout 完成 JSON-RPC 收发；
//   - 调用 connectWithTimeout 在已带连接超时的 ctx 下建立连接并完成 initialize 握手。
//
// 说明（Req 4.7）：stdio 传输无 HTTP 头通道，鉴权凭证按上游约定通常以命令行参数或环境变量传入，
// 此处不强制注入；凭证语义对 HTTP 类传输（SSE/Streamable-HTTP/WebSocket）生效。
func (s *stdioSession) Connect(ctx context.Context) error {
	return s.establish(ctx, func(dialCtx context.Context) (mcpClientConn, error) {
		// 注意：刻意使用 exec.Command 而非 exec.CommandContext。dialCtx 仅约束连接建立阶段，
		// establish 在连接成功后会立即取消它；若将子进程绑定到 dialCtx，进程会被随之杀死。
		// 子进程生命周期应等同于会话生命周期，由 CommandTransport 在 Close 时关闭 stdin 优雅终止
		//（必要时 SIGTERM/SIGKILL）。
		cmd := exec.Command(s.params.command, s.params.args...)
		transport := &mcp.CommandTransport{Command: cmd}
		return connectWithTimeout(dialCtx, transport)
	})
}
