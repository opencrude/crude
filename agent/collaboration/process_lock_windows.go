//go:build windows

package collaboration

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type processLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("collaboration: open runtime lock: %w", err)
	}
	lock := &processLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errProcessLockHeld
		}
		return nil, fmt.Errorf("collaboration: acquire runtime lock: %w", err)
	}
	return lock, nil
}

func (lock *processLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.overlapped)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return fmt.Errorf("collaboration: release runtime lock: %w", err)
	}
	return closeErr
}
