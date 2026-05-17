package session

import (
	"os"
	"path/filepath"
	"runtime"
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
	root, _ := SessionsRoot()

	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		expected := filepath.Join(appdata, "crude", "sessions")
		if appdata == "" {
			expected = filepath.Join(home, "AppData", "Roaming", "crude", "sessions")
		}
		if root != expected {
			t.Errorf("expected %q, got %q", expected, root)
		}
	case "darwin":
		expected := filepath.Join(home, "Library", "Application Support", "crude", "sessions")
		if root != expected {
			t.Errorf("expected %q, got %q", expected, root)
		}
	case "linux":
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg != "" {
			expected := filepath.Join(xdg, "crude", "sessions")
			if root != expected {
				t.Errorf("expected %q, got %q", expected, root)
			}
		}
	}
}

func TestSessionsRoot_ContainsCrudeSessions(t *testing.T) {
	root, _ := SessionsRoot()
	if root == "" {
		t.Skip("empty root")
	}
	base := filepath.Base(filepath.Dir(root))
	if base != "crude" {
		t.Errorf("parent dir should be 'crude', got %q", base)
	}
	last := filepath.Base(root)
	if last != "sessions" {
		t.Errorf("last dir should be 'sessions', got %q", last)
	}
}
