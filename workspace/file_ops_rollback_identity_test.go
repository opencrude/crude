package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/fileio"
)

func TestWorkspaceEditRollbackPreservesEqualBytesForeignPostimageIdentity(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	service := NewFileOpsService(root, nil, nil)
	var foreign os.FileInfo
	service.commitWorkspaceEdit = func(from, to string) error {
		if to == b {
			return errors.New("injected later target refusal")
		}
		if err := fileio.CommitAtomicFile(from, to); err != nil {
			return err
		}
		// Keep the published inode alive, so identity cannot be recycled into
		// the foreign replacement which happens to contain identical bytes.
		if err := os.Rename(to, to+".published"); err != nil {
			return err
		}
		if err := os.WriteFile(to, []byte("after"), 0600); err != nil {
			return err
		}
		var err error
		foreign, err = os.Stat(to)
		return err
	}
	result := service.ApplyWorkspaceEdit([]FileEdit{{Path: a, OldContent: "before", NewContent: "after"}, {Path: b, OldContent: "before", NewContent: "after"}})
	if result.Err == nil || foreign == nil {
		t.Fatal("regression did not reach the later failure")
	}
	current, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a)
	if !os.SameFile(current, foreign) || string(data) != "after" {
		t.Fatalf("rollback overwrote distinct equal-bytes foreign postimage: %q", data)
	}
	backups, err := filepath.Glob(filepath.Join(root, ".crude-workspace-edit-*"))
	if err != nil || len(backups) == 0 {
		t.Fatal("unowned rollback lost its recovery evidence", backups, err)
	}
}
