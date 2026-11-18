//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package fileio

import (
	"errors"

	"golang.org/x/sys/unix"
)

func waitCommandExit(pid int) error {
	descriptor, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(descriptor)
	changes := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	events := make([]unix.Kevent_t, 1)
	for {
		_, err := unix.Kevent(descriptor, changes, events, nil)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if !errors.Is(err, unix.EINTR) {
			if err == nil && events[0].Flags&unix.EV_ERROR != 0 {
				eventError := unix.Errno(events[0].Data)
				if eventError != 0 && eventError != unix.ESRCH {
					return eventError
				}
			}
			return err
		}
		changes = nil
	}
}
