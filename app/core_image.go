package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if staged, err := os.Stat(target); err != nil || !staged.Mode().IsRegular() || staged.Size() != info.Size() {
		if err := copyCoreImage(source, dir, target); err != nil {
			return "", err
		}
	}
	pruneCoreImages(dir, filepath.Base(target))
	return target, nil
}

// copyCoreImage publishes the copy atomically, so a concurrent start never
// executes a partially written image.
func copyCoreImage(source *os.File, dir, target string) error {
	temp, err := os.CreateTemp(dir, ".staging-*")
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(temp, source)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(copyErr, syncErr, closeErr, os.Chmod(temp.Name(), 0o700)); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		_ = os.Remove(temp.Name())
		// A concurrent start of the same build may already run the identical
		// image, which Windows will not let a rename replace.
		if staged, statErr := os.Stat(target); statErr == nil && staged.Mode().IsRegular() {
			return nil
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
