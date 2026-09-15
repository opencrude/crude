//go:build windows

package fileio

import "os"

func normalizedMutationMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o222 == 0 {
		return 0o444
	}
	return 0o666
}

func normalizedMutationDirMode(mode os.FileMode) os.FileMode {
	if mode.Perm()&0o222 == 0 {
		return 0o555
	}
	return 0o777
}
