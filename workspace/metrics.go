package workspace

import (
	"strings"
	"time"

	"crude/metrics"
)

var (
	gitCommands = metrics.NewTable(metrics.Meta{Name: "git.commands", Group: metrics.GroupGit, Title: "Git commands",
		Help: "Git executable runs by subcommand (status, diff, fetch, ...). Large repositories and filters show here.",
		Warn: 500_000, Crit: 3_000_000, Stall: 10_000_000})
	projectSearches = metrics.NewLatency(metrics.Meta{Name: "files.search", Group: metrics.GroupFiles, Title: "Find in files",
		Help: "Project-wide text searches from start to the last result.", Warn: 1_000_000, Crit: 5_000_000})
)

// observeGit records one Git run keyed by its subcommand.
func observeGit(args []string, start time.Time) {
	elapsed := time.Since(start)
	key := "git"
	for _, arg := range args {
		if arg != "" && !strings.HasPrefix(arg, "-") {
			key = arg
			break
		}
	}
	gitCommands.Observe(key, elapsed)
	metrics.Blocking("git", key, elapsed)
}
