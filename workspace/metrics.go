package workspace

import (
	"strings"
	"time"

	"crude/insight/textindex"
	"crude/internal/metrics"
	"crude/types/insight"
)

var (
	gitCommands = metrics.NewTable(metrics.Meta{Name: "git.commands", SaveLabels: true, Group: metrics.GroupGit, Title: "Git commands",
		Help: "Git executable runs by subcommand (status, diff, fetch, ...). Large repositories and filters show here.",
		Warn: 500_000, Crit: 3_000_000, Stall: 10_000_000})
	// Remote operations wait on the network: seconds are normal, so they are
	// judged apart from local commands.
	gitRemoteCommands = metrics.NewTable(metrics.Meta{Name: "git.remote", SaveLabels: true, Group: metrics.GroupGit, Title: "Git remote operations",
		Help: "Fetch, pull and push, including background fetches that keep ahead/behind current.",
		Warn: 15_000_000, Crit: 60_000_000})
	projectSearches = metrics.NewLatency(metrics.Meta{Name: "files.search", Group: metrics.GroupFiles, Title: "Find in files",
		Help: "Project-wide text searches from start to the last result.", Warn: 1_000_000, Crit: 5_000_000})

	// Errand suggestion stages in the core, one observation per answered
	// request. Reused answers (cache, follow-ups) record only the total.
	suggestContext = metrics.NewLatency(metrics.Meta{Name: "suggest.context", Group: metrics.GroupSuggest, Title: "Suggestion context",
		Help: "Collecting code, edits, problems and related code from memory. It never waits for a server.", Warn: 5_000, Crit: 20_000})
	suggestLedger = metrics.NewLatency(metrics.Meta{Name: "suggest.ledger", Group: metrics.GroupSuggest, Title: "Suggestion spending check",
		Help: "Reserving tokens in the hourly limit before the request.", Warn: 5_000, Crit: 50_000})
	suggestFirstByte = metrics.NewLatency(metrics.Meta{Name: "suggest.first_byte", Group: metrics.GroupSuggest, Title: "Suggestion first text",
		Help: "From sending the request to the model's first text.", Warn: 300_000, Crit: 1_000_000, Stall: 5_000_000})
	suggestModel = metrics.NewLatency(metrics.Meta{Name: "suggest.model", Group: metrics.GroupSuggest, Title: "Suggestion model answer",
		Help: "From sending the request to the end of the model's answer.", Warn: 500_000, Crit: 2_000_000, Stall: 10_000_000})
	suggestCheck = metrics.NewLatency(metrics.Meta{Name: "suggest.check", Group: metrics.GroupSuggest, Title: "Suggestion check",
		Help: "Checking a suggestion before it is shown.", Warn: 2_000, Crit: 10_000})
	suggestTotal = metrics.NewLatency(metrics.Meta{Name: "suggest.total", Group: metrics.GroupSuggest, Title: "Suggestion answer in the core",
		Help: "From the request to the answer, including reused answers. Under 300 ms feels immediate.", Warn: 300_000, Crit: 1_000_000, Stall: 10_000_000})
	_ = metrics.NewGauge( // sampled each second; never set directly
		metrics.Meta{Name: "suggest.index", Group: metrics.GroupSuggest, Title: "Text index memory", Unit: "bytes",
			Help: "Estimated memory of the workspace text indexes: text, chunks and postings."}, textindex.TotalMemory)
)

// observeSuggestionStages records one answered suggestion request.
func observeSuggestionStages(stages insight.EditorPredictionStages, total time.Duration, reused bool) {
	us := func(v int64) time.Duration { return time.Duration(v) * time.Microsecond }
	suggestTotal.Observe(total)
	if reused {
		return
	}
	suggestContext.Observe(us(stages.ContextUS))
	if stages.AccountUS > 0 || stages.ModelUS > 0 {
		suggestLedger.Observe(us(stages.AccountUS))
		suggestFirstByte.Observe(us(stages.FirstByteUS))
		suggestModel.Observe(us(stages.ModelUS))
	}
	suggestCheck.Observe(us(stages.CheckUS))
}

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
