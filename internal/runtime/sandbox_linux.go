//go:build linux

package runtime

import (
	"os/exec"
	"syscall"
)

func applySandboxPlatform(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// 独立进程组：便于后续会话关闭时按组清理；不改变 SDK Close 语义。
	cmd.SysProcAttr.Setpgid = true
	// 父进程（网关）异常退出时终止子进程，降低孤儿 MCP 进程残留。
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported: true,
		Platform:                  "linux",
		Description:               "Linux：stdio 子进程使用独立进程组，并在网关进程退出时发送 SIGTERM。",
	}
}
