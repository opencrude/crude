package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type Theme struct {
	Colors  Colors
	Spacing Spacing
	Styles  Styles
	Tree    Tree
}

type Tree struct {
	Colors TreeColors
	Styles TreeStyles
	Config TreeConfig
}

type Colors struct {
	Bg, Fg, Muted, Accent, Accent2  color.Color
	Success, Error, Warning, Border color.Color
	Selection, SubPaneBg            color.Color
	// Popup chrome colors. All popups (context menu, find/replace,
	// save dialog, text prompt, file picker, diagnostic) use a
	// unified design system — see widgets.PopupChromeConfig.
	// - PopupBg: the body fill of every popup (typically == Code.Bg)
	// - PopupBorder: the border color when unfocused
	// - PopupBorderFocused: the border color when the popup has focus
	// - PopupTitle: title text color (unfocused)
	// - PopupTitleFocused: title text color (focused)
	// - PopupDivider: subtle separator line color
	// - PopupSubtleBg: hover/selection background for items in a list
	// - PopupButtonBg: filled button background (primary action)
	// - PopupButtonFg: filled button foreground
	PopupBg, PopupBorder, PopupBorderFocused, PopupTitle, PopupTitleFocused color.Color
	PopupDivider, PopupSubtleBg                                             color.Color
	PopupButtonBg, PopupButtonFg                                             color.Color
	Code                            CodeColors
	Spinner                         SpinnerColors
	Diff                            DiffColors
	Tree                            TreeColors
}

type CodeColors struct {
	Keyword, String, Number, Comment     color.Color
	Function, Type, Operator, Bg, Border color.Color
	Property                             color.Color
}

type SpinnerColors struct {
	Grounding, Planning, Executing, Default, Done, Error color.Color
}

type DiffColors struct {
	File, Hunk, Context, Add, Delete color.Color
}

type TreeColors struct {
	DirFg, FileFg, SymlinkFg                  color.Color
	SelectedBg, SelectedFg                     color.Color
	CursorBg, CursorFg                         color.Color
	IndentLine                                 color.Color
	GitModified, GitAdded, GitDeleted          color.Color
	GitUntracked, GitConflicted                color.Color
	IconDir, IconFile, IconSymlink, BorderFg  color.Color
	DiagError, DiagWarning, DiagInfo           color.Color
}

type TreeStyles struct {
	DirStyle, FileStyle                        lipgloss.Style
	SelectedStyle, CursorStyle                  lipgloss.Style
	GitModifiedStyle, GitAddedStyle             lipgloss.Style
	GitDeletedStyle, GitUntrackedStyle          lipgloss.Style
}

type TreeConfig struct {
	IndentWidth    int
	ShowIcons      bool
	ShowGitStatus  bool
	ShowDiagnostics bool
	ShowHidden     bool
	DefaultWidth   int
	MinWidth       int
	MaxWidth       int
	UseNerdFont    *bool // nil = auto-detect
}

type Spacing struct {
	PadLR        int
	FieldGap     int
	ButtonGap    int
	DialogPad    int
	FormIndent   int
	LabelColGap  int
	FormTitlePad int
}

type WindowStyles struct {
	Border         lipgloss.Style
	Title          lipgloss.Style
	TitleFocused   lipgloss.Style
	TitleBg        lipgloss.Style
	TitleBgFocused lipgloss.Style
	BodyBg         lipgloss.Style
	CloseBtn       lipgloss.Style
	CloseBtnFocused lipgloss.Style
	BorderFocused  lipgloss.Style
}

type Styles struct {
	Header, Separator, DimLabel, AccentLabel     lipgloss.Style
	Body, Button, ButtonFocus, ButtonDim, DialogBg lipgloss.Style
	CodeBlock, ToolActive, ToolDone, ToolError    lipgloss.Style
	ThinkingLine, UserLabel, AsstLabel            lipgloss.Style
	ErrorText, StatusBar, InputPrompt, InputText  lipgloss.Style
	Highlight, DangerText, RetryText, AwaitText     lipgloss.Style
	Window                                         WindowStyles
}

func DefaultDark() *Theme {
	c := Colors{
		Bg:       lipgloss.Color("#0d1117"),
		Fg:       lipgloss.Color("#e6edf3"),
		Muted:    lipgloss.Color("#8b949e"),
		Accent:   lipgloss.Color("#f0883e"),
		Accent2:  lipgloss.Color("#a5d6ff"),
		Success:  lipgloss.Color("#7ee787"),
		Error:    lipgloss.Color("#ff7b72"),
		Warning:  lipgloss.Color("#d29922"),
		Border:   lipgloss.Color("#30363d"),
		Selection: lipgloss.Color("#1e2a3a"),
		SubPaneBg: lipgloss.Color("#1c2333"),
		// Popup chrome — unified design across all popups.
		// Code.Bg provides the "card on editor" feel used by the
		// diagnostic hover popup. PopupBorder is intentionally
		// dim so the title color carries the visual weight.
		PopupBg:            lipgloss.Color("#161b22"),
		PopupBorder:        lipgloss.Color("#3a4250"),
		PopupBorderFocused: lipgloss.Color("#a5d6ff"),
		PopupTitle:         lipgloss.Color("#8b949e"),
		PopupTitleFocused:  lipgloss.Color("#a5d6ff"),
		PopupDivider:       lipgloss.Color("#2a313c"),
		PopupSubtleBg:      lipgloss.Color("#1e2a3a"),
		PopupButtonBg:      lipgloss.Color("#a5d6ff"),
		PopupButtonFg:      lipgloss.Color("#0d1117"),
		Code: CodeColors{
			Keyword:  lipgloss.Color("#ff7b72"),
			String:   lipgloss.Color("#a5d6ff"),
			Number:   lipgloss.Color("#79c0ff"),
			Comment:  lipgloss.Color("#8b949e"),
			Function: lipgloss.Color("#d2a8ff"),
			Type:     lipgloss.Color("#79c0ff"),
			Operator: lipgloss.Color("#ff7b72"),
			Bg:       lipgloss.Color("#161b22"),
			Border:   lipgloss.Color("#30363d"),
			Property: lipgloss.Color("#79c0ff"),
		},
		Spinner: SpinnerColors{
			Grounding: lipgloss.Color("#f0883e"),
			Planning:  lipgloss.Color("#a5d6ff"),
			Executing: lipgloss.Color("#7ee787"),
			Default:   lipgloss.Color("#8b949e"),
			Done:      lipgloss.Color("#7ee787"),
			Error:     lipgloss.Color("#ff7b72"),
		},
		Diff: DiffColors{
			File:   lipgloss.Color("#a0a8b7"),
			Hunk:   lipgloss.Color("#636d83"),
			Context: lipgloss.Color("#8b949e"),
			Add:    lipgloss.Color("#7ee787"),
			Delete: lipgloss.Color("#ff7b72"),
		},
		Tree: TreeColors{
			DirFg:        lipgloss.Color("#79c0ff"),
			FileFg:       lipgloss.Color("#e6edf3"),
			SymlinkFg:    lipgloss.Color("#a0a8b7"),
			SelectedBg:   lipgloss.Color("#264040"),
			SelectedFg:   lipgloss.Color("#e6edf3"),
			CursorBg:     lipgloss.Color("#2d4f4f"),
			CursorFg:     lipgloss.Color("#e6edf3"),
			IndentLine:   lipgloss.Color("#30363d"),
			GitModified:  lipgloss.Color("#d29922"),
			GitAdded:     lipgloss.Color("#7ee787"),
			GitDeleted:   lipgloss.Color("#ff7b72"),
			GitUntracked: lipgloss.Color("#636d83"),
			GitConflicted: lipgloss.Color("#ff7b72"),
			IconDir:      lipgloss.Color("#f0883e"),
			IconFile:     lipgloss.Color("#8b949e"),
			IconSymlink:  lipgloss.Color("#79c0ff"),
			BorderFg:     lipgloss.Color("#30363d"),
		},
	}

	s := Spacing{
		PadLR:        2,
		FieldGap:     1,
		ButtonGap:    2,
		DialogPad:    2,
		FormIndent:   0,
		LabelColGap:  3,
		FormTitlePad: 2,
	}

	tc := TreeConfig{
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

	ws := WindowStyles{
		Border: lipgloss.NewStyle().
			Foreground(c.Border),
		Title: lipgloss.NewStyle().
			Foreground(c.Muted),
		TitleFocused: lipgloss.NewStyle().
			Foreground(c.Fg).
			Bold(true),
		TitleBg: lipgloss.NewStyle().
			Background(c.SubPaneBg),
		TitleBgFocused: lipgloss.NewStyle().
			Background(c.Selection),
		BodyBg: lipgloss.NewStyle().
			Background(c.SubPaneBg),
		CloseBtn: lipgloss.NewStyle().
			Foreground(c.Muted),
		CloseBtnFocused: lipgloss.NewStyle().
			Foreground(c.Error).
			Bold(true),
		BorderFocused: lipgloss.NewStyle().
			Foreground(c.Accent),
	}

	st := Styles{
		Header: lipgloss.NewStyle().
			Foreground(c.Accent).
			Bold(true),
		Separator: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#333333")),
		DimLabel: lipgloss.NewStyle().
			Foreground(c.Muted),
		AccentLabel: lipgloss.NewStyle().
			Foreground(c.Accent2).
			Italic(true),
		Body: lipgloss.NewStyle().
			Foreground(c.Fg),
		Button: lipgloss.NewStyle().
			Foreground(c.Fg),
		ButtonFocus: lipgloss.NewStyle().
			Foreground(c.Fg).
			Background(c.Border),
		ButtonDim: lipgloss.NewStyle().
			Foreground(c.Muted),
		DialogBg: lipgloss.NewStyle().
			Background(lipgloss.Color("#1a1a2e")).
			Padding(1, 2),
		CodeBlock: lipgloss.NewStyle().
			Foreground(c.Fg).
			Background(c.Code.Bg),
		ToolActive: lipgloss.NewStyle().
			Foreground(c.Accent),
		ToolDone: lipgloss.NewStyle().
			Foreground(c.Success),
		ToolError: lipgloss.NewStyle().
			Foreground(c.Error),
		ThinkingLine: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Italic(true),
		UserLabel: lipgloss.NewStyle().
			Foreground(c.Fg).
			Bold(true).
			Background(lipgloss.Color("#2a2a3a")),
		AsstLabel: lipgloss.NewStyle().
			Foreground(c.Accent2).
			Bold(true).
			Background(lipgloss.Color("#2a2a3a")),
		ErrorText: lipgloss.NewStyle().
			Foreground(c.Error).
			Bold(true),
		StatusBar: lipgloss.NewStyle().
			Foreground(c.Muted),
		InputPrompt: lipgloss.NewStyle().
			Foreground(c.Accent),
		InputText: lipgloss.NewStyle().
			Foreground(c.Fg),
		Highlight: lipgloss.NewStyle().
			Foreground(c.Fg).
			Background(c.Selection),
		DangerText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ff0000")).
			Bold(true),
		RetryText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffaa00")),
		AwaitText: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffaa00")),
		Window: ws,
	}

	ts := TreeStyles{
		DirStyle: lipgloss.NewStyle().
			Foreground(c.Tree.DirFg).
			Bold(true),
		FileStyle: lipgloss.NewStyle().
			Foreground(c.Tree.FileFg),
		SelectedStyle: lipgloss.NewStyle().
			Foreground(c.Tree.SelectedFg).
			Background(c.Tree.SelectedBg),
		CursorStyle: lipgloss.NewStyle().
			Foreground(c.Tree.CursorFg).
			Background(c.Tree.CursorBg),
		GitModifiedStyle: lipgloss.NewStyle().
			Foreground(c.Tree.GitModified),
		GitAddedStyle: lipgloss.NewStyle().
			Foreground(c.Tree.GitAdded),
		GitDeletedStyle: lipgloss.NewStyle().
			Foreground(c.Tree.GitDeleted),
		GitUntrackedStyle: lipgloss.NewStyle().
			Foreground(c.Tree.GitUntracked).
			Italic(true),
	}

	return &Theme{Colors: c, Spacing: s, Styles: st, Tree: Tree{Colors: c.Tree, Styles: ts, Config: tc}}
}
