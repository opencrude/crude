//go:build !windows

package lsp

import (
	"os"
	"path/filepath"
	"strings"
)

func processEnvironmentKey(key string) string { return key }

// PATHEXT has no executable-resolution meaning on Unix.
func prepareProcessExtensions(string) ([]string, error) { return nil, nil }

func validProcessAbsolutePath(path string) bool {
	if !validProcessPathText(path) || !filepath.IsAbs(path) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func processExplicitExecutable(string) bool { return true }

func processExecutableNames(command string, _ []string) []string { return []string{command} }

func processExecutableMode(mode os.FileMode) bool { return mode.Perm()&0111 != 0 }
