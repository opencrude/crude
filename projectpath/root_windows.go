package projectpath

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func volumeRoot(path string, info os.FileInfo) (bool, error) {
	// Handles drive roots, UNC shares, and extended-length forms.
	if filepath.Dir(path) == path {
		return true, nil
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	buffer := make([]uint16, 32768)
	if err := windows.GetVolumePathName(name, &buffer[0], uint32(len(buffer))); err != nil {
		return false, err
	}
	return sameDirectory(info, windows.UTF16ToString(buffer)), nil
}
