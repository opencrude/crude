package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/fileio"
)

func TestWorkspaceEditRefusesChangedStagingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	ops := NewFileOpsService(root, nil, nil)
	changed := false
	result := ops.applyWorkspaceEdit([]FileEdit{{Path: path, OldContent: "before", NewContent: "after"}}, nil, func() error {
		paths, err := filepath.Glob(filepath.Join(root, ".crude-workspace-edit-*"))
		if err != nil {
			return err
		}
		for _, stage := range paths {
			data, err := os.ReadFile(stage)
			if err != nil {
				return err
			}
			if string(data) == "after" {
				changed = true
				return os.WriteFile(stage, []byte("forged"), 0600)
			}
		}
		t.Fatal("did not find the staged postimage")
		return nil
	}, nil, nil)
	if !changed || result.Err == nil || !result.NoEffect {
		t.Fatalf("changed stage was admitted: %+v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before" {
		t.Fatalf("target changed: %q, %v", data, err)
	}
}

func TestWorkspaceEditDoesNotOverwriteChangedCommittedTarget(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ops := NewFileOpsService(root, nil, nil)
	count := 0
	ops.commitWorkspaceEdit = func(from, to string) error {
		if err := fileio.CommitAtomicFile(from, to); err != nil {
			return err
		}
		count++
		if count == 2 {
			return os.WriteFile(a, []byte("outside change"), 0600)
		}
		return nil
	}
	result := ops.applyWorkspaceEdit([]FileEdit{
		{Path: a, OldContent: "before", NewContent: "after"},
		{Path: b, OldContent: "before", NewContent: "after"},
	}, nil, nil, nil, nil)
	if result.Err == nil || result.NoEffect || errors.Is(result.Err, os.ErrNotExist) {
		t.Fatalf("changed postimage was reported as committed: %+v", result)
	}
	if data, err := os.ReadFile(a); err != nil || string(data) != "outside change" {
		t.Fatalf("later change was overwritten: %q %v", data, err)
	}
}
