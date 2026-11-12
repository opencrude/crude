package buildinfo

import (
	"os"
	"path/filepath"
)

// DataDirName is the app data directory name (without dot prefix).
// Stable: "crude" → data in <home>/.crude or <xdg>/crude, binary name "crude"
// Beta:   "crude-beta" (via build tag) → data in <home>/.crude-beta or <xdg>/crude-beta
// You can also override via ldflags: -X crude/buildinfo.DataDirName=crude-beta
var DataDirName = "crude"

// DataDir returns the data directory name with dot prefix for home-dir paths.
// Returns ".crude" for stable, ".crude-beta" for beta.
func DataDir() string { return "." + DataDirName }

// HomeEnv selects an isolated crude profile: its directory replaces the user's
// home for every crude data path (core endpoint, config, workspaces, sessions,
// history, logs). Child processes keep the real HOME, so git and go are
// unaffected.
const HomeEnv = "CRUDE_HOME"

// DevelopmentProfile is the profile home, relative to the user's home, that a
// development build selects when HomeEnv is unset. `go run .` then starts its
// own core and data beside the release install and never attaches to, replaces
// or rewrites the release core and its data.
const DevelopmentProfile = ".crude-dev"

// Development reports a source build. Release builds set Version with -ldflags.
func Development() bool { return Version == "dev" }

var selected struct {
	home        string
	development bool
}

// SelectHome fixes this process's profile home and removes HomeEnv from its
// environment, so programs that Crude starts (terminals, tools, a development
// build run inside Crude) choose their own profile instead of inheriting this
// one. An explicit HomeEnv wins; a development build otherwise uses
// DevelopmentProfile. Call it once at program start, before other goroutines.
// Tests do not call it: they keep resolving HomeEnv and HOME on each call.
func SelectHome() (string, error) {
	home := os.Getenv(HomeEnv)
	development := false
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = user
		if Development() {
			home, development = filepath.Join(user, DevelopmentProfile), true
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	selected.home, selected.development = home, development
	return home, os.Unsetenv(HomeEnv)
}

// UsesDevelopmentProfile reports that SelectHome chose DevelopmentProfile.
func UsesDevelopmentProfile() bool { return selected.development }

// Home is the directory whose DataDir holds this instance's data.
func Home() (string, error) {
	if selected.home != "" {
		return selected.home, nil
	}
	if home := os.Getenv(HomeEnv); home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}
