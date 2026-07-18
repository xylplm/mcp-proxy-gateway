//go:build !linux

package runtime

import (
	"os/exec"
	"runtime"
)

func applySandboxPlatform(cmd *exec.Cmd) {
	// Windows / macOS / 其他：保持 no-op，避免 Job Object / 降权在未完整验证前影响主路径。
	_ = cmd
}

func describeSandboxPlatform() SandboxCapabilities {
	return SandboxCapabilities{
		ProcessHardeningSupported: false,
		Platform:                  runtime.GOOS,
		Description:               "当前平台使用策略层加固（命令白名单、环境清理、卷路径优先）；进程级隔离在 Linux 生产容器中启用。",
	}
}
