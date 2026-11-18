//go:build windows

package fileio

import "golang.org/x/sys/windows"

func replaceFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MOVEFILE_WRITE_THROUGH waits for the replacement to reach disk. Windows
// does not expose a portable directory fsync equivalent.
func syncParentDirectory(string) error { return nil }
