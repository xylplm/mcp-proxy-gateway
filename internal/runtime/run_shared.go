package runtime

import (
	"bufio"
	"os/exec"
	"time"
)

// drainGrace 是 cmd.Wait() 返回后，等待 stdout/stderr 读取 goroutine 退出的上限。
// 防止 npm/uv 的孙进程继承了管道写端、进程组被杀后仍持有管道导致读取 goroutine 永久阻塞。
const drainGrace = 5 * time.Second

// runBounded 执行一条已构造好的 exec.Cmd，逐行捕获 stdout/stderr 到有界缓冲，
// 并在 Wait 返回后限时等待读取 goroutine 退出（避免管道被孙进程持有时泄漏 goroutine）。
//
// 调用方负责：构造 cmd（含 SysProcAttr 进程组、Cancel、WaitDelay、Env、Dir）。
// 本函数仅负责启动、读取、限时回收。
//
// logFn 非 nil 时，每行 stdout/stderr 会回调（用于依赖管理写 depLogs）。
func runBounded(cmd *exec.Cmd, logFn func(stream, line string)) (string, string, error) {
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
			line := scanner.Text()
			stdoutBuf.appendLine(line)
			if logFn != nil {
				logFn("stdout", line)
			}
		}
		close(doneOut)
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuf.appendLine(line)
			if logFn != nil {
				logFn("stderr", line)
			}
		}
		close(doneErr)
	}()

	waitErr := cmd.Wait()
	// 限时等待读取 goroutine：若孙进程持有管道，避免永久阻塞泄漏 goroutine 与锁。
	drainWithTimeout(doneOut)
	drainWithTimeout(doneErr)
	return stdoutBuf.String(), stderrBuf.String(), waitErr
}

// drainWithTimeout 等待 channel 关闭，最多 drainGrace；超时返回（放弃该 goroutine）。
func drainWithTimeout(ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(drainGrace):
	}
}
