package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsRoot_ReturnsNonEmpty(t *testing.T) {
	root, err := SessionsRoot()
	if err != nil {
		t.Fatalf("SessionsRoot: %v", err)
	}
	if root == "" {
		t.Error("SessionsRoot returned empty string")
	}
}

func TestSessionsRoot_IsPlatformSpecific(t *testing.T) {
	root, err := SessionsRoot()
	if err != nil {
		t.Fatalf("SessionsRoot: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".crude", "sessions")
	if root != expected {
		t.Errorf("expected %q, got %q", expected, root)
	}
}

func TestSessionsRoot_ContainsCrudeSessions(t *testing.T) {
	root, _ := SessionsRoot()
	if root == "" {
		t.Skip("empty root")
	}
	base := filepath.Base(filepath.Dir(root))
	if base != ".crude" && base != "crude" {
		t.Errorf("parent dir should be '.crude' or 'crude', got %q", base)
	}
	last := filepath.Base(root)
	if last != "sessions" {
		t.Errorf("last dir should be 'sessions', got %q", last)
	}
}
