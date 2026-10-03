//go:build linux

package fileio

import (
	"errors"

	"golang.org/x/sys/unix"
)

func waitCommandExit(pid int) error {
	for {
		err := unix.Waitid(unix.P_PID, pid, nil, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
