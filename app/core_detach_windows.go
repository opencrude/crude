//go:build windows

package app

import (
	"fmt"
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

// CREATE_NEW_PROCESS_GROUP starts with Ctrl+C ignored. Clear that inherited
// policy before launching any native console children on their own ConPTY.
func initializeCoreSignals() error {
	result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0)
	if result == 0 {
		return fmt.Errorf("initialize core console control policy: %w", err)
	}
	return nil
}
