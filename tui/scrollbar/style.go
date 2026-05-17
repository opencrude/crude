package scrollbar

import "github.com/charmbracelet/lipgloss"

var (
	defaultTrackStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#303030"))
	defaultThumbStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#767676"))
	defaultFadeThumbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4e4e4e"))
)

func styleChar(ch string, style lipgloss.Style) string {
	return style.Render(ch)
}
