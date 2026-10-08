//go:build linux

package lsp

import (
	"syscall"
	"unsafe"
)

func waitProcessExit(pid int) error {
	var info [128]byte
	for {
		_, _, err := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(pid), uintptr(unsafe.Pointer(&info[0])), syscall.WEXITED|0x01000000 /* WNOWAIT */, 0, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != 0 {
			return err
		}
		return nil
	}
}
