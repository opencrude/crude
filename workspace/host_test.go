package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"crude/buildinfo"
	"crude/types"
)

func TestGhosttyVersionMatchesPin(t *testing.T) {
	tests := []struct {
		ver  string
		want bool
	}{
		{types.PinnedGhosttyVersion, true},
		{types.PinnedGhosttyVersion + "-1", true},
		{"1.3", true},
		{"1.3.1", true},
		{"1.2.0", false},
		{"", false},
		{"kitty", false},
	}
	for _, tc := range tests {
		if got := ghosttyVersionMatchesPin(tc.ver); got != tc.want {
			t.Errorf("ghosttyVersionMatchesPin(%q)=%v want %v", tc.ver, got, tc.want)
		}
	}
}

func TestDetectHostMultiplexer(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("TERM_PROGRAM_VERSION", types.PinnedGhosttyVersion)
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("ZELLIJ", "")
	t.Setenv("STY", "")
	info := DetectHost()
	if info.Kind != HostGhostty {
		t.Fatalf("Kind=%q want ghostty", info.Kind)
	}
	if info.Multiplexer != HostTmux {
		t.Fatalf("Multiplexer=%q want tmux", info.Multiplexer)
	}
	if !info.MatchesGhosttyPin {
		t.Fatal("expected Ghostty pin match inside tmux")
	}
}

func TestPTYEnvironPinsGhostty(t *testing.T) {
	env := PTYEnviron([]string{"PATH=/bin", "TERM=xterm", "HOME=/tmp",
		"TERM_PROGRAM=ghostty", "TERM_PROGRAM_VERSION=1.3.0", "KITTY_WINDOW_ID=1",
		"ALACRITTY_WINDOW_ID=2", "WEZTERM_PANE=3", "GHOSTTY_RESOURCES_DIR=/outer"})
	got := map[string]string{}
	for _, e := range env {
		k, v, ok := splitEnv(e)
		if ok {
			got[k] = v
		}
	}
	if got["TERM"] != types.GhosttyTermName {
		t.Fatalf("TERM=%q want %q", got["TERM"], types.GhosttyTermName)
	}
	if got["TERM_PROGRAM"] != "crude" {
		t.Fatalf("TERM_PROGRAM=%q", got["TERM_PROGRAM"])
	}
	for _, key := range []string{"TERM_PROGRAM_VERSION", "KITTY_WINDOW_ID", "ALACRITTY_WINDOW_ID", "WEZTERM_PANE", "GHOSTTY_RESOURCES_DIR"} {
		if _, inherited := got[key]; inherited {
			t.Fatalf("embedded PTY inherited outer renderer identity %s", key)
		}
	}
	if got["COLORTERM"] != "truecolor" {
		t.Fatalf("COLORTERM=%q", got["COLORTERM"])
	}
	if got["TERMINFO"] == "" {
		t.Fatal("TERMINFO not set; TERM=xterm-ghostty must resolve against a real database")
	}
	if got["PATH"] != "/bin" {
		t.Fatalf("PATH clobbered: %q", got["PATH"])
	}
}

func TestTerminfoDirMaterializesPinnedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CRUDE_TERMINFO_DIR", "")
	// os.UserHomeDir reads USERPROFILE on Windows, not HOME.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	dir := TerminfoDir()
	wantDir := filepath.Join(home, buildinfo.DataDir(), "terminfo")
	if dir != wantDir {
		t.Fatalf("TerminfoDir=%q want %q", dir, wantDir)
	}
	entry := filepath.Join(dir, "x", types.GhosttyTermName)
	got, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read materialized entry: %v", err)
	}
	if !bytes.Equal(got, types.GhosttyTerminfoEntry) {
		t.Fatal("materialized entry does not match the embedded pinned entry")
	}

	// A stale/corrupt entry must be rewritten on the next call.
	if err := os.WriteFile(entry, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir := TerminfoDir(); dir != wantDir {
		t.Fatalf("TerminfoDir=%q want %q", dir, wantDir)
	}
	got, err = os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, types.GhosttyTerminfoEntry) {
		t.Fatal("corrupt entry was not rewritten with the embedded pinned entry")
	}

	// Explicit override wins and materializes nothing.
	override := t.TempDir()
	t.Setenv("CRUDE_TERMINFO_DIR", override)
	if dir := TerminfoDir(); dir != override {
		t.Fatalf("TerminfoDir=%q want override %q", dir, override)
	}
}

func splitEnv(e string) (string, string, bool) {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return e[:i], e[i+1:], true
		}
	}
	return "", "", false
}
