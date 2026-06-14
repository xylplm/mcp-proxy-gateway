package syslog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestHandlerCapturesDebugWithoutWritingToInfoConsole(t *testing.T) {
	var out bytes.Buffer
	store := NewStore(10)
	console := slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(NewHandler(console, store))

	logger.Debug("debug detail", "component", "test")
	logger.Info("service ready")

	logs := store.List(0, "", 10)
	if len(logs) != 2 || logs[0].Level != "debug" || logs[0].Attrs["component"] != "test" {
		t.Fatalf("内存日志未捕获完整记录：%+v", logs)
	}
	if strings.Contains(out.String(), "debug detail") {
		t.Fatalf("debug 日志不应写入 info 级控制台输出：%s", out.String())
	}
	if !strings.Contains(out.String(), "service ready") {
		t.Fatalf("info 日志应继续写入控制台输出：%s", out.String())
	}
}

func TestHandlerWithAttrsAndGroup(t *testing.T) {
	store := NewStore(10)
	h := NewHandler(nil, store).WithAttrs([]slog.Attr{slog.String("app", "gateway")}).WithGroup("http")
	rec := slog.NewRecord(timeNow(), slog.LevelWarn, "request slow", 0)
	rec.AddAttrs(slog.Int("status", 504))

	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle 不应返回错误：%v", err)
	}
	logs := store.List(0, "warn", 10)
	if len(logs) != 1 || logs[0].Attrs["app"] != "gateway" || logs[0].Attrs["http.status"] != int64(504) {
		t.Fatalf("WithAttrs/WithGroup 未正确记录：%+v", logs)
	}
}

// TestHandlerCapturesSource 验证通过 logger 方法记录的日志能捕获调用方源码位置。
func TestHandlerCapturesSource(t *testing.T) {
	store := NewStore(10)
	logger := slog.New(NewHandler(nil, store))

	logger.Info("source captured")
	logs := store.List(0, "info", 10)
	if len(logs) != 1 {
		t.Fatalf("期望 1 条日志，实际 %d", len(logs))
	}
	src := logs[0].Source
	if src == "" {
		t.Fatalf("通过 logger.Info 记录的日志应捕获调用方源码位置，实际为空")
	}
	// 应指向本测试文件，且含行号。
	if !strings.Contains(src, "handler_test.go") {
		t.Errorf("source 应指向 handler_test.go，实际 %q", src)
	}
	if !strings.Contains(src, ":") {
		t.Errorf("source 应含行号，实际 %q", src)
	}
}

func timeNow() time.Time { return time.Unix(10, 0).UTC() }
