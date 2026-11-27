//go:build linux && !android

package clipboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	native "golang.design/x/clipboard"
)

const helperFlag = "--desktop-clipboard"

// RunHelper serves one desktop selection without opening a Crude profile.
// The copy owner lives until another application replaces its selection.
func RunHelper(args []string) (int, bool) {
	if len(args) != 2 || args[0] != helperFlag {
		return 0, false
	}
	action := args[1]
	if action != "text" && action != "image" && action != "copy" {
		return 1, true
	}
	x11 := func() (int, bool) {
		if err := runXSelection(action); err != nil {
			return 1, true
		}
		return 0, true
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return x11()
	}
	// Under Wayland the compositor owns the clipboard. KWin, wlroots,
	// Hyprland, niri and COSMIC offer data-control, so Crude reads it there;
	// their XWayland copy can be older.
	// Mutter (GNOME) and its forks have no data-control protocol, but they give
	// XWayland each new Wayland selection at once, so XWayland is current
	// there. Vim's default clipboard order is the same: Wayland, then X11.
	display := os.Getenv("DISPLAY")
	_ = os.Unsetenv("DISPLAY") // The library must not choose X11 by itself.
	if err := native.Init(); err != nil {
		if display == "" {
			return 1, true
		}
		_ = os.Setenv("DISPLAY", display)
		return x11()
	}
	ctx := context.Background()
	if action == "copy" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 1, true
		}
		changed, err := native.Write(ctx, native.FmtText, data)
		if err != nil {
			return 1, true
		}
		if _, err = os.Stdout.Write([]byte{'+'}); err != nil {
			return 1, true
		}
		_ = os.Stdout.Close()
		<-changed
		return 0, true
	}
	format := native.FmtText
	if action == "image" {
		format = native.FmtImage
	}
	data, err := native.Read(ctx, format)
	if action == "image" && errors.Is(err, native.ErrNoData) {
		data, err = native.Read(ctx, native.Register("image/jpeg"))
	}
	if errors.Is(err, native.ErrNoData) {
		return 0, true
	}
	if err != nil {
		return 1, true
	}
	_, err = os.Stdout.Write(data)
	if err != nil {
		return 1, true
	}
	return 0, true
}

// A separate client keeps selection ownership after the shell exits and bounds
// a stalled display server. The helper, not the shell, chooses the protocol.
func nativeCommand(ctx context.Context, action string) (*exec.Cmd, error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" || os.Getenv("TERMUX_VERSION") != "" {
		return nil, errUnavailable
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, exe, helperFlag, action), nil
}
func nativeRead(t Format) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	action := "text"
	if t == FmtImage {
		action = "image"
	}
	cmd, err := nativeCommand(ctx, action)
	if err != nil {
		return nil, err
	}
	return cmd.Output()
}
func nativeCopy(data []byte) error {
	cmd, err := nativeCommand(context.Background(), "copy")
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(data)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	timer := time.AfterFunc(2*time.Second, func() { _ = cmd.Process.Kill() })
	var ack [1]byte
	_, err = io.ReadFull(pipe, ack[:])
	ready := timer.Stop() && err == nil && ack[0] == '+'
	_ = pipe.Close()
	if !ready {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errUnavailable
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
