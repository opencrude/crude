//go:build !windows

package fileio

import "os"

// MutationFileMode is the mode observable after a native file write or chmod.
func MutationFileMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

// MutationDirectoryMode is the mode observable after a native directory chmod.
func MutationDirectoryMode(mode os.FileMode) os.FileMode { return MutationFileMode(mode) }
