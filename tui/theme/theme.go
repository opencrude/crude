package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type Theme struct {
	Colors  Colors
	Spacing Spacing
	Styles  Styles
}

type Colors struct {
	Bg, Fg, Muted, Accent, Accent2  color.Color
	Success, Error, Warning, Border color.Color
	Selection, SubPaneBg            color.Color
	Code                            CodeColors
	Spinner                         SpinnerColors
	Diff                            DiffColors
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

type Spacing struct {
	PadLR        int
	FieldGap     int
	ButtonGap    int
	DialogPad    int
	FormIndent   int
	LabelColGap  int
	FormTitlePad int
}

type Styles struct {
	Header, Separator, DimLabel, AccentLabel     lipgloss.Style
	Body, Button, ButtonFocus, ButtonDim, DialogBg lipgloss.Style
	CodeBlock, ToolActive, ToolDone, ToolError    lipgloss.Style
	ThinkingLine, UserLabel, AsstLabel            lipgloss.Style
	ErrorText, StatusBar, InputPrompt, InputText  lipgloss.Style
	Highlight, DangerText, RetryText, AwaitText     lipgloss.Style
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
		SubPaneBg: lipgloss.Color("#161b22"),
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
	}

	return &Theme{Colors: c, Spacing: s, Styles: st}
}
