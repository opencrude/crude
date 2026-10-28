package projectpath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ErrParentMovedAfterCreate indicates that creation succeeded on the opened
// directory, but its displayed path no longer names that directory.
var ErrParentMovedAfterCreate = errors.New("parent folder moved during creation")

// ValidateFolderName accepts exactly one child name, never a path or an
// operating-system control character. The filesystem validates platform-specific
// reserved names and reports collisions without changing the existing entry.
func ValidateFolderName(name string) error {
	if len(name) > 1024 {
		return fmt.Errorf("folder name is too long")
	}
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return fmt.Errorf("enter a folder name")
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("folder name cannot end with a dot or space")
	}
	if strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 || !filepath.IsLocal(name) {
		return fmt.Errorf("folder name must be a single name without path separators or control characters")
	}
	return nil
}

// CreateFolder creates exactly one previously nonexistent child of parent.
// An open directory handle confines the operation even if its path is changed.
func CreateFolder(ctx context.Context, parent, name string) (string, error) {
	if err := ValidateFolderName(name); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(parent) {
		return "", fmt.Errorf("parent folder must be an absolute path")
	}
	parent, before, err := canonicalDirectory(parent)
	if err != nil {
		return "", fmt.Errorf("open parent folder: %w", err)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", fmt.Errorf("open parent folder: %w", err)
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return "", fmt.Errorf("parent folder changed; browse it again")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := root.Mkdir(name, 0755); err != nil {
		return "", fmt.Errorf("create folder %q: %w", name, err)
	}
	current, err := os.Stat(parent)
	if err != nil || !os.SameFile(opened, current) {
		return "", fmt.Errorf("%w; the folder was created in the original parent, whose current location must be checked", ErrParentMovedAfterCreate)
	}
	// Return the core-host path; the shell never constructs a remote destination.
	return filepath.Join(parent, name), nil
}
