package fileio

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"crude/logger"
)

func writeAtomic(path string, data []byte, perm os.FileMode, lg *logger.Logger) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("atomic write: resolve path: %w", err)
	}

	if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() {
		perm = fi.Mode().Perm()
	}

	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("atomic write: ensure parent dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".crude_tmp_*")
	if err != nil {
		return fmt.Errorf("atomic write: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if lg != nil {
		lg.Debug("ATOMIC", "temp_created", "path", abs, "tmp", tmpName, "data_len", len(data))
	}

	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
			if lg != nil {
				lg.Debug("ATOMIC", "temp_rolled_back", "tmp", tmpName, "path", abs)
			}
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("atomic write: write data: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(perm); err != nil {
			return fmt.Errorf("atomic write: chmod: %w", err)
		}
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("atomic write: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write: close: %w", err)
	}

	if err := os.Rename(tmpName, abs); err != nil {
		if lg != nil {
			lg.Error("ATOMIC", "rename_failed", "tmp", tmpName, "path", abs, "error", err.Error())
		}
		return fmt.Errorf("atomic write: rename: %w", err)
	}

	ok = true
	if lg != nil {
		lg.Debug("ATOMIC", "committed", "path", abs, "data_len", len(data))
	}
	return nil
}

func readFile(absPath string) ([]byte, os.FileInfo, error) {
	fi, err := os.Stat(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file: stat: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("read file: %s is not a regular file", absPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file: read: %w", err)
	}
	return data, fi, nil
}

func readFileSecure(absPath string, policy *pathPolicy) ([]byte, os.FileInfo, error) {
	if err := policy.validatePath(absPath); err != nil {
		return nil, nil, err
	}
	if err := validatePathForRead(absPath); err != nil {
		return nil, nil, err
	}
	return readFile(absPath)
}

func readFileForEdit(absPath string, policy *pathPolicy) ([]byte, os.FileInfo, error) {
	if err := policy.validatePath(absPath); err != nil {
		return nil, nil, err
	}
	if err := validatePathForRead(absPath); err != nil {
		return nil, nil, err
	}

	ft, _ := classifyPath(absPath)
	if ft == ftNonexistent {
		return nil, nil, fmt.Errorf("file not found: %s", absPath)
	}

	data, fi, err := readFile(absPath)
	if err != nil {
		return nil, nil, err
	}

	if IsBinary(data) {
		return nil, nil, &BinaryFileError{Path: absPath, Size: fi.Size()}
	}

	return data, fi, nil
}
