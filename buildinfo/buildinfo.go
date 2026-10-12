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
// unaffected. Developing crude inside crude runs the dev build with its own
// profile, so it never replaces or blocks the core the developer is using.
const HomeEnv = "CRUDE_HOME"

// Home is the directory whose DataDir holds this instance's data.
func Home() (string, error) {
	if home := os.Getenv(HomeEnv); home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}
