//go:build windows

package fileio

import "os"

func openRootRegular(root *os.Root, name string) (*os.File, error) {
	// Root confines link resolution; the caller compares the opened identity
	// against the preceding Lstat before accepting it.
	return root.Open(name)
}
