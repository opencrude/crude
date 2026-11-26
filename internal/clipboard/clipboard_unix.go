//go:build !windows

package clipboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"crude/internal/logger"
)

// A session has one clipboard owner: the display server it runs in. On
// Wayland only the Wayland clipboard is current. X11 tools read XWayland's
// copy, which a compositor can update late or not at all, so xclip in a
// Wayland session can return an older entry. Each session therefore reads
// with its own tool, as Neovim, Helix and atotto/clipboard do, and that
// tool's "nothing copied" answer is final: Crude never asks another
// clipboard, which could only hold older content.

// backend is the clipboard tool set of one kind of session.
type backend struct {
	// install names the package that provides the tools.
	install   string
	readText  []string
	readImage [][]string
	write     []string
	// empty holds the tool's reports that the clipboard has no content of
	// the requested type; such a read is readable and empty.
	empty []string
}

var (
	waylandBackend = backend{
		install:  "wl-clipboard",
		readText: []string{"wl-paste", "--no-newline", "--type", "text"},
		readImage: [][]string{
			{"wl-paste", "--no-newline", "--type", "image/png"},
			{"wl-paste", "--no-newline", "--type", "image/jpeg"},
		},
		write: []string{"wl-copy", "--type", "text/plain;charset=utf-8"},
		empty: []string{"nothing is copied", "not available as requested type", "no selection"},
	}
	xclipBackend = backend{
		install:   "xclip",
		readText:  []string{"xclip", "-selection", "clipboard", "-o"},
		readImage: [][]string{{"xclip", "-selection", "clipboard", "-o", "-target", "image/png"}},
		write:     []string{"xclip", "-selection", "clipboard"},
		empty:     []string{"not available"},
	}
	xselBackend = backend{
		install:  "xsel",
		readText: []string{"xsel", "--clipboard", "--output"},
		write:    []string{"xsel", "--clipboard", "--input"},
	}
	pasteboardBackend = backend{
		readText:  []string{"pbpaste"},
		readImage: [][]string{{"pngpaste", "-"}},
		write:     []string{"pbcopy"},
	}
	termuxBackend = backend{
		install:  "termux-api",
		readText: []string{"termux-clipboard-get"},
		write:    []string{"termux-clipboard-set"},
	}
)

// sessionBackends returns the tools that may read this session's clipboard
// and the tools that may write it, in order of preference. A Wayland session
// reads only its own clipboard; it may still write through XWayland, which
// the compositor forwards to Wayland clients.
func sessionBackends(goos string, getenv func(string) string) (readers, writers []backend) {
	var x11 []backend
	if getenv("DISPLAY") != "" {
		x11 = []backend{xclipBackend, xselBackend}
	}
	switch {
	case goos == "darwin":
		return []backend{pasteboardBackend}, []backend{pasteboardBackend}
	case getenv("WAYLAND_DISPLAY") != "":
		return []backend{waylandBackend}, append([]backend{waylandBackend}, x11...)
	case len(x11) > 0:
		return x11, x11
	case getenv("TERMUX_VERSION") != "":
		return []backend{termuxBackend}, []backend{termuxBackend}
	default:
		// No desktop session: Crude copies through the terminal instead.
		return nil, nil
	}
}

func read(t Format) ([]byte, error) {
	if t != FmtText && t != FmtImage {
		return nil, errUnsupported
	}
	readers, _ := sessionBackends(runtime.GOOS, os.Getenv)
	for _, b := range readers {
		commands := [][]string{b.readText}
		if t == FmtImage {
			commands = b.readImage
		}
		if len(commands) == 0 || !installed(commands[0][0]) {
			continue
		}
		// The session's clipboard answered; what it holds is final.
		return readFrom(b, t, commands)
	}
	if t == FmtImage {
		return nil, errUnsupported
	}
	logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "no_read_tool") })
	return nil, errUnavailable
}

func readFrom(b backend, t Format, commands [][]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var lastErr error
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.WaitDelay = 100 * time.Millisecond
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		if err == nil && (t == FmtText || out.Len() > 0) {
			logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "read_tool_ok", "tool", command[0], "bytes", out.Len()) })
			return out.Bytes(), nil
		}
		if err == nil || b.reportsEmpty(stderr.String()) {
			continue // Nothing of this type; an image read tries the next type.
		}
		lastErr = err
		logIf(func(lg *logger.Logger) {
			lg.Debug("CLIPBOARD", "read_tool_failed", "tool", command[0], "error", strings.TrimSpace(stderr.String()))
		})
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return []byte{}, nil
}

func (b backend) reportsEmpty(stderr string) bool {
	stderr = strings.ToLower(stderr)
	for _, marker := range b.empty {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}

func write(t Format, buf []byte) (bool, error) {
	if t != FmtText {
		return false, errUnsupported
	}
	_, writers := sessionBackends(runtime.GOOS, os.Getenv)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var lastErr error
	for _, b := range writers {
		if !installed(b.write[0]) {
			continue
		}
		// A writer forks to serve the selection. Its output is not read, so
		// the child does not hold a pipe that Run would wait for.
		cmd := exec.CommandContext(ctx, b.write[0], b.write[1:]...)
		cmd.WaitDelay = 100 * time.Millisecond
		cmd.Stdin = bytes.NewReader(buf)
		if lastErr = cmd.Run(); lastErr == nil {
			logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write_tool_ok", "tool", b.write[0]) })
			return true, nil
		}
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write_tool_failed", "tool", b.write[0]) })
	}
	logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "no_write_tool") })
	if lastErr != nil {
		return false, lastErr
	}
	return false, errUnavailable
}

func installed(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}

// MissingTool names the package to install so Crude can read this session's
// clipboard, or returns "" when a reader is installed or none is known. It
// looks up executables, so call it off the UI thread.
func MissingTool() string {
	readers, _ := sessionBackends(runtime.GOOS, os.Getenv)
	for _, b := range readers {
		if installed(b.readText[0]) {
			return ""
		}
	}
	if len(readers) == 0 {
		return ""
	}
	return readers[0].install
}
