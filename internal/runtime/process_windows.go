//go:build windows

package runtime

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// TerminateProcessTree Windows 使用系统 taskkill 按 PID 终止整棵进程树，并以直接 Kill 兜底。
// exec.Cmd 的 Wait 由 SDK 唯一负责，避免并发/重复回收。
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	_ = cmd.Process.Kill()
}
