//go:build !linux && !darwin

package runtime

import (
	"os/exec"
	"runtime"
)

func applySandboxPlatform(cmd *exec.Cmd, _ SandboxOptions) {
	// Windows / 其他：保持 no-op，避免 Job Object / 降权在未完整验证前影响主路径。
	_ = cmd
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported:    false,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		HostAllowlistEnforced:        false,
		IsolationBackend:             "none",
		Platform:                     runtime.GOOS,
		Description:                  "当前平台使用策略层加固（命令白名单、环境清理、安全档位、卷路径优先）；进程级隔离与 bwrap 文件/网络隔离在 Linux 生产容器中启用。",
	}
}
