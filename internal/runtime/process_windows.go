//go:build windows

package runtime

import (
	"os/exec"
	"time"
)

// TerminateProcessTree Windows 尽力终止子进程（无进程组 API 时退化为 Process.Kill）。
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
