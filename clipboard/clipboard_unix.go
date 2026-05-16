//go:build !windows

package clipboard

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"crude/logger"
)

func read(t Format) ([]byte, error) {
	if t != FmtText {
		return nil, errUnsupported
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tools := [][]string{
		{"wl-paste", "--no-newline"},
		{"pbpaste"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"termux-clipboard-get"},
	}

	for _, tool := range tools {
		if len(tool) == 0 {
			continue
		}
		cmd := exec.CommandContext(ctx, tool[0], tool[1:]...)
		var out bytes.Buffer
		cmd.Stdout = &out
		if cmd.Run() == nil {
			logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "read_tool_ok", "tool", tool[0], "bytes", out.Len()) })
			return out.Bytes(), nil
		}
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "read_tool_failed", "tool", tool[0]) })
	}

	logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "no_read_tool") })
	return nil, errUnavailable
}

func write(t Format, buf []byte) (bool, error) {
	if t != FmtText {
		return false, errUnsupported
	}

	tools := [][]string{
		{"wl-copy"},
		{"pbcopy"},
		{"xclip", "-selection", "clipboard"},
		{"termux-clipboard-set"},
	}

	for _, tool := range tools {
		if len(tool) == 0 {
			continue
		}
		cmd := exec.Command(tool[0], tool[1:]...)
		cmd.Stdin = bytes.NewReader(buf)
		if cmd.Run() == nil {
			logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write_tool_ok", "tool", tool[0]) })
			return true, nil
		}
		logIf(func(lg *logger.Logger) { lg.Debug("CLIPBOARD", "write_tool_failed", "tool", tool[0]) })
	}

	logIf(func(lg *logger.Logger) { lg.Warn("CLIPBOARD", "no_write_tool") })
	return false, errUnavailable
}
