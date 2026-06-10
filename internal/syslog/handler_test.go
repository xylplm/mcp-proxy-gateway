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

func timeNow() time.Time { return time.Unix(10, 0).UTC() }
