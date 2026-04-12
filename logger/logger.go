package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type Logger struct {
	*slog.Logger
	fw   *FileWriter
	path string
}

type Config struct {
	SessionDir string
	SessionID  string
	Console    bool
	Level      slog.Level
}

func New(cfg Config) (*Logger, error) {
	if cfg.SessionDir == "" {
		cfg.SessionDir = defaultSessionDir()
	}
	if cfg.SessionID == "" {
		cfg.SessionID = generateID()
	}
	if cfg.Level == 0 {
		cfg.Level = slog.LevelDebug
	}

	logPath := filepath.Join(cfg.SessionDir, cfg.SessionID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("logger: create session dir: %w", err)
	}

	fw, err := NewFileWriter(logPath, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logger: create log file: %w", err)
	}

	var handler slog.Handler = NewBlockHandler(fw, cfg.Level)
	if cfg.Console {
		handler = newMultiHandler(NewBlockHandler(fw, cfg.Level), NewBlockHandler(os.Stderr, cfg.Level))
	}

	slg := slog.New(handler)
	slog.SetDefault(slg)

	l := &Logger{
		Logger: slg,
		fw:     fw,
		path:   logPath,
	}

	return l, nil
}

func (l *Logger) Path() string {
	return l.path
}

func (l *Logger) Close() error {
	if l.fw != nil {
		return l.fw.Close()
	}
	return nil
}

func defaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".crude", "sessions")
}

func generateID() string {
	return fmt.Sprintf("session-%s", time.Now().Format("20060102-150405"))
}

type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for _, h := range m.handlers {
		if h.Enabled(ctx, record.Level) {
			if err := h.Handle(ctx, record); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithAttrs(attrs)
	}
	return newMultiHandler(newHandlers...)
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		newHandlers[i] = h.WithGroup(name)
	}
	return newMultiHandler(newHandlers...)
}
