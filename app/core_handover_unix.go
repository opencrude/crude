//go:build !windows

package app

import (
	"errors"
	"os"
	"syscall"
)

func validateOwnerPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errors.New("core owner path is not private to the current user: " + path)
	}
	return nil
}

func secureOwnerPath(path string, directory bool) error {
	return validateOwnerPath(path, directory)
}
