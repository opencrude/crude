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
	// Remote operations wait on the network: seconds are normal, so they are
	// judged apart from local commands.
	gitRemoteCommands = metrics.NewTable(metrics.Meta{Name: "git.remote", Group: metrics.GroupGit, Title: "Git remote operations",
		Help: "Fetch, pull and push, including background fetches that keep ahead/behind current.",
		Warn: 15_000_000, Crit: 60_000_000})
	projectSearches = metrics.NewLatency(metrics.Meta{Name: "files.search", Group: metrics.GroupFiles, Title: "Find in files",
		Help: "Project-wide text searches from start to the last result.", Warn: 1_000_000, Crit: 5_000_000})
)

// observeGit records one Git run keyed by its subcommand.
func observeGit(args []string, start time.Time) {
	observeGitIn(gitCommands, args, start)
}

// observeGitRemote records one network operation keyed by its subcommand.
func observeGitRemote(args []string, start time.Time) {
	observeGitIn(gitRemoteCommands, args, start)
}

func observeGitIn(table *metrics.Table, args []string, start time.Time) {
	elapsed := time.Since(start)
	key := "git"
	for _, arg := range args {
		if arg != "" && !strings.HasPrefix(arg, "-") {
			key = arg
			break
		}
	}
	table.Observe(key, elapsed)
	metrics.Blocking("git", key, elapsed)
}
