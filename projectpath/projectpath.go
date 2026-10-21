// Package projectpath distinguishes project directories from navigation-only
// locations on the core host. It never creates directories or project state.
package projectpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	KindRoot     = "root"
	KindHome     = "home"
	KindPersonal = "personal"
)

// Location is an existing directory. An empty Kind admits a project, including
// an empty or non-Git directory. Every nonempty Kind is navigation-only.
type Location struct {
	Path string
	Kind string
	// Warning reports incomplete optional personal-folder metadata. It does not
	// invalidate Path or relax canonical directory and volume-root validation.
	Warning string
}

// Inspect resolves links and preserves filesystem case. Classification uses
// directory identity, not names: children of personal folders remain projects.
func Inspect(path string) (Location, error) {
	path, info, err := canonicalDirectory(path)
	if err != nil {
		return Location{}, err
	}
	root, err := volumeRoot(path, info)
	if err != nil {
		return Location{}, fmt.Errorf("inspect volume root: %w", err)
	}
	if root {
		return Location{Path: path, Kind: KindRoot}, nil
	}
	// This is personal-folder detection, not a Crude data path.
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		homeErr = fmt.Errorf("locate personal home: %w", homeErr)
	}
	if home != "" && sameDirectory(info, home) {
		return Location{Path: path, Kind: KindHome}, nil
	}
	personal, err := personalDirectories(home)
	location := Location{Path: path}
	if warning := errors.Join(homeErr, err); warning != nil {
		location.Warning = warning.Error()
	}
	for _, candidate := range personal {
		if sameDirectory(info, candidate) {
			location.Kind = KindPersonal
			break
		}
	}
	return location, nil
}

func canonicalDirectory(path string) (string, os.FileInfo, error) {
	if path == "" {
		return "", nil, fmt.Errorf("directory path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%q is not a directory", path)
	}
	return resolved, info, nil
}

func sameDirectory(info os.FileInfo, path string) bool {
	// Personal folders may be disabled, absent or inaccessible. They are optional
	// candidates, never a reason to deny an unrelated project.
	other, err := os.Stat(path)
	return err == nil && other.IsDir() && os.SameFile(info, other)
}
