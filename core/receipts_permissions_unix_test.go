//go:build !windows

package core

import (
	"os"
	"testing"
)

func makeReceiptPublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
}
