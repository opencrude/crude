//go:build !windows

package core

import (
	"path/filepath"
	"testing"
)

func isolatedStartupNavigation(t *testing.T) bool {
	t.Helper()
	return false
}

func testDesktop(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, "Desktop")
}
