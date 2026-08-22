package transport

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestStderrCaptureRetainsTail 验证有界写入器保留最近字节、丢弃最旧字节。
func TestStderrCaptureRetainsTail(t *testing.T) {
	t.Parallel()
	c := &stderrCapture{limit: 10}
	if _, err := io.WriteString(c, "0123456789ABCDE"); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	got := c.tail()
	if got != "56789ABCDE" {
		t.Fatalf("应保留末尾 limit 字节（去空白后），实际=%q", got)
	}
}

// TestStderrCaptureMultipleWrites 验证跨多次 Write 累积并只保留尾部。
func TestStderrCaptureMultipleWrites(t *testing.T) {
	t.Parallel()
	c := newStderrCapture()
	c.Write([]byte("first line\n"))
	c.Write([]byte("ModuleNotFoundError: No module named 'mcp'\n"))
	if !strings.Contains(c.tail(), "ModuleNotFoundError") {
		t.Fatalf("应保留最新崩溃行，实际=%q", c.tail())
	}
}

// TestWithStderrTailPreservesUnwrap 验证附加 stderr 后仍可通过 errors.Is 识别根因，
// 使连接管理器对 io.EOF 等终态的判定不被破坏。
func TestWithStderrTailPreservesUnwrap(t *testing.T) {
	t.Parallel()
	wrapped := withStderrTail(io.EOF, "ModuleNotFoundError: No module named 'mcp'")
	if !errors.Is(wrapped, io.EOF) {
		t.Fatal("包裹后应仍可 errors.Is 识别 io.EOF")
	}
	if !strings.Contains(wrapped.Error(), "ModuleNotFoundError") {
		t.Fatalf("错误消息应包含 stderr 片段，实际=%q", wrapped.Error())
	}
}

// TestWithStderrTailEmptyTailUnchanged 验证 tail 为空时不修饰原错误。
func TestWithStderrTailEmptyTailUnchanged(t *testing.T) {
	t.Parallel()
	if got := withStderrTail(io.EOF, ""); got != io.EOF {
		t.Fatalf("空 tail 应原样返回，实际=%v", got)
	}
	if got := withStderrTail(io.EOF, "   \n  "); got != io.EOF {
		t.Fatalf("纯空白 tail 经 stderrCapture.tail 已 Trim 为空，此处也应原样返回，实际=%v", got)
	}
}

// TestWithStderrTailTruncates 验证超长 stderr 尾部被截断并保留末尾。
func TestWithStderrTailTruncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", stderrTailMaxRunes+200) + "TAIL_MARKER"
	wrapped := withStderrTail(io.EOF, long)
	msg := wrapped.Error()
	if !strings.Contains(msg, "TAIL_MARKER") {
		t.Fatal("截断应保留尾部标记")
	}
	if !strings.Contains(msg, "…") {
		t.Fatal("截断应带省略前缀")
	}
}

// TestWithStderrTailNilError 验证 nil 错误不被包裹。
func TestWithStderrTailNilError(t *testing.T) {
	t.Parallel()
	if got := withStderrTail(nil, "anything"); got != nil {
		t.Fatalf("nil 错误应返回 nil，实际=%v", got)
	}
}
