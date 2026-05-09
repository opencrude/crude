package fileio

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const binaryProbeSize = 512

func IsBinary(data []byte) bool {
	probe := data
	if len(probe) > binaryProbeSize {
		probe = probe[:binaryProbeSize]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

type fileType int

const (
	ftRegular fileType = iota
	ftDirectory
	ftSymlink
	ftDevice
	ftOther
	ftNonexistent
)

func classifyPath(path string) (fileType, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ftNonexistent, nil
		}
		return ftOther, fmt.Errorf("stat %s: %w", path, err)
	}

	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return ftSymlink, nil
	case fi.IsDir():
		return ftDirectory, nil
	case fi.Mode()&os.ModeDevice != 0:
		return ftDevice, nil
	case fi.Mode().IsRegular():
		return ftRegular, nil
	default:
		return ftOther, nil
	}
}

func resolveSymlink(absPath string) (string, error) {
	for {
		fi, err := os.Lstat(absPath)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return absPath, nil
		}
		target, err := os.Readlink(absPath)
		if err != nil {
			return "", fmt.Errorf("readlink %s: %w", absPath, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(absPath), target)
		}
		absPath = filepath.Clean(target)
	}
}

type pathPolicy struct {
	followSymlinks bool
}

func defaultPathPolicy() *pathPolicy {
	return &pathPolicy{
		followSymlinks: false,
	}
}

func (p *pathPolicy) validatePath(absPath string) error {
	if p.followSymlinks {
		resolved, err := resolveSymlink(absPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("symlink resolution failed for %s: %w", absPath, err)
			}
		} else {
			absPath = resolved
		}
	} else {
		ft, err := classifyPath(absPath)
		if err != nil {
			return fmt.Errorf("cannot classify path %s: %w", absPath, err)
		}
		if ft == ftSymlink {
			return fmt.Errorf("refusing to follow symlink %s — resolve the actual file path instead", absPath)
		}
	}

	ft, err := classifyPath(absPath)
	if err != nil {
		return fmt.Errorf("cannot classify path %s: %w", absPath, err)
	}

	switch ft {
	case ftDirectory:
		return fmt.Errorf("%s is a directory, not a file", absPath)
	case ftDevice:
		return fmt.Errorf("refusing to operate on device file %s", absPath)
	case ftOther:
		return fmt.Errorf("refusing to operate on non-regular file %s", absPath)
	}

	return nil
}

func validatePathForRead(absPath string) error {
	ft, err := classifyPath(absPath)
	if err != nil {
		return fmt.Errorf("cannot classify path %s: %w", absPath, err)
	}
	switch ft {
	case ftNonexistent:
		return fmt.Errorf("file not found: %s", absPath)
	case ftDirectory:
		return fmt.Errorf("%s is a directory, not a file", absPath)
	case ftDevice:
		return fmt.Errorf("refusing to read device file %s", absPath)
	case ftSymlink:
		return fmt.Errorf("refusing to follow symlink %s", absPath)
	case ftOther:
		return fmt.Errorf("refusing to read non-regular file %s", absPath)
	}
	return nil
}

func validateParentDir(absPath string) error {
	dir := filepath.Dir(absPath)
	ft, err := classifyPath(dir)
	if err != nil {
		return fmt.Errorf("cannot stat parent directory %s: %w", dir, err)
	}
	if ft == ftNonexistent {
		return os.MkdirAll(dir, 0755)
	}
	if ft != ftDirectory {
		return fmt.Errorf("parent path %s is not a directory", dir)
	}
	return nil
}

type BinaryFileError struct {
	Path string
	Size int64
}

func (e *BinaryFileError) Error() string {
	return fmt.Sprintf("binary file detected: %s (%d bytes) — cannot edit binary files, use execute_command instead", e.Path, e.Size)
}

func (ft fileType) String() string {
	switch ft {
	case ftRegular:
		return "regular"
	case ftDirectory:
		return "directory"
	case ftSymlink:
		return "symlink"
	case ftDevice:
		return "device"
	case ftOther:
		return "other"
	case ftNonexistent:
		return "nonexistent"
	default:
		return "unknown"
	}
}
