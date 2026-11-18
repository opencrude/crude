package projectpath

import (
	"errors"
	"strings"
	"unicode"
)

// ValidateFileName accepts one file name rather than a path. Platform-specific
// reserved names are checked by the destination filesystem.
func ValidateFileName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." {
		return errors.New("file name is empty or invalid")
	}
	if len(name) > 1024 || strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("file name contains invalid characters or is too long")
	}
	return nil
}
