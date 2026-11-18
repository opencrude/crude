//go:build windows

package fileio

import "os"

// MutationFileMode is the mode observable after a native file write or chmod.
func MutationFileMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o200 == 0 {
		return 0o444
	}
	return 0o666
}

// MutationDirectoryMode is the mode observable after a native directory chmod.
func MutationDirectoryMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o200 == 0 {
		return 0o555
	}
	return 0o777
}
