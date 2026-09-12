package theme

import "charm.land/lipgloss/v2"

// DefaultHighContrast returns the high-contrast reference theme. It keeps the
// same semantic roles, metrics, glyphs, and derived-style factory as dark and
// light so controls cannot grow theme-specific layout or behavior. Colors are
// chosen for maximum terminal contrast (near-black background, near-white
// foreground, saturated accents).
func DefaultHighContrast() *Theme {
	c := Colors{
		Bg:                 lipgloss.Color("#000000"),
		Fg:                 lipgloss.Color("#ffffff"),
		Muted:              lipgloss.Color("#c8c8c8"),
		Accent:             lipgloss.Color("#ffff00"),
		Accent2:            lipgloss.Color("#00ffff"),
		Success:            lipgloss.Color("#00ff00"),
		Error:              lipgloss.Color("#ff4040"),
		Warning:            lipgloss.Color("#ffcc00"),
		Border:             lipgloss.Color("#ffffff"),
		Selection:          lipgloss.Color("#0000aa"),
		Hover:              lipgloss.Color("#222222"),
		SubPaneBg:          lipgloss.Color("#0a0a0a"),
		IncompleteBg:       lipgloss.Color("#440000"),
		ContextBg:          lipgloss.Color("#111111"),
		ContextBorder:      lipgloss.Color("#767676"),
		PopupBg:            lipgloss.Color("#000000"),
		PopupBorder:        lipgloss.Color("#ffffff"),
		PopupBorderFocused: lipgloss.Color("#ffff00"),
		PopupTitle:         lipgloss.Color("#c8c8c8"),
		PopupTitleFocused:  lipgloss.Color("#ffff00"),
		PopupDivider:       lipgloss.Color("#808080"),
		PopupSubtleBg:      lipgloss.Color("#1a1a1a"),
		PopupButtonBg:      lipgloss.Color("#00ffff"),
		PopupButtonFg:      lipgloss.Color("#000000"),
		Code: CodeColors{
			Keyword:  lipgloss.Color("#ff8080"),
			String:   lipgloss.Color("#80ff80"),
			Number:   lipgloss.Color("#80ffff"),
			Comment:  lipgloss.Color("#c8c8c8"),
			Function: lipgloss.Color("#ffff80"),
			Type:     lipgloss.Color("#ff80ff"),
			Operator: lipgloss.Color("#ffffff"),
			Bg:       lipgloss.Color("#000000"),
			Border:   lipgloss.Color("#ffffff"),
			Property: lipgloss.Color("#80ffff"),
		},
		Spinner: SpinnerColors{
			Grounding: lipgloss.Color("#ffff00"),
			Planning:  lipgloss.Color("#00ffff"),
			Executing: lipgloss.Color("#00ff00"),
			Default:   lipgloss.Color("#c8c8c8"),
			Done:      lipgloss.Color("#00ff00"),
			Error:     lipgloss.Color("#ff4040"),
		},
		Diff: DiffColors{
			File:     lipgloss.Color("#c8c8c8"),
			Hunk:     lipgloss.Color("#ff80ff"),
			Context:  lipgloss.Color("#c8c8c8"),
			Add:      lipgloss.Color("#00ff00"),
			Delete:   lipgloss.Color("#ff4040"),
			AddBg:    lipgloss.Color("#0a2a0a"),
			DeleteBg: lipgloss.Color("#3a0a0a"),
		},
		Tree: TreeColors{
			DirFg:         lipgloss.Color("#00ffff"),
			FileFg:        lipgloss.Color("#ffffff"),
			SymlinkFg:     lipgloss.Color("#c8c8c8"),
			SelectedBg:    lipgloss.Color("#0000aa"),
			SelectedFg:    lipgloss.Color("#ffffff"),
			CursorBg:      lipgloss.Color("#0033cc"),
			CursorFg:      lipgloss.Color("#ffffff"),
			Hover:         lipgloss.Color("#222222"),
			IndentLine:    lipgloss.Color("#808080"),
			GitModified:   lipgloss.Color("#ffcc00"),
			GitAdded:      lipgloss.Color("#00ff00"),
			GitDeleted:    lipgloss.Color("#ff4040"),
			GitUntracked:  lipgloss.Color("#c8c8c8"),
			GitConflicted: lipgloss.Color("#ff4040"),
			IconDir:       lipgloss.Color("#ffff00"),
			IconFile:      lipgloss.Color("#c8c8c8"),
			IconSymlink:   lipgloss.Color("#00ffff"),
			BorderFg:      lipgloss.Color("#ffffff"),
			DiagError:     lipgloss.Color("#ff4040"),
			DiagWarning:   lipgloss.Color("#ffcc00"),
			DiagInfo:      lipgloss.Color("#00ffff"),
		},
	}
	return named("high-contrast", newTheme(c))
}
