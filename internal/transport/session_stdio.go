package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
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
		policy := currentPolicy()
		profile := runtime.SecurityProfile{}
		if raw, ok := s.cfg.ConnParams[ParamSecurityProfile]; ok && raw != nil {
			// fail-closed：与保存路径一致，非法/损坏 profile 不得静默回落默认档。
			p, err := runtime.ValidateSecurityProfile(raw)
			if err != nil {
				return nil, err
			}
			profile = p
		}
		cwd := s.params.cwd
		if cwd != "" {
			cwd = resolveCredentialPlaceholders(cwd, s.credential)
		}
		args := resolveStringSliceCredentials(s.params.args, s.credential)
		command := s.params.command
		scriptRisk := scripts.RiskLevel("")
		var scriptBinding scripts.LaunchBinding
		if managedCommand, managedArgs, managedCWD, risk, binding, isScript, err := resolveManagedScript(s.cfg.ConnParams); isScript {
			if err != nil {
				return nil, err
			}
			command = managedCommand
			args = managedArgs
			cwd = managedCWD
			scriptRisk = risk
			scriptBinding = binding
		} else if dirCommand, dirArgs, dirCWD, isDirectory, err := resolveDirectoryLaunch(s.cfg.ConnParams, policy, profile.FileAccess.Paths); isDirectory {
			if err != nil {
				return nil, err
			}
			command = dirCommand
			args = dirArgs
			cwd = dirCWD
		}
		eff := runtime.ResolveEffectiveSecurity(policy, profile, cwd)
		if err := runtime.ValidateIsolationRequirement(policy, eff); err != nil {
			return nil, err
		}
		if scriptRisk == scripts.RiskCritical && eff.Mode != runtime.SecurityModeUnrestricted {
			return nil, fmt.Errorf("极高风险脚本必须使用完全放行档位并明确确认风险")
		}
		if err := runtime.ValidateCommandForSecurity(command, policy, eff); err != nil {
			return nil, err
		}
		if err := runtime.ValidateEffectiveSecurityWithCommand(eff, cwd, command, args); err != nil {
			return nil, err
		}

		pathPrefixes := currentPathPrefixes()
		// 严格档仅 runtime 卷解析：不回落系统 PATH。
		var resolveErr error
		if eff.Mode == runtime.SecurityModeStrict && eff.StrictPathOnly {
			command, resolveErr = runtime.ResolveCommandStrictRuntime(command, pathPrefixes)
		} else {
			command, resolveErr = runtime.ResolveCommandWithPrefixes(command, pathPrefixes)
		}
		if resolveErr != nil {
			return nil, resolveErr
		}
		// 解析后的绝对路径再按基名校验，防止 PATH 劫持绕过 allowlist。
		if err := runtime.ValidateCommandForSecurity(command, policy, eff); err != nil {
			return nil, err
		}

		var verifiedScript *verifiedScriptLaunch
		if scriptBinding.ScriptID != "" {
			prepared, prepErr := prepareVerifiedScript(scriptBinding.EntryPath, scriptBinding.ContentSHA256)
			if prepErr != nil {
				return nil, prepErr
			}
			verifiedScript = prepared
			args = []string{verifiedScript.Path}
		}

		userEnv := resolveStringMapCredentials(s.params.env, s.credential)
		cmd := exec.Command(command, args...)
		if verifiedScript != nil {
			cmd.ExtraFiles = verifiedScript.ExtraFiles
		}
		if cwd != "" {
			cmd.Dir = cwd
		}
		// 始终显式设置 Env：剥离敏感父进程变量，并按档位收紧/前置卷内 runtime PATH。
		cmd.Env = runtime.BuildChildEnvWithOptions(os.Environ(), userEnv, policy, runtime.ChildEnvOptions{
			Mode:         eff.Mode,
			RuntimeDir:   currentRuntimeDir(),
			FileRoots:    eff.FileAccess.Paths,
			NetworkMode:  eff.Network.Mode,
			NetworkHosts: eff.Network.Hosts,
		}, pathPrefixes...)
		// 进程级加固（Linux: 进程组 + Pdeathsig；其他平台 no-op）。
		// 严格档 + bwrap：文件 bind 隔离；网络 deny 时 unshare-net。
		hardening := eff.ProcessHardening
		runtime.ApplySandbox(cmd, runtime.SandboxOptions{
			Enabled:      hardening,
			SecurityMode: eff.Mode,
			FileRoots:    eff.FileAccess.Paths,
			CWD:          cwd,
			NetworkMode:  eff.Network.Mode,
			NetworkHosts: eff.Network.Hosts,
			RuntimeDir:   currentRuntimeDir(),
		})
		transport := newCommandTransport(cmd, hardening)
		conn, err := connectWithTimeout(dialCtx, transport)
		if err != nil {
			// 仅发送进程组终止信号；SDK 的异步超时清理唯一负责 cmd.Wait。
			if hardening {
				runtime.TerminateProcessTree(cmd)
			}
			if verifiedScript != nil {
				verifiedScript.close()
			}
			return nil, err
		}
		// 包装 close：SDK 关闭后按进程组清理孙进程，并释放脚本 FD/快照。
		return &stdioClientConn{
			inner:          conn,
			cmd:            cmd,
			hardening:      hardening,
			verifiedScript: verifiedScript,
		}, nil
	})
}

// stdioClientConn 装饰 mcpClientConn，在 close 时清理进程树。
type stdioClientConn struct {
	inner          mcpClientConn
	cmd            *exec.Cmd
	hardening      bool
	verifiedScript *verifiedScriptLaunch
}

func (c *stdioClientConn) listTools(ctx context.Context) ([]domain.ToolDef, error) {
	return c.inner.listTools(ctx)
}

func (c *stdioClientConn) callTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	return c.inner.callTool(ctx, name, args)
}

func (c *stdioClientConn) close() error {
	var err error
	if c.inner != nil {
		err = c.inner.close()
	}
	if c.hardening {
		runtime.TerminateProcessTree(c.cmd)
	}
	if c.verifiedScript != nil {
		c.verifiedScript.close()
	}
	return err
}
