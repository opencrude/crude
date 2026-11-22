package viewmodel

import (
	"time"

	"crude/internal/metrics"
)

// Shell-side suggestion measurements for the performance dashboard. They
// stay in this process: nothing is stored or sent.
var (
	suggestVisible = metrics.NewLatency(metrics.Meta{Name: "suggest.visible", Group: metrics.GroupSuggest, Title: "Key to suggestion",
		Help: "From the key or request to Errand's change on screen. Under 300 ms feels immediate; reused changes show in the next frame.",
		Warn: 300_000, Crit: 1_000_000, Stall: 10_000_000})
	suggestShownCount = metrics.NewCounter(metrics.Meta{Name: "suggest.shown", Group: metrics.GroupSuggest, Title: "Suggestions shown", Unit: "items",
		Help: "Changes that Errand showed in an editor."})
	suggestAcceptedCount = metrics.NewCounter(metrics.Meta{Name: "suggest.accepted", Group: metrics.GroupSuggest, Title: "Suggestions accepted", Unit: "items",
		Help: "Changes taken with Tab, or partly with Ctrl+Right or Alt+End."})
	suggestTypedCount = metrics.NewCounter(metrics.Meta{Name: "suggest.typed", Group: metrics.GroupSuggest, Title: "Suggestions typed through", Unit: "items",
		Help: "Changes that the user typed in full instead of pressing Tab."})
	suggestDismissedCount = metrics.NewCounter(metrics.Meta{Name: "suggest.dismissed", Group: metrics.GroupSuggest, Title: "Suggestions dismissed", Unit: "items",
		Help: "Changes dismissed with Esc."})
	// suggestOutcomes ranks outcomes by context source: a source whose
	// changes are seldom accepted does not help and is a candidate to remove.
	// The duration is the time from showing the change to the outcome.
	suggestOutcomes = metrics.NewTable(metrics.Meta{Name: "suggest.outcomes", Group: metrics.GroupSuggest, Title: "Suggestion outcomes by context",
		Help: "Each row is an outcome and a context source the request carried. Compare accepted with dismissed for each source."})
)

type suggestionOutcome string

const (
	suggestionAccepted     suggestionOutcome = "accepted"
	suggestionAcceptedPart suggestionOutcome = "accepted in part"
	suggestionTypedThrough suggestionOutcome = "typed through"
	suggestionDismissed    suggestionOutcome = "dismissed"
)

// recordSuggestionShown counts a shown change and its latency from the key.
func recordSuggestionShown(began time.Time, shown *suggestionShown) {
	suggestShownCount.Inc()
	if !began.IsZero() {
		suggestVisible.Observe(shown.at.Sub(began))
	}
	for _, source := range shownSources(shown) {
		suggestOutcomes.Observe("shown · "+source, 0)
	}
}

// recordSuggestionOutcome counts one outcome of a shown change.
func recordSuggestionOutcome(outcome suggestionOutcome, shown *suggestionShown) {
	switch outcome {
	case suggestionAccepted, suggestionAcceptedPart:
		suggestAcceptedCount.Inc()
	case suggestionTypedThrough:
		suggestTypedCount.Inc()
	case suggestionDismissed:
		suggestDismissedCount.Inc()
	}
	elapsed := time.Since(shown.at)
	for _, source := range shownSources(shown) {
		suggestOutcomes.Observe(string(outcome)+" · "+source, elapsed)
	}
}

// shownSources names the code at the caret too, so every outcome has a row.
func shownSources(shown *suggestionShown) []string {
	return append([]string{"code at the caret"}, shown.sources...)
}
