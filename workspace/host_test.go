package workspace

import (
	"testing"

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
	if got["PATH"] != "/bin" {
		t.Fatalf("PATH clobbered: %q", got["PATH"])
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
