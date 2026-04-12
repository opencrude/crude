package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type BlockHandler struct {
	mu     sync.Mutex
	out    io.Writer
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func NewBlockHandler(out io.Writer, level slog.Level) *BlockHandler {
	return &BlockHandler{
		out:   out,
		level: level,
	}
}

func (h *BlockHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *BlockHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var sb strings.Builder

	sb.WriteString("\u25ba ")

	sb.WriteString(record.Time.UTC().Format("2006-01-02T15:04:05.000Z"))

	sb.WriteString(" ")

	switch {
	case record.Level >= slog.LevelError:
		sb.WriteString("ERROR")
	case record.Level >= slog.LevelWarn:
		sb.WriteString(" WARN")
	case record.Level >= slog.LevelInfo:
		sb.WriteString(" INFO")
	default:
		sb.WriteString("DEBUG")
	}

	sb.WriteString("  ")

	msg := record.Message
	sb.WriteString(msg)

	padding := 16 - utf8.RuneCountInString(msg)
	if padding < 1 {
		padding = 1
	}
	for i := 0; i < padding; i++ {
		sb.WriteString(" ")
	}

	var contentVal string
	var contentFound bool

	allAttrs := h.attrs
	record.Attrs(func(attr slog.Attr) bool {
		allAttrs = append(allAttrs, attr)
		return true
	})

	first := true
	for _, attr := range allAttrs {
		if attr.Key == "content" {
			contentVal = attr.Value.String()
			contentFound = true
			continue
		}

		key := attr.Key
		if len(h.groups) > 0 {
			key = strings.Join(h.groups, ".") + "." + key
		}

		if !first {
			sb.WriteString(" ")
		}
		first = false

		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(logfmtValue(attr.Value))
	}

	sb.WriteString("\n")

	if contentFound {
		for _, line := range strings.Split(contentVal, "\n") {
			sb.WriteString("  | ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	_, err := io.WriteString(h.out, sb.String())
	return err
}

func (h *BlockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &BlockHandler{
		out:    h.out,
		level:  h.level,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

func (h *BlockHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups), len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups = append(newGroups, name)
	return &BlockHandler{
		out:    h.out,
		level:  h.level,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

func logfmtValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return logfmtQuote(v.String())
	case slog.KindInt64:
		return fmt.Sprintf("%d", v.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", v.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%g", v.Float64())
	case slog.KindBool:
		return fmt.Sprintf("%t", v.Bool())
	case slog.KindDuration:
		return fmt.Sprintf("%d", v.Duration())
	case slog.KindTime:
		return logfmtQuote(v.Time().Format(time.RFC3339Nano))
	case slog.KindAny:
		return logfmtQuote(fmt.Sprintf("%v", v.Any()))
	default:
		return logfmtQuote(v.String())
	}
}

func logfmtQuote(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range s {
		if r <= unicode.MaxASCII && (unicode.IsSpace(r) || r == '"' || r == '\\' || r == '=' || r == '<' || r == '>') || !unicode.IsPrint(r) {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if unicode.IsPrint(r) {
				sb.WriteRune(r)
			} else {
				fmt.Fprintf(&sb, `\x%02x`, r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
