package transport

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// stderrCapture 是挂在 stdio 子进程 cmd.Stderr 上的有界写入器，只保留最近写入的
// 尾部字节。它用于在连接建立失败时把子进程的真实崩溃原因（如 Python 的
// ModuleNotFoundError、配置缺失提示）回传给上层，而不是只暴露 SDK 侧的 EOF。
//
// 为什么保留尾部而非头部：解释器/框架崩溃时，关键错误行（traceback 末行、
// "No config file found" 等）通常出现在 stderr 末尾，头部多为无关的启动噪声。
//
// 为什么另写一份而不复用 runtime.boundedBuffer：后者填满后丢弃新行（保留头部），
// 语义与诊断需求相反；且它是 runtime 包私有类型。这里的实现很小，独立更清晰。
//
// 并发说明：Go 的 os/exec 在 cmd.Stderr 为非 *os.File 时会用独立 goroutine 把子进程
// 的 stderr 拷贝到 Write；tail 可能在另一 goroutine 读取，故以互斥保护。连接失败
// （多为 stdout EOF）时子进程已退出、fd 已关闭，拷贝 goroutine 已写完崩溃日志。
type stderrCapture struct {
	mu    sync.Mutex
	buf   []byte
	limit int
	// passthrough 非 nil 时，写入的数据同时透传给它。用于保留「子进程 stderr 继承
	// 到网关标准错误」的原有运维可见性，避免捕获后被静默吞掉。
	passthrough io.Writer
}

// stderrCaptureLimit 是 stderr 尾部保留的字节上限，足以容纳一段完整的
// traceback，同时避免 chatty 子进程占用过多内存。
const stderrCaptureLimit = 8 * 1024

func newStderrCapture() *stderrCapture {
	return &stderrCapture{limit: stderrCaptureLimit, passthrough: os.Stderr}
}

// Write 追加数据并在超过上限时丢弃最旧的字节，始终保留最近 limit 字节；同时透传给
// passthrough（如有）。它满足 io.Writer，可直接赋给 exec.Cmd.Stderr。
func (c *stderrCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.limit {
		c.buf = c.buf[len(c.buf)-c.limit:]
	}
	pt := c.passthrough
	c.mu.Unlock()
	// 透传在锁外进行，避免慢速目标阻塞其他调用；透传失败不影响捕获与子进程运行。
	if pt != nil {
		_, _ = pt.Write(p)
	}
	return len(p), nil
}

// tail 返回已捕获的 stderr 文本（去除首尾空白）。无输出时返回空串。
func (c *stderrCapture) tail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(string(c.buf))
}

// stderrTailMaxRunes 是并入错误消息的 stderr 尾部长度上限（按字符）。lastError 会
// 落到运行期状态并渲染到前端提示，过长不利于展示；末尾若干行足以定位崩溃原因。
const stderrTailMaxRunes = 600

// withStderrTail 在连接失败错误上附加子进程 stderr 的尾部，便于上层与前端定位
// 真实崩溃原因。tail 为空时原样返回 err，不做任何修饰。
//
// 用 %w 包裹以保留原错误的 Unwrap 链，使连接管理器仍能通过 errors.Is 识别
// io.EOF 等会话终态；仅在人类可读消息上追加 stderr 片段。
func withStderrTail(err error, tail string) error {
	tail = strings.TrimSpace(tail)
	if err == nil || tail == "" {
		return err
	}
	if trimmed := []rune(tail); len(trimmed) > stderrTailMaxRunes {
		// 保留尾部（最新的错误行通常在末尾）。
		tail = "…" + string(trimmed[len(trimmed)-stderrTailMaxRunes:])
	}
	return fmt.Errorf("%w（子进程 stderr：%s）", err, tail)
}
