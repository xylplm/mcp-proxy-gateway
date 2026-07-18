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
	caps := SandboxCapabilities{
		ProcessHardeningSupported:    true,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		IsolationBackend:             "none",
		Platform:                     "linux",
		Description:                  "Linux：stdio 子进程使用独立进程组，并在网关进程退出时发送 SIGTERM。文件/网络为策略约束（非内核沙箱）。",
	}
	// Phase C：探测 bubblewrap；实际包装启用前仅用于管理台能力展示。
	if path, err := exec.LookPath("bwrap"); err == nil && path != "" {
		caps.FilesystemIsolationSupported = true
		caps.NetworkIsolationSupported = true
		caps.IsolationBackend = "bwrap"
		caps.Description = "Linux：进程组加固可用；检测到 bubblewrap，可在后续版本对严格档启用文件/网络命名空间隔离。"
	}
	return caps
}
