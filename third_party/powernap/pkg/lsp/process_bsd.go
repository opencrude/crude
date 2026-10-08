//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package lsp

import "syscall"

func waitProcessExit(pid int) error {
	fd, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(fd) }()
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	events := make([]syscall.Kevent_t, 1)
	for {
		_, err = syscall.Kevent(fd, change, events, nil)
		if err == syscall.EINTR {
			change = nil
			continue
		}
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
}
