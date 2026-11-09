package fileio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicNewNeverReplacesAFileCreatedAfterPrecondition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	err := WriteAtomicNewChecked(path, []byte("draft"), 0o600, func() error {
		return os.WriteFile(path, []byte("other writer"), 0o600)
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("concurrent create was not refused: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "other writer" {
		t.Fatalf("concurrent file was overwritten: %q, %v", data, err)
	}
}
