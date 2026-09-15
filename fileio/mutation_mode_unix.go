//go:build !windows

package fileio

import "os"

func normalizedMutationMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func normalizedMutationDirMode(mode os.FileMode) os.FileMode { return normalizedMutationMode(mode) }
