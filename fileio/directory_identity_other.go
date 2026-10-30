//go:build !linux && !darwin && !windows

package fileio

import (
	"fmt"
	"runtime"
)

func openDirectoryIdentityRoot(string) (directoryIdentityHandle, error) {
	return nil, fmt.Errorf("stable directory identity is unsupported on %s; use a supported OS/filesystem with native creation identity", runtime.GOOS)
}
