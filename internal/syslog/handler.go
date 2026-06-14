package syslog

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Handler tees slog records into Store while preserving the wrapped handler output.
type Handler struct {
	next   slog.Handler
	store  *Store
	attrs  []slog.Attr
	groups []string
}

func NewHandler(next slog.Handler, store *Store) *Handler {
	return &Handler{next: next, store: store}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.store != nil || (h.next != nil && h.next.Enabled(ctx, level))
}

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	if h.store != nil {
		h.store.Add(rec.Level.String(), rec.Message, rec.Time, sourceOf(rec), h.recordAttrs(rec))
	}
	if h.next != nil && h.next.Enabled(ctx, rec.Level) {
		return h.next.Handle(ctx, rec)
	}
	return nil
}

// sourceOf extracts a short caller location (e.g. "service/subscribe_service.go:196")
// from the record's program counter. Returns empty when the PC is unavailable.
func sourceOf(rec slog.Record) string {
	frames := runtime.CallersFrames([]uintptr{rec.PC})
	frame, _ := frames.Next()
	if frame.File == "" {
		return ""
	}
	// 保留末两级路径，使标识稳定且足够定位，避免全路径过长。
	file := frame.File
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		if prev := strings.LastIndex(file[:idx], "/"); prev >= 0 {
			file = file[prev+1:]
		} else {
			file = file[idx+1:]
		}
	}
	return file + ":" + strconv.Itoa(frame.Line)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := h.clone()
	if clone.next != nil {
		clone.next = clone.next.WithAttrs(attrs)
	}
	for _, attr := range attrs {
		flattenAttr(&clone.attrs, clone.groups, attr)
	}
	return clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	clone := h.clone()
	if clone.next != nil {
		clone.next = clone.next.WithGroup(name)
	}
	if name != "" {
		clone.groups = append(clone.groups, name)
	}
	return clone
}

func (h *Handler) clone() *Handler {
	clone := &Handler{
		next:  h.next,
		store: h.store,
	}
	if len(h.attrs) > 0 {
		clone.attrs = append([]slog.Attr{}, h.attrs...)
	}
	if len(h.groups) > 0 {
		clone.groups = append([]string{}, h.groups...)
	}
	return clone
}

func (h *Handler) recordAttrs(rec slog.Record) map[string]any {
	attrs := make([]slog.Attr, 0, len(h.attrs)+rec.NumAttrs())
	attrs = append(attrs, h.attrs...)
	rec.Attrs(func(attr slog.Attr) bool {
		flattenAttr(&attrs, h.groups, attr)
		return true
	})
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		out[attr.Key] = attrValue(attr.Value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func flattenAttr(dst *[]slog.Attr, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		nestedGroups := groups
		if attr.Key != "" {
			nestedGroups = append(append([]string{}, groups...), attr.Key)
		}
		for _, child := range attr.Value.Group() {
			flattenAttr(dst, nestedGroups, child)
		}
		return
	}
	if attr.Key == "" {
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(append(append([]string{}, groups...), attr.Key), ".")
	}
	*dst = append(*dst, slog.Attr{Key: key, Value: attr.Value})
}

func attrValue(value slog.Value) any {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindAny:
		switch v := value.Any().(type) {
		case nil:
			return nil
		case error:
			return v.Error()
		case fmt.Stringer:
			return v.String()
		case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return v
		case time.Time:
			return v.Format(time.RFC3339Nano)
		default:
			return fmt.Sprint(v)
		}
	case slog.KindGroup:
		group := make(map[string]any)
		for _, attr := range value.Group() {
			flattenGroupAttr(group, attr)
		}
		return group
	default:
		return fmt.Sprint(value.Any())
	}
}

func flattenGroupAttr(out map[string]any, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Key == "" {
		return
	}
	out[attr.Key] = attrValue(attr.Value)
}
