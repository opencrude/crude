package gitreview

import (
	"crude/tui/screens/diffview"
	"crude/tui/theme"
	"crude/workspace"
)

type PreparedComparison struct {
	Comparison workspace.GitComparison
	View       diffview.Prepared
}

// PrepareComparison derives presentation off the UI thread; theme is resolved
// when mounted, so a prepared result cannot reinstall an obsolete palette.
func PrepareComparison(_ *theme.Theme, c workspace.GitComparison) PreparedComparison {
	p := PreparedComparison{Comparison: c}
	if !c.Binary {
		p.View = diffview.Prepare(diffview.Comparison{Path: c.Path, BeforeLabel: c.BaseLabel, AfterLabel: c.TargetLabel, Before: c.Before, After: c.After})
	}
	return p
}
func (screen *Screen) InstallComparisons(comparisons []PreparedComparison) {
	if screen.prepared == nil {
		screen.prepared = make(map[string]PreparedComparison)
	}
	for _, p := range comparisons {
		screen.prepared[p.Comparison.Path+"\x00"+p.Comparison.Fingerprint] = p
	}
}
