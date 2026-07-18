//go:build unix

package runtime

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// TerminateProcessTree 在会话关闭后清理 stdio 子进程树。
//
// Linux/Unix：若启用了独立进程组，向整个组发送 SIGTERM，短暂等待后再 SIGKILL，
// 避免 npx/uvx 拉起的孙进程在主进程退出后残留。
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}

	// 先尝试按进程组终止（ApplySandbox 设置了 Setpgid）。
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
	}

	_ = syscall.Kill(-pid, syscall.SIGKILL)
	// 兜底杀 leader（组信号失败时）。
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
	}
}

// ensureProcessStarted 供测试/兼容；unix 下无额外动作。
func processAlive(p *os.Process) bool {
	if p == nil {
		return false
	}
	// Signal 0 探测是否仍存在。
	return p.Signal(syscall.Signal(0)) == nil
}
