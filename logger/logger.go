package logger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger provides session-scoped structured logging.
// Each session gets a unique directory: ~/.crude/sessions/{YYYY-MM-DD_HHMMSS_hex}/
// containing session.log (central log) and turns/ (LLM dump files).
type Logger struct {
	dir       string
	sessionID string
	w         *BufferedWriter
	turn      *TurnLogger
	mu        sync.Mutex
	closed    bool
	start     time.Time
}

// Options configures session logger creation.
type Options struct {
	HomeDir        string
	TurnLogEnabled bool
}

// New creates a new session logger with a unique session directory.
func New(opts Options) (*Logger, error) {
	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		opts.HomeDir = home
	}

	sessionID := generateSessionID()
	dir := filepath.Join(opts.HomeDir, ".crude", "sessions", sessionID)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("logger: create session dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "turns"), 0o755); err != nil {
		return nil, fmt.Errorf("logger: create turns dir: %w", err)
	}

	logPath := filepath.Join(dir, "session.log")
	w, err := NewBufferedWriter(logPath)
	if err != nil {
		return nil, fmt.Errorf("logger: create log file: %w", err)
	}

	now := time.Now()
	l := &Logger{
		dir:       dir,
		sessionID: sessionID,
		w:         w,
		turn: &TurnLogger{
			dir:     filepath.Join(dir, "turns"),
			enabled: opts.TurnLogEnabled,
		},
		start: now,
	}

	// Session header
	l.w.WriteString(fmt.Sprintf("# session %s | %s\n\n", sessionID, now.Format(time.RFC3339)))

	// Clean up old sessions
	sessionsDir := filepath.Join(opts.HomeDir, ".crude", "sessions")
	if removed, _ := CleanOldSessions(sessionsDir, 0, 0); removed > 0 {
		l.Info("SESSION", "cleanup", "removed", removed)
	}

	return l, nil
}

func generateSessionID() string {
	now := time.Now()
	b := make([]byte, 4)
	rand.Read(b)
	short := hex.EncodeToString(b)
	return fmt.Sprintf("%s_%s", now.Format("2006-01-02_150405"), short)
}

func (l *Logger) SessionID() string  { return l.sessionID }
func (l *Logger) SessionDir() string { return l.dir }
func (l *Logger) Turn() *TurnLogger  { return l.turn }

func (l *Logger) SetTurnEnabled(on bool) {
	if l.turn != nil {
		l.turn.SetEnabled(on)
	}
}

func (l *Logger) write(level, tag, msg string, pairs ...any) {
	if l.closed {
		return
	}
	l.w.WriteString(formatEntry(level, tag, msg, pairs...))
}

// Info logs an informational entry with tag and key=value pairs.
func (l *Logger) Info(tag, msg string, pairs ...any)  { if l == nil { return }; l.write("INFO", tag, msg, pairs...) }
func (l *Logger) Warn(tag, msg string, pairs ...any)  { if l == nil { return }; l.write("WARN", tag, msg, pairs...) }
func (l *Logger) Error(tag, msg string, pairs ...any) { if l == nil { return }; l.write("ERR!", tag, msg, pairs...) }
func (l *Logger) Debug(tag, msg string, pairs ...any) { if l == nil { return }; l.write("DBG", tag, msg, pairs...) }

// Close flushes buffers, writes session.end, and releases resources.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	duration := time.Since(l.start).Truncate(time.Millisecond)
	l.w.WriteString(formatEntry("INFO", "SESSION", "end", "duration", duration.String()))
	l.w.Flush()
	if l.turn != nil {
		l.turn.Close()
	}
	return l.w.Close()
}

// Remove deletes the entire session directory (for empty sessions).
func (l *Logger) Remove() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		l.w.Close()
		if l.turn != nil {
			l.turn.Close()
		}
	}
	return os.RemoveAll(l.dir)
}

// Format: "15:04:05.000 LEVEL [TAG] msg key=value key=value\n"
func formatEntry(level, tag, msg string, pairs ...any) string {
	var sb strings.Builder
	sb.WriteString(time.Now().Format("15:04:05.000"))
	sb.WriteByte(' ')
	sb.WriteString(level)
	sb.WriteString(" [")
	sb.WriteString(tag)
	sb.WriteString("] ")
	sb.WriteString(msg)
	for i := 0; i+1 < len(pairs); i += 2 {
		sb.WriteByte(' ')
		sb.WriteString(fmt.Sprintf("%v", pairs[i]))
		sb.WriteByte('=')
		sb.WriteString(formatValue(pairs[i+1]))
	}
	sb.WriteByte('\n')
	return sb.String()
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return quoteIfNeeded(val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%.10g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case time.Duration:
		return val.String()
	case fmt.Stringer:
		return quoteIfNeeded(val.String())
	default:
		return quoteIfNeeded(fmt.Sprintf("%v", val))
	}
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range s {
		if r <= 32 || r == '"' || r == '\\' || r == '=' || r == '[' || r == ']' {
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
			if r >= 32 && r < 127 {
				sb.WriteRune(r)
			} else {
				fmt.Fprintf(&sb, `\x%02x`, r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
