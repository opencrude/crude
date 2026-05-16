//go:build windows

package clipboard

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.MustLoadDLL("user32")
	kernel32 = syscall.MustLoadDLL("kernel32")

	openClipboard              = user32.MustFindProc("OpenClipboard")
	closeClipboard             = user32.MustFindProc("CloseClipboard")
	emptyClipboard             = user32.MustFindProc("EmptyClipboard")
	getClipboardData           = user32.MustFindProc("GetClipboardData")
	setClipboardData           = user32.MustFindProc("SetClipboardData")

	gLock   = kernel32.MustFindProc("GlobalLock")
	gUnlock = kernel32.MustFindProc("GlobalUnlock")
	gAlloc  = kernel32.MustFindProc("GlobalAlloc")
	gFree   = kernel32.MustFindProc("GlobalFree")
	memMove = kernel32.MustFindProc("RtlMoveMemory")
)

const (
	cFmtUnicodeText = uintptr(13)
	gmemMoveable    = uintptr(0x0002)
)

func openClipboardWithRetry() error {
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		r, _, _ := openClipboard.Call(0)
		if r != 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "open_retry_exhausted", "retries", maxRetries) })
	return fmt.Errorf("failed to open clipboard after retries")
}

func read(t Format) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_read", "format", t) })

	if t != FmtText {
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_read_unsupported", "format", t) })
		return nil, errUnsupported
	}

	if err := openClipboardWithRetry(); err != nil {
		return nil, err
	}
	defer closeClipboard.Call()

	return readText()
}

func readText() ([]byte, error) {
	hMem, _, _ := getClipboardData.Call(cFmtUnicodeText)
	if hMem == 0 {
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_read_no_data") })
		return nil, errUnavailable
	}

	p, _, _ := gLock.Call(hMem)
	if p == 0 {
		logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "win_read_lock_failed") })
		return nil, errUnavailable
	}
	defer gUnlock.Call(hMem)

	const maxChars = 1 << 20
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(p)), maxChars)

	n := 0
	for buf[n] != 0 {
		n++
	}

	result := []byte(string(utf16.Decode(buf[:n])))
	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_read_ok", "chars", n, "bytes", len(result)) })
	return result, nil
}

func write(t Format, buf []byte) (bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if t != FmtText {
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_write_unsupported", "format", t) })
		return false, errUnsupported
	}

	if err := openClipboardWithRetry(); err != nil {
		return false, err
	}
	defer closeClipboard.Call()

	r, _, err := emptyClipboard.Call()
	if r == 0 {
		logIf(func(lg *logger.Logger) { lg.Error("CLIPBOARD", "win_clear_failed", "error", err.Error()) })
		return false, fmt.Errorf("failed to clear clipboard: %w", err)
	}

	if len(buf) == 0 {
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_write_empty") })
		return true, nil
	}

	s, err := syscall.UTF16FromString(string(buf))
	if err != nil {
		logIf(func(lg *logger.Logger) { lg.Error("CLIPBOARD", "win_write_utf16_failed", "error", err.Error()) })
		return false, fmt.Errorf("failed to convert string: %w", err)
	}

	hMem, _, err := gAlloc.Call(gmemMoveable, uintptr(len(s)*2))
	if hMem == 0 {
		logIf(func(lg *logger.Logger) { lg.Error("CLIPBOARD", "win_write_alloc_failed", "error", err.Error()) })
		return false, fmt.Errorf("failed to alloc memory: %w", err)
	}

	p, _, err := gLock.Call(hMem)
	if p == 0 {
		gFree.Call(hMem)
		logIf(func(lg *logger.Logger) { lg.Error("CLIPBOARD", "win_write_lock_failed", "error", err.Error()) })
		return false, fmt.Errorf("failed to lock memory: %w", err)
	}

	memMove.Call(p, uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)*2))

	gUnlock.Call(hMem)

	v, _, err := setClipboardData.Call(cFmtUnicodeText, hMem)
	if v == 0 {
		gFree.Call(hMem)
		logIf(func(lg *logger.Logger) { lg.Error("CLIPBOARD", "win_write_set_failed", "error", err.Error()) })
		return false, fmt.Errorf("failed to set clipboard data: %w", err)
	}

	logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "win_write_ok", "bytes", len(buf)) })
	return true, nil
}