package fileio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const (
	maxDirectoryIdentityPathBytes = 32768
	maxDirectoryIdentityDepth     = 128
)

// DirectoryIdentity observes an existing real directory's native object and
// creation identity. The opaque UTF-8 result is at most 512 bytes, survives
// reopen/process restart and child mutations, and is not derived from its path.
// Filesystems without trustworthy creation identity fail closed.
//
// This performs synchronous, bounded metadata I/O; callers must run it off the
// UI/Update thread. It never scans children or writes markers. Native calls can
// still block on an unresponsive filesystem. Retained no-follow handles and a
// second observation detect observed replacement, not all adversarial races:
// this is neither a sandbox nor a guarantee that a later open is the same object.
func DirectoryIdentity(path string) (string, error) {
	return directoryIdentity(path, openDirectoryIdentityRoot)
}

type directoryIdentityHandle interface {
	openChild(string) (directoryIdentityHandle, error)
	// identity(false) needs only native object identity: retained handles prevent
	// reuse while comparing ancestors. The final directory requires birth time.
	identity(withBirth bool) (string, error)
	close() error
}

type directoryIdentityOpener func(string) (directoryIdentityHandle, error)

func directoryIdentity(path string, openRoot directoryIdentityOpener) (result string, err error) {
	root, components, err := directoryIdentityPath(path)
	if err != nil {
		return "", err
	}
	if err := CheckDirectoryNoSymlinks(path); err != nil {
		return "", fmt.Errorf("directory identity safety check: %w", err)
	}

	// Hold both observations until comparison is complete. In particular, an
	// unlinked inode cannot be recycled underneath the first observation.
	var retained []directoryIdentityHandle
	defer func() {
		for i := len(retained) - 1; i >= 0; i-- {
			if closeErr := retained[i].close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close directory identity handle: %w", closeErr))
				result = ""
			}
		}
	}()
	observe := func() ([]string, string, error) {
		handle, err := openRoot(root)
		if err != nil {
			return nil, "", err
		}
		retained = append(retained, handle)
		objects := make([]string, 0, len(components)+1)
		for i := 0; ; i++ {
			object, err := handle.identity(false)
			if err != nil {
				return nil, "", err
			}
			objects = append(objects, object)
			if i == len(components) {
				stable, err := handle.identity(true)
				return objects, stable, err
			}
			handle, err = handle.openChild(components[i])
			if err != nil {
				return nil, "", err
			}
			retained = append(retained, handle)
		}
	}
	before, stable, err := observe()
	if err != nil {
		return "", fmt.Errorf("observe native directory identity: %w", err)
	}
	after, confirmed, err := observe()
	if err != nil {
		return "", fmt.Errorf("recheck native directory identity: %w", err)
	}
	for i := range before {
		if before[i] != after[i] {
			return "", fmt.Errorf("directory or ancestor replaced during identity observation; retry with a stable directory")
		}
	}
	if stable != confirmed {
		return "", fmt.Errorf("directory creation identity changed during observation; retry with a stable directory")
	}
	if err := CheckDirectoryNoSymlinks(path); err != nil {
		return "", fmt.Errorf("directory identity final safety check: %w", err)
	}
	if stable == "" || len(stable) > 512 || !utf8.ValidString(stable) {
		return "", fmt.Errorf("native filesystem returned an unavailable or invalid directory identity")
	}
	return stable, nil
}

func directoryIdentityPath(path string) (string, []string, error) {
	if path == "" || len(path) > maxDirectoryIdentityPathBytes || strings.IndexByte(path, 0) >= 0 {
		return "", nil, fmt.Errorf("directory identity requires a nonempty absolute path of at most %d bytes", maxDirectoryIdentityPathBytes)
	}
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", `\`)
	}
	if !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("directory identity requires an absolute path")
	}
	volume := filepath.VolumeName(path)
	if runtime.GOOS == "windows" && (strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`)) {
		return "", nil, fmt.Errorf("directory identity requires an ordinary drive or UNC path, not a device namespace")
	}
	remainder := path[len(volume):]
	components := strings.FieldsFunc(remainder, func(r rune) bool {
		return r == rune(filepath.Separator) || (runtime.GOOS == "windows" && r == '/')
	})
	if len(components) > maxDirectoryIdentityDepth {
		return "", nil, fmt.Errorf("directory identity path exceeds %d components", maxDirectoryIdentityDepth)
	}
	for _, component := range components {
		if component == "." || component == ".." {
			return "", nil, fmt.Errorf("directory identity rejects dot and traversal path components")
		}
		if runtime.GOOS == "windows" && (strings.Contains(component, ":") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ")) {
			return "", nil, fmt.Errorf("directory identity rejects ambiguous Windows path components")
		}
	}
	return volume + string(os.PathSeparator), components, nil
}
