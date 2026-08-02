package clipboard

import (
	"errors"
	"sync"

	"crude/logger"
)

var (
	errUnavailable = errors.New("clipboard unavailable")
	errUnsupported = errors.New("unsupported format")

	pkgLogger   *logger.Logger
	pkgLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	pkgLoggerMu.Lock()
	defer pkgLoggerMu.Unlock()
	pkgLogger = lg
}

func logIf(fn func(lg *logger.Logger)) {
	pkgLoggerMu.RLock()
	lg := pkgLogger
	pkgLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

type Format int

const (
	FmtText Format = iota
	FmtImage
)

var (
	lock      sync.Mutex
	initError error
)

func Read(t Format) []byte {
	lock.Lock()
	defer lock.Unlock()

	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "read", "format", t) })
	buf, err := read(t)
	if err != nil {
		logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "read_failed", "error", err.Error()) })
		return nil
	}
	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "read_ok", "bytes", len(buf)) })
	return buf
}

func WriteText(text string) error {
	return Write(FmtText, []byte(text))
}

func Write(t Format, buf []byte) error {
	lock.Lock()
	defer lock.Unlock()

	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write", "format", t, "bytes", len(buf)) })
	_, err := write(t, buf)
	if err != nil {
		logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "write_failed", "error", err.Error()) })
	} else {
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write_ok") })
	}
	return err
}

func GetError() error {
	return initError
}
