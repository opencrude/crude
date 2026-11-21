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

// A read-only record must be published read-only on every platform. Windows
// keeps only a read-only attribute, and it must still be set.
func TestAtomicNewPublishesTheRequestedReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	readOnly := filepath.Join(dir, "record.md")
	if err := WriteAtomicNewChecked(readOnly, []byte("evidence"), 0o400, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("a 0400 record was published as %v", info.Mode())
	}
	if data, err := os.ReadFile(readOnly); err != nil || string(data) != "evidence" {
		t.Fatalf("read-only record = %q, %v", data, err)
	}
	// The superuser may write any file; everyone else must be refused.
	if os.Geteuid() != 0 {
		if err := os.WriteFile(readOnly, []byte("changed"), 0o600); err == nil {
			t.Fatal("a read-only record accepted a write")
		}
	}
	writable := filepath.Join(dir, "draft.md")
	if err := WriteAtomicNewChecked(writable, []byte("draft"), 0o600, nil); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(writable)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("a 0600 file was published as %v", info.Mode())
	}
	if err := os.Remove(readOnly); err != nil {
		t.Fatalf("a read-only record cannot be removed: %v", err)
	}
}
