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
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}
