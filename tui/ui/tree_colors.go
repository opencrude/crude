package ui

import (
	"crude/tui/cell"
	"crude/tui/rendering"
)

// Tree semantic colors resolve from theme.Tree.Colors via TokenSet.

func (ts *TokenSet) TreeDirFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.DirFg })
}
func (ts *TokenSet) TreeFileFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.FileFg })
}
func (ts *TokenSet) TreeSymlinkFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.SymlinkFg })
}
func (ts *TokenSet) TreeSelectedBg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.SelectedBg })
}
func (ts *TokenSet) TreeSelectedFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.SelectedFg })
}
func (ts *TokenSet) TreeCursorBg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.CursorBg })
}
func (ts *TokenSet) TreeCursorFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.CursorFg })
}
func (ts *TokenSet) TreeHoverBg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.Hover })
}
func (ts *TokenSet) TreeIndentFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.IndentLine })
}
func (ts *TokenSet) TreeGitModified() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.GitModified })
}
func (ts *TokenSet) TreeGitAdded() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.GitAdded })
}
func (ts *TokenSet) TreeGitDeleted() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.GitDeleted })
}
func (ts *TokenSet) TreeGitUntracked() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.GitUntracked })
}
func (ts *TokenSet) TreeGitConflicted() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.GitConflicted })
}
func (ts *TokenSet) TreeDiagError() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.DiagError })
}
func (ts *TokenSet) TreeDiagWarning() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.DiagWarning })
}
func (ts *TokenSet) TreeBorderFg() cell.Color {
	return ts.treeColor(func(v treeColorView) cell.Color { return v.BorderFg })
}

type treeColorView struct {
	DirFg, FileFg, SymlinkFg          cell.Color
	SelectedBg, SelectedFg            cell.Color
	CursorBg, CursorFg                cell.Color
	Hover, IndentLine                 cell.Color
	GitModified, GitAdded, GitDeleted cell.Color
	GitUntracked, GitConflicted       cell.Color
	DiagError, DiagWarning            cell.Color
	BorderFg                          cell.Color
}

func (ts *TokenSet) treeColors() treeColorView {
	fallback := func(c cell.Color, role SemanticColor) cell.Color {
		if !c.IsDefault() {
			return c
		}
		return ts.Color(role)
	}
	if ts == nil || ts.theme == nil {
		return treeColorView{}
	}
	tc := ts.theme.Tree.Colors
	return treeColorView{
		DirFg:         fallback(rendering.ThemeColorToCell(tc.DirFg), ColorAccent2),
		FileFg:        fallback(rendering.ThemeColorToCell(tc.FileFg), ColorFg),
		SymlinkFg:     fallback(rendering.ThemeColorToCell(tc.SymlinkFg), ColorMuted),
		SelectedBg:    fallback(rendering.ThemeColorToCell(tc.SelectedBg), ColorSelection),
		SelectedFg:    fallback(rendering.ThemeColorToCell(tc.SelectedFg), ColorFg),
		CursorBg:      fallback(rendering.ThemeColorToCell(tc.CursorBg), ColorSelection),
		CursorFg:      fallback(rendering.ThemeColorToCell(tc.CursorFg), ColorFg),
		Hover:         fallback(rendering.ThemeColorToCell(tc.Hover), ColorHover),
		IndentLine:    fallback(rendering.ThemeColorToCell(tc.IndentLine), ColorBorder),
		GitModified:   fallback(rendering.ThemeColorToCell(tc.GitModified), ColorWarning),
		GitAdded:      fallback(rendering.ThemeColorToCell(tc.GitAdded), ColorSuccess),
		GitDeleted:    fallback(rendering.ThemeColorToCell(tc.GitDeleted), ColorError),
		GitUntracked:  fallback(rendering.ThemeColorToCell(tc.GitUntracked), ColorMuted),
		GitConflicted: fallback(rendering.ThemeColorToCell(tc.GitConflicted), ColorError),
		DiagError:     fallback(rendering.ThemeColorToCell(tc.DiagError), ColorError),
		DiagWarning:   fallback(rendering.ThemeColorToCell(tc.DiagWarning), ColorWarning),
		BorderFg:      fallback(rendering.ThemeColorToCell(tc.BorderFg), ColorBorder),
	}
}

func (ts *TokenSet) treeColor(fn func(treeColorView) cell.Color) cell.Color {
	if ts == nil {
		return cell.NoColor
	}
	return fn(ts.treeColors())
}
