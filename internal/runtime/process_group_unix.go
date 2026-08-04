//go:build unix

package runtime

import (
	"errors"
	"os/exec"
	"syscall"
)

// terminateProcessGroup 向独立进程组（Setpgid）发送 SIGTERM，用于依赖管理命令
// 超时/取消时优雅终止 npm/uv 及其孙进程。失败时回退到仅终止 leader。
//
// 与 TerminateProcessTree 不同：这里不调用 Wait（exec.CommandContext 的 Cancel
// 契约要求只发信号，Wait 由 exec 运行时在 WaitDelay 后负责）。
func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	// 向整个进程组发 SIGTERM（负 pid = 进程组）。
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		// 进程组未建立或失败时，至少终止 leader。
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
