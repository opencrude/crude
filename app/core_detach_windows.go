//go:build windows

package app

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachedProcAttr starts the core without a console and outside the shell's
// process group, so closing the originating console does not stop it.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		// DETACHED_PROCESS disables CREATE_NO_WINDOW and lets children create consoles.
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
