package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestAnnotateFileBlameAndLineEdits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.User.Name = "Crude Test"
	cfg.User.Email = "crude@example.test"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{
		Name: "Crude Test", Email: "crude@example.test", When: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	edited := "package main\n\nfunc a() { return }\nfunc b() {}\n"
	service := NewGitService(root)
	ann, err := service.AnnotateFile(context.Background(), path, edited)
	if err != nil {
		t.Fatal(err)
	}
	if len(ann.Edits) != 4 {
		t.Fatalf("edits=%d want 4: %#v", len(ann.Edits), ann.Edits)
	}
	if ann.Edits[2] != GitLineModified {
		t.Fatalf("line 3 kind=%d want modified", ann.Edits[2])
	}
	if ann.Edits[3] != GitLineAdded {
		t.Fatalf("line 4 kind=%d want added", ann.Edits[3])
	}
	if len(ann.Blame) < 3 {
		t.Fatalf("blame lines=%d want at least HEAD lines", len(ann.Blame))
	}
	if ann.Blame[0].Author != "Crude Test" || ann.Blame[0].Subject != "initial" {
		t.Fatalf("blame[0]=%#v", ann.Blame[0])
	}
	var added GitBlameLine
	for _, line := range ann.Blame {
		if line.Line == 4 {
			added = line
			break
		}
	}
	if added.Line != 4 || !added.Uncommitted {
		t.Fatalf("added-line blame=%#v want uncommitted line 4", added)
	}
}

func TestAnnotateFileUntrackedIsAllAdded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "new.go")
	if err := os.WriteFile(path, []byte("package new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ann, err := NewGitService(root).AnnotateFile(context.Background(), path, "package new\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(ann.Edits) != 1 || ann.Edits[0] != GitLineAdded {
		t.Fatalf("edits=%v want [added]", ann.Edits)
	}
}

func TestAnnotateFileHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewGitService(t.TempDir()).AnnotateFile(ctx, "x.go", "x")
	if err == nil {
		t.Fatal("canceled context must fail")
	}
}

func TestLineEditsMarksModifiedAndAdded(t *testing.T) {
	before := "a\nb\nc\n"
	after := "a\nB\nc\nd\n"
	got := lineEdits(before, after)
	want := []GitLineKind{GitLineUnchanged, GitLineModified, GitLineUnchanged, GitLineAdded}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d kind=%d want %d", i+1, got[i], want[i])
		}
	}
}
