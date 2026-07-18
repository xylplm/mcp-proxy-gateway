//go:build darwin

package runtime

import (
	"os/exec"
	"syscall"
)

func applySandboxPlatform(cmd *exec.Cmd, _ SandboxOptions) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// 独立进程组供会话关闭时按组清理；macOS 不支持 Linux Pdeathsig / 无特权 bwrap。
	cmd.SysProcAttr.Setpgid = true
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported:    true,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		HostAllowlistEnforced:        false,
		IsolationBackend:             "none",
		Platform:                     "darwin",
		Description:                  "macOS：stdio 子进程使用独立进程组；文件与网络限制为策略约束（无特权内核沙箱）。",
	}
}
