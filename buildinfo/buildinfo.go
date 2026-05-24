package buildinfo

// DataDirName is the app data directory name (without dot prefix).
// Stable: "crude" → data in <home>/.crude or <xdg>/crude, binary name "crude"
// Beta:   "crude-beta" (via build tag) → data in <home>/.crude-beta or <xdg>/crude-beta
// You can also override via ldflags: -X crude/buildinfo.DataDirName=crude-beta
var DataDirName = "crude"

// DataDir returns the data directory name with dot prefix for home-dir paths.
// Returns ".crude" for stable, ".crude-beta" for beta.
func DataDir() string { return "." + DataDirName }
