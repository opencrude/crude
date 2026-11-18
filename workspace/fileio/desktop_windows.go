package fileio

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Explorer parses /select, itself, rather than using CommandLineToArgvW.
func explorerSelectArgument(path string) (string, error) {
	// A quote cannot occur in a Windows filename and cannot be safely embedded
	// in Explorer's quoted operand.
	if strings.ContainsRune(path, '"') {
		return "", fmt.Errorf("invalid quote in Windows path")
	}
	return `/select,"` + path + `"`, nil
}

func platformOpenBrowser(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return platformOpenPath(ctx, rawURL, false)
}

func platformOpenPath(ctx context.Context, path string, reveal bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	var parameters *uint16
	if reveal {
		var arg string
		arg, err = explorerSelectArgument(path)
		if err != nil {
			return err
		}
		parameters, err = windows.UTF16PtrFromString(arg)
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
