package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// CompactHandler is a custom slog.Handler that produces compact, human-readable
// log output in the format:
//
//	HH:MM:SS LEVEL  event.name  attr1:val1 attr2:val2 (Nms)
type CompactHandler struct {
	mu    sync.Mutex
	out   io.Writer
	attrs []slog.Attr
	group string
}

// Compile-time interface check.
var _ slog.Handler = (*CompactHandler)(nil)

func NewCompactHandler(out io.Writer) *CompactHandler {
	return &CompactHandler{out: out}
}

// Enabled implements slog.Handler. It returns true for levels >= INFO,
// filtering out DEBUG by default.
func (h *CompactHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *CompactHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var buf strings.Builder

	buf.WriteString(r.Time.Format("15:04:05.000"))
	buf.WriteByte(' ')
	buf.WriteString(r.Level.String())
	buf.WriteString("  ")
	buf.WriteString("[")
	buf.WriteString(r.Message)
	buf.WriteString("]")

	var attrParts []string
	var durationMs int64
	var hasDuration bool

	r.Attrs(func(a slog.Attr) bool {
		val := a.Value.Resolve()
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		part := formatAttr(key, val, &durationMs, &hasDuration)
		if part != "" {
			attrParts = append(attrParts, part)
		}
		return true
	})

	for _, a := range h.attrs {
		val := a.Value.Resolve()
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		part := formatAttr(key, val, &durationMs, &hasDuration)
		if part != "" {
			attrParts = append(attrParts, part)
		}
	}

	if len(attrParts) > 0 {
		buf.WriteString("  ")
		buf.WriteString(strings.Join(attrParts, " "))
	}

	if hasDuration {
		fmt.Fprintf(&buf, " +%dms", durationMs)
	}

	buf.WriteByte('\n')

	_, err := h.out.Write([]byte(buf.String()))
	return err
}

func (h *CompactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &CompactHandler{
		out:   h.out,
		attrs: newAttrs,
		group: h.group,
	}
}

func (h *CompactHandler) WithGroup(name string) slog.Handler {
	return &CompactHandler{
		out:   h.out,
		attrs: h.attrs,
		group: name,
	}
}

func formatAttr(key string, val slog.Value, durationMs *int64, hasDuration *bool) string {
	baseKey := key
	if dot := strings.LastIndex(baseKey, "."); dot >= 0 {
		baseKey = baseKey[dot+1:]
	}

	switch baseKey {
	case "task_id":
		s := val.String()
		return "task:" + truncateID(s)
	case "account_id", "account":
		s := val.String()
		return "acc:" + truncateID(s)
	case "attempt":
		if isOne(val) {
			return ""
		}
		return key + ":" + val.String()
	case "error_code":
		if val.String() == "eligible" {
			return ""
		}
		return key + ":" + val.String()
	case "duration_ms":
		*durationMs = val.Int64()
		*hasDuration = true
		return ""
	default:
		return key + ":" + val.String()
	}
}

func isOne(val slog.Value) bool {
	switch val.Kind() {
	case slog.KindInt64:
		return val.Int64() == 1
	case slog.KindUint64:
		return val.Uint64() == 1
	case slog.KindFloat64:
		return val.Float64() == 1
	case slog.KindString:
		return val.String() == "1"
	default:
		n, err := strconv.ParseInt(val.String(), 10, 64)
		return err == nil && n == 1
	}
}

func truncateID(s string) string {
	if len(s) > 8 {
		return "…" + s[len(s)-8:]
	}
	return s
}
