//go:build unix

package runtime

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// TerminateProcessTree 在会话关闭后清理 stdio 子进程树。
//
// Unix：若启用了独立进程组，向整个组发送 SIGTERM，短暂等待后再 SIGKILL。
// exec.Cmd 的 Wait 由 SDK 唯一负责；这里仅观察进程组，避免并发/重复回收主进程。
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		// 进程组未建立或信号失败时，至少终止 leader；SDK 仍负责 Wait。
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	deadline := time.Now().Add(2 * time.Second)
	for processGroupAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processGroupAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	// 兜底杀 leader（组信号失败时）；不调用 Wait。
	_ = cmd.Process.Kill()
}

func processGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ensureProcessStarted 供测试/兼容；unix 下无额外动作。
func processAlive(p *os.Process) bool {
	if p == nil {
		return false
	}
	// Signal 0 探测是否仍存在。
	return p.Signal(syscall.Signal(0)) == nil
}
