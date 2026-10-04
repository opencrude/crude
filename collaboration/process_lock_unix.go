//go:build !windows

package collaboration

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type processLock struct {
	file *os.File
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("collaboration: open runtime lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errProcessLockHeld
		}
		return nil, fmt.Errorf("collaboration: acquire runtime lock: %w", err)
	}
	return &processLock{file: file}, nil
}

func (lock *processLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return fmt.Errorf("collaboration: release runtime lock: %w", err)
	}
	return closeErr
}
