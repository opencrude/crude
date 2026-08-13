package workspace

import (
	"os"
	"strings"

	"crude/types"
)

// HostKind identifies the outer terminal emulator Crude is running in.
type HostKind string

const (
	HostGhostty  HostKind = "ghostty"
	HostTmux     HostKind = "tmux"
	HostZellij   HostKind = "zellij"
	HostScreen   HostKind = "screen"
	HostUnknown  HostKind = "unknown"
)

// HostInfo describes the outer terminal and any multiplexer wrapping Crude.
type HostInfo struct {
	Kind              HostKind
	Version           string
	Term              string
	TermProgram       string
	Multiplexer       HostKind // tmux/zellij/screen when nested; empty otherwise
	MatchesGhosttyPin bool
}

// DetectHost inspects process environment for Ghostty and common multiplexers.
// Ghostty sets TERM_PROGRAM=ghostty and TERM_PROGRAM_VERSION even when nested
// inside tmux/zellij (those set TMUX / ZELLIJ / STY on top).
func DetectHost() HostInfo {
	info := HostInfo{
		Term:        os.Getenv("TERM"),
		TermProgram: os.Getenv("TERM_PROGRAM"),
		Kind:        HostUnknown,
	}
	ver := os.Getenv("TERM_PROGRAM_VERSION")
	if ver == "" {
		ver = os.Getenv("GHOSTTY_VERSION")
	}
	info.Version = ver

	prog := strings.ToLower(info.TermProgram)
	term := strings.ToLower(info.Term)
	switch {
	case prog == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		info.Kind = HostGhostty
	}

	switch {
	case os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux"):
		info.Multiplexer = HostTmux
	case os.Getenv("ZELLIJ") != "" || strings.Contains(term, "zellij"):
		info.Multiplexer = HostZellij
	case os.Getenv("STY") != "" || strings.HasPrefix(term, "screen"):
		info.Multiplexer = HostScreen
	}

	if info.Kind == HostGhostty {
		info.MatchesGhosttyPin = ghosttyVersionMatchesPin(info.Version)
	}
	return info
}

func ghosttyVersionMatchesPin(version string) bool {
	if version == "" {
		return false
	}
	pin := types.PinnedGhosttyVersion
	if version == pin {
		return true
	}
	// Accept "1.3.0-..." and "1.3.0+..." build suffixes, and "1.3" as 1.3.x.
	if strings.HasPrefix(version, pin) {
		rest := version[len(pin):]
		return rest == "" || rest[0] == '-' || rest[0] == '+' || rest[0] == '.'
	}
	parts := strings.Split(pin, ".")
	if len(parts) >= 2 {
		majorMinor := parts[0] + "." + parts[1]
		if version == majorMinor || strings.HasPrefix(version, majorMinor+".") {
			return true
		}
	}
	return false
}

// PTYEnviron returns environment variables for an in-app terminal so child
// programs see the pinned Ghostty terminfo regardless of Crude's host.
func PTYEnviron(extra []string) []string {
	env := append([]string{}, extra...)
	if len(env) == 0 {
		env = append(env, os.Environ()...)
	}
	env = setEnv(env, "TERM", types.GhosttyTermName)
	env = setEnv(env, "COLORTERM", "truecolor")
	env = setEnv(env, "TERM_PROGRAM", "ghostty")
	env = setEnv(env, "TERM_PROGRAM_VERSION", types.PinnedGhosttyVersion)
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
