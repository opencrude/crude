package fileio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"crude/buildinfo"
	"crude/logger"
)

type committedWriteError struct{ err error }

func (e committedWriteError) Error() string { return e.err.Error() }
func (e committedWriteError) Unwrap() error { return e.err }

// WriteCommitted reports whether replacement succeeded before a durability
// error. Callers must acknowledge the new disk bytes rather than retrying an
// obsolete precondition.
func WriteCommitted(err error) bool {
	var committed committedWriteError
	return errors.As(err, &committed)
}

// CommittedWarning reconstructs a committed durability warning received from a
// remote filesystem owner. It must never be used for an unconfirmed effect.
func CommittedWarning(message string) error {
	return committedWriteError{err: errors.New(message)}
}

// CommitAtomicFile publishes a closed, synced temporary file in the target's
// directory. Callers own staging, permissions, mutation leases and preconditions;
// this primitive owns platform-specific replacement and directory durability.
func CommitAtomicFile(from, to string) error {
	if err := replaceFile(from, to); err != nil {
		return fmt.Errorf("atomic write: replace %s: %w", to, err)
	}
	if err := syncParentDirectory(filepath.Dir(to)); err != nil {
		return committedWriteError{err: fmt.Errorf("atomic write: sync parent of %s: %w", to, err)}
	}
	return nil
}

const maxTrackedMutationBytes int64 = 256 << 20

// WriteAtomicChecked replaces path with data after precommit succeeds. It is
// the shared low-level commit primitive for workspace mutation services; policy
// and precondition ownership stay with the caller.
func WriteAtomicChecked(path string, data []byte, perm os.FileMode, precommit func() error) error {
	return replaceAtomic(path, data, perm, nil, precommit, false)
}

func writeAtomicChecked(path string, data []byte, perm os.FileMode, lg *logger.Logger, precommit func() error) error {
	return replaceAtomic(path, data, perm, lg, precommit, true)
}

func replaceAtomic(path string, data []byte, perm os.FileMode, lg *logger.Logger, precommit func() error, createParents bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("atomic write: resolve path: %w", err)
	}

	if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() {
		perm = fi.Mode().Perm()
	}

	dir := filepath.Dir(abs)
	if createParents {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("atomic write: ensure parent dir: %w", err)
		}
	}

	tmp, err := os.CreateTemp(dir, buildinfo.DataDirName+"_tmp_*")
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
	if precommit != nil {
		if err := precommit(); err != nil {
			return fmt.Errorf("atomic write: precondition: %w", err)
		}
	}

	if err := CommitAtomicFile(tmpName, abs); err != nil {
		if lg != nil {
			lg.Error("ATOMIC", "commit_failed", "tmp", tmpName, "path", abs, "error", err.Error())
		}
		return err
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
	if fi.Size() > maxTrackedMutationBytes {
		return nil, nil, fmt.Errorf("read file: %s exceeds the %d MiB tracked-mutation limit", absPath, maxTrackedMutationBytes>>20)
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
		return nil, nil, fmt.Errorf("file not found: %s: %w", absPath, fs.ErrNotExist)
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
