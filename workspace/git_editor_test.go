package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if len(ann.Hunks) != 1 || ann.Hunks[0].OldStart != 2 || ann.Hunks[0].NewStart != 2 || len(ann.Hunks[0].OldLines) != 1 || len(ann.Hunks[0].NewLines) != 2 {
		t.Fatalf("hunks=%#v", ann.Hunks)
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
	if len(ann.Hunks) != 1 || len(ann.Hunks[0].OldLines) != 0 || len(ann.Hunks[0].NewLines) != 1 {
		t.Fatalf("hunks=%#v", ann.Hunks)
	}
}

func TestAnnotateFileUnbornHeadMarksAdded(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	path := filepath.Join(root, "first.txt")
	content := "first\nsecond\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ann, err := NewGitService(root).AnnotateFile(context.Background(), path, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(ann.Hunks) != 1 || len(ann.Hunks[0].OldLines) != 0 || len(ann.Hunks[0].NewLines) != 2 {
		t.Fatalf("hunks=%#v", ann.Hunks)
	}
}

func TestAnnotateFileReportsUnmergedConflict(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		return cmd.CombinedOutput()
	}
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	for _, args := range [][]string{{"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		if out, err := git(args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	path := filepath.Join(root, "conflict.txt")
	commit := func(text, message string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := git("add", "conflict.txt"); err != nil {
			t.Fatalf("git add: %v: %s", err, out)
		}
		if out, err := git("commit", "-m", message); err != nil {
			t.Fatalf("git commit: %v: %s", err, out)
		}
	}
	commit("base\n", "base")
	branchBytes, err := git("branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	baseBranch := strings.TrimSpace(string(branchBytes))
	if out, err := git("checkout", "-b", "other"); err != nil {
		t.Fatalf("branch: %v: %s", err, out)
	}
	commit("other\n", "other")
	if out, err := git("checkout", baseBranch); err != nil {
		t.Fatalf("checkout: %v: %s", err, out)
	}
	commit("main\n", "main")
	if _, err := git("merge", "other"); err == nil {
		t.Fatal("expected merge conflict")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ann, err := NewGitService(root).AnnotateFile(context.Background(), path, string(content))
	if err != nil {
		t.Fatal(err)
	}
	if !ann.Conflicted {
		t.Fatal("unmerged index was not reported as conflicted")
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

func TestLineHunksRetainReplacementAndAddition(t *testing.T) {
	before := "a\nb\nc\n"
	after := "a\nB\nc\nd\n"
	hunks := DocumentHunks(before, after)
	if len(hunks) != 2 || hunks[0].NewStart != 1 || len(hunks[0].OldLines) != 1 || len(hunks[0].NewLines) != 1 || hunks[1].NewStart != 3 || len(hunks[1].NewLines) != 1 {
		t.Fatalf("hunks=%#v", hunks)
	}
}

func TestLineAnnotationHonorsDistantAndSeparateHunkCoordinates(t *testing.T) {
	beforeLines := make([]string, 220)
	for i := range beforeLines {
		beforeLines[i] = fmt.Sprintf("line %03d", i+1)
	}
	afterLines := append([]string(nil), beforeLines...)
	afterLines[164] = "changed 165"
	afterLines[199] = "changed 200"
	afterLines = append(afterLines[:205], append([]string{"inserted 206"}, afterLines[205:]...)...)

	hunks := DocumentHunks(strings.Join(beforeLines, "\n")+"\n", strings.Join(afterLines, "\n")+"\n")
	if len(hunks) != 3 || hunks[0].NewStart != 164 || hunks[1].NewStart != 199 || hunks[2].NewStart != 205 {
		t.Fatalf("hunks=%#v", hunks)
	}
}

func TestLineAnnotationRetainsDeletionBoundary(t *testing.T) {
	hunks := DocumentHunks("a\nb\nc\nd\n", "a\nd\n")
	if len(hunks) != 1 || hunks[0].NewStart != 1 || len(hunks[0].OldLines) != 2 || len(hunks[0].NewLines) != 0 {
		t.Fatalf("hunks=%#v", hunks)
	}
}
