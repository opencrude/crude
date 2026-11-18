package projectpath

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func volumeRoot(path string, info os.FileInfo) (bool, error) {
	parent := filepath.Dir(path)
	if parent == path {
		return true, nil
	}
	// Mount IDs distinguish bind mounts too, unlike device numbers alone.
	var current, above unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_MNT_ID, &current)
	if err == nil {
		err = unix.Statx(unix.AT_FDCWD, parent, 0, unix.STATX_MNT_ID, &above)
	}
	if err == nil && current.Mask&unix.STATX_MNT_ID != 0 && above.Mask&unix.STATX_MNT_ID != 0 {
		return current.Mnt_id != above.Mnt_id, nil
	}
	if err != nil && !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return false, err
	}
	var a, b unix.Stat_t
	if err := unix.Stat(path, &a); err != nil {
		return false, err
	}
	if err := unix.Stat(parent, &b); err != nil {
		return false, err
	}
	return a.Dev != b.Dev || sameDirectory(info, parent), nil
}
