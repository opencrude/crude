package app

import (
	"crypto/sha256"
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
// rebuild could not replace crude.exe on Windows. Other images are retained:
// Unix permits unlinking a running executable, and staging cannot prove that
// an image is unused by a core, a helper, or a concurrent starter.
func stageCoreImage(home, exe, buildID string) (string, error) {
	if buildID == "" || buildID == "unknown" || strings.ContainsAny(buildID, `/\.`) {
		return "", fmt.Errorf("cannot identify the executable %s", exe)
	}
	dir := CoreImageDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := secureOwnerPath(dir, true); err != nil {
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
	digest, err := coreImageDigest(source)
	if err != nil {
		return "", err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err := validateCoreImage(target, info.Size()); errors.Is(err, os.ErrNotExist) {
		if err := copyCoreImage(source, dir, target); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := verifyCoreImage(target, digest); err != nil {
		return "", err
	}
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
	if err := secureOwnerPath(temp.Name(), false); err != nil {
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

func coreImageDigest(source *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, source); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func verifyCoreImage(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validateCoreImage(path, info.Size()); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return fmt.Errorf("core image changed while opening %s", path)
	}
	actual, err := coreImageDigest(f)
	if err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("core executable digest mismatch: %s", path)
	}
	return nil
}
