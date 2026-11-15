package fileio

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenReadSharedDelete permits a concurrent rename or deletion during a read.
// Callers that need a stable source must verify its identity after reading.
func OpenReadSharedDelete(path string) (*os.File, error) {
	return openReadSharedDelete(path, windows.FILE_ATTRIBUTE_NORMAL)
}

// OpenDirectorySharedDelete permits a directory scan to coexist with renames
// and deletion. Windows requires backup semantics to open a directory.
func OpenDirectorySharedDelete(path string) (*os.File, error) {
	return openReadSharedDelete(path, windows.FILE_FLAG_BACKUP_SEMANTICS)
}

func openReadSharedDelete(path string, flags uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
