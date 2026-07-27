//go:build windows

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var windowsProcessJobs sync.Map // map[int]windows.Handle

// AttachProcessHardening binds a started process to a kill-on-close Job Object.
// The caller must invoke this immediately after cmd.Start and before the child
// can establish a long-lived session.
func AttachProcessHardening(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return fmt.Errorf("Windows 进程尚未启动")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("创建 Windows Job Object 失败：%w", err)
	}
	keepJob := false
	defer func() {
		if !keepJob {
			_ = windows.CloseHandle(job)
		}
	}()

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return fmt.Errorf("配置 Windows Job Object 失败：%w", err)
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return fmt.Errorf("打开 Windows 子进程失败：%w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("绑定 Windows 子进程到 Job Object 失败：%w", err)
	}
	windowsProcessJobs.Store(cmd.Process.Pid, job)
	keepJob = true
	return nil
}

// TerminateProcessTree Windows 使用系统 taskkill 按 PID 终止整棵进程树，并以直接 Kill 兜底。
// exec.Cmd 的 Wait 由 SDK 唯一负责，避免并发/重复回收。
func TerminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	if job, ok := windowsProcessJobs.LoadAndDelete(cmd.Process.Pid); ok {
		jobHandle := job.(windows.Handle)
		if err := windows.TerminateJobObject(jobHandle, 1); err != nil {
			slog.Default().Warn("Windows Job Object 终止失败", "pid", cmd.Process.Pid, "error", err)
		}
		_ = windows.CloseHandle(jobHandle)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return
	}
	if cmd.ProcessState != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	taskkill := filepath.Join(strings.TrimSpace(os.Getenv("SystemRoot")), "System32", "taskkill.exe")
	if strings.TrimSpace(os.Getenv("SystemRoot")) == "" {
		taskkill = filepath.Join(`C:\Windows`, "System32", "taskkill.exe")
	}
	if err := exec.CommandContext(ctx, taskkill, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
		slog.Default().Warn("Windows taskkill 终止进程树失败", "pid", cmd.Process.Pid, "error", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
