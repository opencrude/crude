//go:build !windows

package core

import (
	"errors"
	"os"
)

func receiptFilePermissions(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("receipt files must be private regular files")
	}
	return nil
}
