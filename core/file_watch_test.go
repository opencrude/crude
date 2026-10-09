package core

import (
	"crude/coreprotocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoreFileWatchMetadataAndLifetime(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", ".claude/worktrees/ignored", "nested"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	events := make(chan coreprotocol.FileChange, 64)
	w, err := startCoreFileWatch(root, func(change coreprotocol.FileChange) { events <- change })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	select {
	case <-w.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("watch startup did not complete")
	}
	git := filepath.Join(root, ".git", "HEAD")
	if err := os.WriteFile(git, []byte("ref: refs/heads/main"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Path == git {
				if !event.Git {
					t.Fatal("git metadata lost")
				}
				w.Close()
				w.Close()
				return
			}
		case <-deadline.C:
			t.Fatal("no Git metadata feed")
		}
	}
}

func TestCoreFileWatchSkipsWorktreesNotGit(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		path string
		skip bool
	}{{".claude/worktrees", true}, {".claude/worktrees/a/b", true}, {".claude/settings.json", false}, {".git/refs/heads", false}, {"worktrees/project", false}} {
		if got := skipCoreWatchPath(root, filepath.Join(root, test.path)); got != test.skip {
			t.Errorf("%s skip=%v", test.path, got)
		}
	}
}
