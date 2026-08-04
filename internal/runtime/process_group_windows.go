//go:build windows

package runtime

import "os/exec"

// terminateProcessGroup 在 Windows 上回退到 Kill leader（依赖管理命令超时/取消时）。
// Windows 进程组清理由 ApplySandbox 的 Job Object 负责（TerminateJobObject 在进程退出时
// 由系统级联终止子进程）；Cancel 阶段强制结束 leader 即可触发 Job 收尾。
func terminateProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
