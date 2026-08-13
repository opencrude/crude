package workspace

import (
	"bytes"
	"os"
	"path/filepath"
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
	env := PTYEnviron([]string{"PATH=/bin", "TERM=xterm", "HOME=/tmp"})
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
	if got["TERM_PROGRAM"] != "ghostty" {
		t.Fatalf("TERM_PROGRAM=%q", got["TERM_PROGRAM"])
	}
	if got["TERM_PROGRAM_VERSION"] != types.PinnedGhosttyVersion {
		t.Fatalf("TERM_PROGRAM_VERSION=%q", got["TERM_PROGRAM_VERSION"])
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
