//go:build windows

package runtime

import (
	"os/exec"
	"runtime"
	"syscall"
)

func applySandboxPlatform(cmd *exec.Cmd, _ SandboxOptions) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Job Object provides lifetime cleanup; a new process group also keeps
	// console-control semantics separate from the gateway process.
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported:    true,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		HostAllowlistEnforced:        false,
		IsolationBackend:             "job",
		Platform:                     runtime.GOOS,
		Description:                  "Windows：stdio 子进程绑定 Job Object，网关退出时自动清理进程树；文件与网络限制为策略约束。",
	}
}
