package gitreview

import (
	"crude/tui/editor"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

type PreparedComparison struct {
	Comparison   workspace.GitComparison
	hunks        []workspace.GitHunk
	rows         []int
	unified      *editor.Editor
	before       *editor.Editor
	after        *editor.Editor
	unifiedKinds map[int]ui.SemanticColor
	beforeKinds  map[int]ui.SemanticColor
	afterKinds   map[int]ui.SemanticColor
}

func PrepareComparison(th *theme.Theme, comparison workspace.GitComparison) PreparedComparison {
	prepared := PreparedComparison{Comparison: comparison}
	if comparison.Binary {
		return prepared
	}
	prepared.hunks = workspace.DocumentHunks(comparison.Before, comparison.After)
	patch, kinds, rows := unifiedDocument(comparison)
	prepared.rows, prepared.unifiedKinds = rows, kinds
	prepared.unified = editor.NewPreview(th, comparison.Path+".diff", patch, 1)
	prepared.unified.SetLineNumbers(false)
	prepared.before = editor.NewPreview(th, comparison.Path, comparison.Before, 1)
	prepared.after = editor.NewPreview(th, comparison.Path, comparison.After, 1)
	prepared.beforeKinds, prepared.afterKinds = sideKinds(prepared.hunks, true), sideKinds(prepared.hunks, false)
	return prepared
}

func (screen *Screen) InstallComparisons(comparisons []PreparedComparison) {
	if screen.prepared == nil {
		screen.prepared = make(map[string]PreparedComparison)
	}
	for _, comparison := range comparisons {
		screen.prepared[comparison.Comparison.Path+"\x00"+comparison.Comparison.Fingerprint] = comparison
	}
}
