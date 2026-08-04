package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ErrRuntimeMissing 表示请求的运行时二进制在 runtime 卷或系统 PATH 上均未找到。
// 调用方可据此判断是否「跳过」依赖该运行时的操作（如语法检测），而非当作硬失败。
var ErrRuntimeMissing = errors.New("运行时未安装")

// runPublicTimeout 是 RunCommand 的默认超时（语法检测等短命令）。
const runPublicTimeout = 30 * time.Second

// RunCommand 在 runtime 环境下执行一条短命令（供脚本语法检测等使用），返回 stdout/stderr。
//
// 安全设施与 DependencyManager.runCommand 一致：独立进程组（超时杀整树）、
// 剥离敏感父进程 env + 注入包仓库镜像 + 前置 runtime PATH、stdout/stderr 2MiB 上限。
//
// base 为解释器基名（如 node / python3），通过 PathPrefixes 优先解析到 runtime 卷。
// 解析失败返回 ErrRuntimeMissing（包装），调用方可 errors.Is 判断后跳过。
// 不写依赖日志（区别于 DependencyManager.runCommand）。
func (s *Service) RunCommand(ctx context.Context, base string, args []string, cwd string) (string, string, error) {
	if s == nil {
		return "", "", ErrRuntimeMissing
	}
	runtimeDir := s.RuntimeDir()
	prefixes := PathPrefixes(runtimeDir)
	resolved, err := ResolveCommandWithPrefixes(base, prefixes)
	if err != nil {
		return "", "", fmt.Errorf("%w：%s", ErrRuntimeMissing, base)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runPublicTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, resolved, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	// 进程组：便于超时按组清理孙进程。
	ApplySandbox(cmd, SandboxOptions{
		Enabled:      true,
		SecurityMode: SecurityModeStandard,
		RuntimeDir:   runtimeDir,
		NetworkMode:  NetworkAccessInherit,
	})
	cmd.Cancel = func() error {
		terminateProcessGroup(cmd)
		return os.ErrProcessDone
	}
	cmd.WaitDelay = depKillGrace
	cmd.Env = BuildChildEnvWithOptions(os.Environ(), nil, s.Policy(), ChildEnvOptions{
		Mode:       SecurityModeStandard,
		RuntimeDir: runtimeDir,
	}, prefixes...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}

	stdoutBuf := newBoundedBuffer(maxCommandOutput)
	stderrBuf := newBoundedBuffer(maxCommandOutput)
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			stdoutBuf.appendLine(scanner.Text())
		}
		close(doneOut)
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			stderrBuf.appendLine(scanner.Text())
		}
		close(doneErr)
	}()

	waitErr := cmd.Wait()
	<-doneOut
	<-doneErr
	return stdoutBuf.String(), stderrBuf.String(), waitErr
}
