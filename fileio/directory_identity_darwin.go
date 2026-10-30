package fileio

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type nativeDirectoryIdentityHandle int

func openDirectoryIdentityRoot(root string) (directoryIdentityHandle, error) {
	return nativeDirectoryIdentityHandle(unix.AT_FDCWD).openChild(root)
}

func (h nativeDirectoryIdentityHandle) openChild(name string) (directoryIdentityHandle, error) {
	fd, err := unix.Openat(int(h), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open no-follow directory: %w", err)
	}
	return nativeDirectoryIdentityHandle(fd), nil
}

func (h nativeDirectoryIdentityHandle) close() error {
	return unix.Close(int(h))
}

func (h nativeDirectoryIdentityHandle) identity(withBirth bool) (string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(h), &stat); err != nil {
		return "", fmt.Errorf("read native directory identity: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino == 0 {
		return "", fmt.Errorf("native handle is not a real directory with an inode identity")
	}
	object := fmt.Sprintf("darwin:v1:%x:%x", uint32(stat.Dev), stat.Ino)
	if !withBirth {
		return object, nil
	}
	if (stat.Btim.Sec == 0 && stat.Btim.Nsec == 0) || stat.Btim.Nsec < 0 || stat.Btim.Nsec >= 1e9 {
		return "", fmt.Errorf("directory identity unavailable: filesystem must provide trustworthy birth time; use a supported local filesystem")
	}
	return fmt.Sprintf("%s:%x:%x", object, stat.Btim.Sec, stat.Btim.Nsec), nil
}
