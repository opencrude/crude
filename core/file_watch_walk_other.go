//go:build !windows

package core

import (
	"io/fs"
	"path/filepath"
)

func walkCoreWatch(root string, visit fs.WalkDirFunc) error {
	return filepath.WalkDir(root, visit)
}
