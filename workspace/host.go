package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"crude/internal/buildinfo"
	"crude/internal/terminalvt"
	"crude/workspace/fileio"
)

// HostKind identifies the outer terminal emulator Crude is running in.
type HostKind string

const (
	HostGhostty HostKind = "ghostty"
	HostTmux    HostKind = "tmux"
	HostZellij  HostKind = "zellij"
	HostScreen  HostKind = "screen"
	HostUnknown HostKind = "unknown"
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
	pin := terminalvt.PinnedGhosttyVersion
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
//
// Setting TERM=xterm-ghostty is only half of the contract: the terminfo
// database entry must also resolve. PTYEnviron therefore points TERMINFO at
// TerminfoDir(), which materializes the embedded pinned entry under the app
// data dir when no system database provides it.
func PTYEnviron(base []string) []string {
	env := slices.Clone(base)
	if base == nil {
		env = os.Environ()
	}
	env = setEnv(env, "TERM", terminalvt.GhosttyTermName)
	env = setEnv(env, "COLORTERM", "truecolor")
	env = setEnv(env, "TERM_PROGRAM", "crude")
	// Outer-window identity does not describe this embedded terminal's renderer
	// or fonts. Do not make children select unconfirmed graphics/glyph paths.
	env = slices.DeleteFunc(env, func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "TERM_PROGRAM_VERSION", "KITTY_WINDOW_ID", "KITTY_PID", "ALACRITTY_WINDOW_ID",
			"ALACRITTY_SOCKET", "WEZTERM_PANE", "WEZTERM_UNIX_SOCKET", "GHOSTTY_RESOURCES_DIR":
			return true
		}
		return false
	})
	if dir := TerminfoDir(); dir != "" {
		env = setEnv(env, "TERMINFO", dir)
	}
	return env
}

// TerminfoDir returns the directory to expose as TERMINFO to child PTYs so
// the pinned xterm-ghostty entry resolves. Precedence:
//
//  1. CRUDE_TERMINFO_DIR: explicit override (installers, tests); used
//     verbatim, nothing is materialized.
//  2. <home>/<app data dir>/terminfo: the embedded pinned entry is
//     written there unless an identical file already exists.
//
// Returns "" when no usable directory can be established (no home dir, or an
// unwritable app data dir); callers then fall back to the system terminfo
// search path.
func TerminfoDir() string {
	if dir := os.Getenv("CRUDE_TERMINFO_DIR"); dir != "" {
		return dir
	}
	home, err := buildinfo.Home()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, buildinfo.DataDir(), "terminfo")
	if !ensureTerminfoEntry(filepath.Join(dir, "x", terminalvt.GhosttyTermName)) {
		return ""
	}
	// Apple ncurses uses the hex value of the leading byte. Programs linked
	// against other ncurses builds can still need the letter directory.
	if runtime.GOOS == "darwin" && !ensureTerminfoEntry(filepath.Join(dir, "78", terminalvt.GhosttyTermName)) {
		return ""
	}
	return dir
}

func ensureTerminfoEntry(entry string) bool {
	if existing, err := os.ReadFile(entry); err == nil && bytes.Equal(existing, terminalvt.GhosttyTerminfoEntry) {
		return true
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return false
	}
	err := fileio.WriteAtomicChecked(entry, terminalvt.GhosttyTerminfoEntry, 0o644, nil)
	return err == nil || fileio.WriteCommitted(err)
}

func environmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func setEnv(env []string, key, value string) []string {
	name := environmentKey(key)
	env = slices.DeleteFunc(env, func(entry string) bool {
		entryKey, _, _ := strings.Cut(entry, "=")
		return environmentKey(entryKey) == name
	})
	return append(env, key+"="+value)
}
