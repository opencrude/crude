package projectpath

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func volumeRoot(path string, info os.FileInfo) (bool, error) {
	if filepath.Dir(path) == path {
		return true, nil
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false, err
	}
	return sameDirectory(info, unix.ByteSliceToString(stat.Mntonname[:])), nil
}
