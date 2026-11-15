//go:build !windows

package fileio

import "os"

// OpenReadSharedDelete opens a read handle; Unix permits concurrent renames.
func OpenReadSharedDelete(path string) (*os.File, error) {
	return os.Open(path)
}

// OpenDirectorySharedDelete opens a directory; Unix permits concurrent renames.
func OpenDirectorySharedDelete(path string) (*os.File, error) {
	return os.Open(path)
}
