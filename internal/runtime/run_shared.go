package runtime

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
)

// maxCommandLineBytes 是单行输出的上限；超过则强制断行，避免无换行的长输出把
// 行缓冲无限撑大（总量另有 maxCommandOutput 约束）。
const maxCommandLineBytes = 1024 * 1024

// runBounded 执行一条已构造好的 exec.Cmd，逐行捕获 stdout/stderr 到有界缓冲。
//
// 这里用 cmd.Stdout/cmd.Stderr 而不是 cmd.StdoutPipe()：StdoutPipe 的契约要求
// 「所有读取完成后才能调用 Wait」，因为 Wait 会关闭管道。若先 Wait 再收尾读取，
// echo 这类瞬间退出的命令会整段丢失 stdout —— 表现为 uv pip list / npm ls 的
// 「解析输出失败」。改由 exec 自己管理管道与拷贝 goroutine，Wait 保证拷贝完成。
//
// 调用方负责：构造 cmd（含 SysProcAttr 进程组、Cancel、WaitDelay、Env、Dir）。
// 其中 WaitDelay 必须非零：孙进程继承管道写端时，靠它让 Wait 关闭管道并返回，
// 不会永久阻塞。
//
// logFn 非 nil 时，每行 stdout/stderr 会回调（用于依赖管理写 depLogs）。
func runBounded(cmd *exec.Cmd, logFn func(stream, line string)) (string, string, error) {
	stdoutBuf := newBoundedBuffer(maxCommandOutput)
	stderrBuf := newBoundedBuffer(maxCommandOutput)

	stdout := &lineSplitter{sink: func(line string) {
		stdoutBuf.appendLine(line)
		if logFn != nil {
			logFn("stdout", line)
		}
	}}
	stderr := &lineSplitter{sink: func(line string) {
		stderrBuf.appendLine(line)
		if logFn != nil {
			logFn("stderr", line)
		}
	}}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	// Wait 同时等待进程退出与 exec 内部的输出拷贝 goroutine 结束。
	waitErr := cmd.Wait()
	// 补交末尾没有换行的残行。
	stdout.flush()
	stderr.flush()
	return stdoutBuf.String(), stderrBuf.String(), waitErr
}

// lineSplitter 把写入流按行切分后交给 sink。
//
// exec 的拷贝 goroutine 串行调用 Write，但 flush 由调用方 goroutine 触发，
// 因此用互斥量保证两者对 pending 的访问安全。
type lineSplitter struct {
	mu      sync.Mutex
	pending []byte
	sink    func(line string)
}

func (ls *lineSplitter) Write(p []byte) (int, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.pending = append(ls.pending, p...)
	for {
		i := bytes.IndexByte(ls.pending, '\n')
		if i < 0 {
			break
		}
		ls.emitLocked(ls.pending[:i])
		ls.pending = ls.pending[i+1:]
	}
	// 超长且无换行时强制断行，避免 pending 无限增长。
	if len(ls.pending) >= maxCommandLineBytes {
		ls.emitLocked(ls.pending)
		ls.pending = nil
	}
	return len(p), nil
}

// flush 补交尾部残行；Wait 返回后调用。
func (ls *lineSplitter) flush() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.pending) > 0 {
		ls.emitLocked(ls.pending)
		ls.pending = nil
	}
}

func (ls *lineSplitter) emitLocked(line []byte) {
	ls.sink(strings.TrimSuffix(string(line), "\r"))
}
