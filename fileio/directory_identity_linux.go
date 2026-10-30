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
	fd, err := unix.Openat(int(h), name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open no-follow directory: %w", err)
	}
	return nativeDirectoryIdentityHandle(fd), nil
}

func (h nativeDirectoryIdentityHandle) close() error {
	return unix.Close(int(h))
}

func (h nativeDirectoryIdentityHandle) identity(withBirth bool) (string, error) {
	var stat unix.Statx_t
	// Empty-path statx observes this exact retained handle, not its old pathname.
	if err := unix.Statx(int(h), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_TYPE|unix.STATX_INO|unix.STATX_BTIME, &stat); err != nil {
		return "", fmt.Errorf("statx directory identity unavailable (kernel/filesystem must support statx birth time): %w", err)
	}
	return linuxDirectoryIdentity(stat, withBirth)
}

func linuxDirectoryIdentity(stat unix.Statx_t, withBirth bool) (string, error) {
	if stat.Mask&(unix.STATX_TYPE|unix.STATX_INO) != unix.STATX_TYPE|unix.STATX_INO || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino == 0 {
		return "", fmt.Errorf("statx did not provide a real directory's device/inode identity")
	}
	object := fmt.Sprintf("linux:v1:%x:%x:%x", stat.Dev_major, stat.Dev_minor, stat.Ino)
	if !withBirth {
		return object, nil
	}
	if stat.Mask&unix.STATX_BTIME == 0 || (stat.Btime.Sec == 0 && stat.Btime.Nsec == 0) || stat.Btime.Nsec >= 1e9 {
		return "", fmt.Errorf("directory identity unavailable: filesystem must provide trustworthy statx birth time; use a supported local filesystem")
	}
	return fmt.Sprintf("%s:%x:%x", object, stat.Btime.Sec, stat.Btime.Nsec), nil
}
