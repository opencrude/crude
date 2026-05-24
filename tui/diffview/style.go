package diffview

import "charm.land/lipgloss/v2"

type LineStyle struct {
	LineNumber lipgloss.Style
	Symbol     lipgloss.Style
	Code       lipgloss.Style
}

type Style struct {
	DividerLine LineStyle
	MissingLine LineStyle
	EqualLine   LineStyle
	InsertLine  LineStyle
	DeleteLine  LineStyle
	Filename    LineStyle
}

func DefaultDarkStyle() Style {
	return Style{
		DividerLine: LineStyle{
			LineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#636d83")).Background(lipgloss.Color("#1e2235")),
			Code:       lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a8b7")).Background(lipgloss.Color("#0d1117")),
		},
		MissingLine: LineStyle{
			LineNumber: lipgloss.NewStyle().Background(lipgloss.Color("#161b22")),
			Code:       lipgloss.NewStyle().Background(lipgloss.Color("#161b22")),
		},
		EqualLine: LineStyle{
			LineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Background(lipgloss.Color("#161b22")),
			Code:       lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3")).Background(lipgloss.Color("#0d1117")),
		},
		InsertLine: LineStyle{
			LineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787")).Background(lipgloss.Color("#16221d")),
			Symbol:     lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787")).Background(lipgloss.Color("#1c2a20")),
			Code:       lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3")).Background(lipgloss.Color("#1c2a20")),
		},
		DeleteLine: LineStyle{
			LineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7b72")).Background(lipgloss.Color("#22181c")),
			Symbol:     lipgloss.NewStyle().Foreground(lipgloss.Color("#ff7b72")).Background(lipgloss.Color("#2a1d1e")),
			Code:       lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3")).Background(lipgloss.Color("#2a1d1e")),
		},
		Filename: LineStyle{
			LineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#636d83")).Background(lipgloss.Color("#1e2235")),
			Code:       lipgloss.NewStyle().Foreground(lipgloss.Color("#636d83")).Background(lipgloss.Color("#1e2235")),
		},
	}
}
