package fileio

import (
	"context"
	"syscall"

	"golang.org/x/sys/windows"
)

func platformOpenBrowser(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return platformOpenPath(rawURL, false)
}

func platformOpenPath(path string, reveal bool) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	var parameters *uint16
	if reveal {
		parameters, err = windows.UTF16PtrFromString("/select," + syscall.EscapeArg(path))
		if err != nil {
			return err
		}
		path = "explorer.exe"
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// ShellExecute resolves file associations without cmd.exe, PowerShell or a
	// rundll32 command string. Filenames cannot become shell instructions.
	return windows.ShellExecute(0, verb, file, parameters, nil, windows.SW_SHOWNORMAL)
}
