package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const coreImagePrefix = "crude-core-"

// CoreImageDir holds the content-addressed executables detached cores run from.
func CoreImageDir(home string) string { return filepath.Join(DataDir(home), "bin") }

// stageCoreImage returns a private copy of exe named by its build ID. A core
// outlives the shell that started it; running it from the shell's own image
// would pin that file, so `go run` could not delete its temporary build and a
// rebuild could not replace crude.exe while the core lingers (Windows refuses
// to remove or overwrite a running image). Images of other builds are pruned;
// one still running is kept by the OS and removed by a later start.
func stageCoreImage(home, exe, buildID string) (string, error) {
	if buildID == "" || buildID == "unknown" || strings.ContainsAny(buildID, `/\.`) {
		return "", fmt.Errorf("cannot identify the executable %s", exe)
	}
	dir := CoreImageDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, coreImagePrefix+buildID+filepath.Ext(exe))
	source, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return "", err
	}
	if err := validateCoreImage(target, info.Size()); errors.Is(err, os.ErrNotExist) {
		if err := copyCoreImage(source, dir, target); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	pruneCoreImages(dir, filepath.Base(target))
	return target, nil
}

// validateCoreImage checks an existing publication without following symlinks or
// repairing it in place. A build-named image is immutable once published.
func validateCoreImage(target string, size int64) error {
	staged, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !staged.Mode().IsRegular() || staged.Size() != size || (runtime.GOOS != "windows" && staged.Mode().Perm()&0o100 == 0) {
		return fmt.Errorf("invalid staged core executable %s: expected a regular executable of %d bytes", target, size)
	}
	return nil
}

// copyCoreImage closes its complete candidate before atomically publishing it
// without replacement. Renaming over an existing image is not safe: a concurrent
// fork may still hold a candidate's writable descriptor until exec, even after
// this process closes it. Replacing the ready winner with that inode can then
// make another start fail with ETXTBSY. A hard link elects one immutable winner
// across processes (including Windows) and never changes the inode being run.
func copyCoreImage(source *os.File, dir, target string) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".staging-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	n, copyErr := io.Copy(temp, source)
	if copyErr == nil && n != info.Size() {
		copyErr = fmt.Errorf("core executable changed size while staging: copied %d bytes, expected %d", n, info.Size())
	}
	modeErr := temp.Chmod(0o700)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(copyErr, modeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Link(temp.Name(), target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return validateCoreImage(target, info.Size())
		}
		return err
	}
	return nil
}

func pruneCoreImages(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || entry.IsDir() || !strings.HasPrefix(name, coreImagePrefix) {
			continue
		}
		// Removal fails only while that image still runs; a later start retries.
		_ = os.Remove(filepath.Join(dir, name))
	}
}
