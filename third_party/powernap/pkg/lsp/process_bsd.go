//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package lsp

import (
	"errors"
	"fmt"
	"syscall"
)

func waitProcessExit(pid int) (resultErr error) {
	fd, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer func() {
		if err := syscall.Close(fd); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close exit-observation kqueue: %w", err))
		}
	}()
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	events := make([]syscall.Kevent_t, 1)
	for {
		var n int
		n, err = syscall.Kevent(fd, change, events, nil)
		if err == syscall.EINTR {
			change = nil
			continue
		}
		if err == syscall.ESRCH {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			change = nil
			continue
		}
		if events[0].Flags&syscall.EV_ERROR != 0 && events[0].Data != 0 {
			return syscall.Errno(events[0].Data)
		}
		return nil
	}
}
