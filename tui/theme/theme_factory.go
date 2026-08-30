package theme

import "charm.land/lipgloss/v2"

// DefaultFrameInsets is the shell frame every theme starts with: zero on all
// four sides — the shell renders edge to edge. Frame padding is a user
// preference configured in Settings → Appearance; nothing in the layout may
// assume a blank row or column exists.
func DefaultFrameInsets() Insets {
	return Insets{}
}

// newTheme builds all derived styles and metrics from semantic colors. Keeping
// this shared is essential: light and dark themes differ only in tokens, never
// in component structure or state behavior.
func newTheme(c Colors) *Theme {
	spacing := Spacing{
		PadLR:        2,
		FieldGap:     1,
		ButtonGap:    2,
		DialogPad:    2,
		FormIndent:   1,
		LabelColGap:  3,
		FormTitlePad: 1,
		Frame:        DefaultFrameInsets(),
	}

	treeConfig := TreeConfig{
		IndentWidth:     2,
		ShowIcons:       true,
		ShowGitStatus:   true,
		ShowDiagnostics: true,
		ShowHidden:      false,
		DefaultWidth:    30,
		MinWidth:        20,
		MaxWidth:        60,
		UseNerdFont:     nil,
	}

	windowStyles := WindowStyles{
		Border:          lipgloss.NewStyle().Foreground(c.Border),
		Title:           lipgloss.NewStyle().Foreground(c.Muted),
		TitleFocused:    lipgloss.NewStyle().Foreground(c.Fg).Bold(true),
		TitleBg:         lipgloss.NewStyle().Background(c.SubPaneBg),
		TitleBgFocused:  lipgloss.NewStyle().Background(c.Selection),
		BodyBg:          lipgloss.NewStyle().Background(c.SubPaneBg),
		CloseBtn:        lipgloss.NewStyle().Foreground(c.Muted),
		CloseBtnFocused: lipgloss.NewStyle().Foreground(c.Error).Bold(true),
		BorderFocused:   lipgloss.NewStyle().Foreground(c.Accent),
	}

	styles := Styles{
		Header:          lipgloss.NewStyle().Foreground(c.Accent).Bold(true),
		Separator:       lipgloss.NewStyle().Foreground(c.Border),
		DimLabel:        lipgloss.NewStyle().Foreground(c.Muted),
		AccentLabel:     lipgloss.NewStyle().Foreground(c.Accent2).Italic(true),
		Body:            lipgloss.NewStyle().Foreground(c.Fg),
		Button:          lipgloss.NewStyle().Foreground(c.Fg),
		ButtonFocus:     lipgloss.NewStyle().Foreground(c.Fg).Background(c.Selection),
		ButtonDim:       lipgloss.NewStyle().Foreground(c.Muted),
		DialogBg:        lipgloss.NewStyle().Background(c.PopupBg).Padding(1, 2),
		CodeBlock:       lipgloss.NewStyle().Foreground(c.Fg).Background(c.Code.Bg),
		ToolActive:      lipgloss.NewStyle().Foreground(c.Accent),
		ToolDone:        lipgloss.NewStyle().Foreground(c.Success),
		ToolError:       lipgloss.NewStyle().Foreground(c.Error),
		ThinkingLine:    lipgloss.NewStyle().Foreground(c.Muted).Italic(true),
		UserLabel:       lipgloss.NewStyle().Foreground(c.Fg).Bold(true).Background(c.PopupSubtleBg),
		AsstLabel:       lipgloss.NewStyle().Foreground(c.Accent2).Bold(true).Background(c.PopupSubtleBg),
		IncompleteLabel: lipgloss.NewStyle().Foreground(c.Error).Italic(true).Bold(true).Background(c.IncompleteBg),
		ErrorText:       lipgloss.NewStyle().Foreground(c.Error).Bold(true),
		StatusBar:       lipgloss.NewStyle().Foreground(c.Muted),
		InputPrompt:     lipgloss.NewStyle().Foreground(c.Accent),
		InputText:       lipgloss.NewStyle().Foreground(c.Fg),
		Highlight:       lipgloss.NewStyle().Foreground(c.Fg).Background(c.Selection),
		DangerText:      lipgloss.NewStyle().Foreground(c.Error).Bold(true),
		RetryText:       lipgloss.NewStyle().Foreground(c.Warning),
		AwaitText:       lipgloss.NewStyle().Foreground(c.Warning),
		Window:          windowStyles,
	}

	treeStyles := TreeStyles{
		DirStyle:          lipgloss.NewStyle().Foreground(c.Tree.DirFg).Bold(true),
		FileStyle:         lipgloss.NewStyle().Foreground(c.Tree.FileFg),
		SelectedStyle:     lipgloss.NewStyle().Foreground(c.Tree.SelectedFg).Background(c.Tree.SelectedBg),
		CursorStyle:       lipgloss.NewStyle().Foreground(c.Tree.CursorFg).Background(c.Tree.CursorBg),
		GitModifiedStyle:  lipgloss.NewStyle().Foreground(c.Tree.GitModified),
		GitAddedStyle:     lipgloss.NewStyle().Foreground(c.Tree.GitAdded),
		GitDeletedStyle:   lipgloss.NewStyle().Foreground(c.Tree.GitDeleted),
		GitUntrackedStyle: lipgloss.NewStyle().Foreground(c.Tree.GitUntracked).Italic(true),
	}

	return &Theme{
		Colors:  c,
		Spacing: spacing,
		Metrics: DefaultMetrics(),
		Glyphs:  DefaultGlyphs(),
		Type:    DefaultTypography(),
		Styles:  styles,
		Tree: Tree{
			Colors: c.Tree,
			Styles: treeStyles,
			Config: treeConfig,
		},
	}
}
