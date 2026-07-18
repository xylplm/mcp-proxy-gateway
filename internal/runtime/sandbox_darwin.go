//go:build darwin

package runtime

import (
	"os/exec"
	"syscall"
)

func applySandboxPlatform(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// 独立进程组供会话关闭时按组清理；macOS 不支持 Linux Pdeathsig。
	cmd.SysProcAttr.Setpgid = true
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported:    true,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		IsolationBackend:             "none",
		Platform:                     "darwin",
		Description:                  "macOS：stdio 子进程使用独立进程组；文件与网络限制为策略约束（非内核沙箱）。",
	}
}
