package widgets

import (
	"fmt"
	"strings"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
)

// AgentTab represents one sub-agent in the tab bar.
type AgentTab struct {
	ID       string // agent ID (e.g. "explorer-abc123")
	Label    string // short display name (e.g. "explorer")
	Category string // "researcher", "crafter", "aeron"
	Status   string // "running", "completed", "failed_timeout", "failed_error", "failed_quota", "failed_policy", "cancelled"
	Elapsed  int    // seconds since start
	IsActive bool   // true if this tab is currently selected/viewed
}

// AgentTabBar manages and renders the vertical agent tab bar.
type AgentTabBar struct {
	theme *theme.Theme
	tabs  []AgentTab
	width int // computed width (0 when no tabs)
}

const (
	tabMinWidth = 10
	tabMaxWidth = 20
)

// NewAgentTabBar creates a new agent tab bar widget.
func NewAgentTabBar(th *theme.Theme) *AgentTabBar {
	return &AgentTabBar{theme: th}
}

// SetTabs updates the tab list and recomputes the width.
func (b *AgentTabBar) SetTabs(tabs []AgentTab) {
	b.tabs = tabs
	b.width = b.computeWidth()
}

// TabCount returns the number of tabs.
func (b *AgentTabBar) TabCount() int {
	return len(b.tabs)
}

// Width returns the computed width of the tab bar (0 when no tabs).
func (b *AgentTabBar) Width() int {
	return b.width
}

// computeWidth determines the tab bar width from the widest tab content.
// Format per line: "│ S label NNs│" where S is the status symbol.
// Overhead: "│ " (2) + " " (1 after symbol) + " " (1 before time) + time + "│" (1)
func (b *AgentTabBar) computeWidth() int {
	if len(b.tabs) == 0 {
		return 0
	}
	maxContent := 0
	for _, t := range b.tabs {
		// symbol(1) + space(1) + label + space(1) + elapsed
		elapsed := formatElapsed(t.Elapsed)
		content := 1 + 1 + rendering.StringWidth(t.Label) + 1 + rendering.StringWidth(elapsed)
		if content > maxContent {
			maxContent = content
		}
	}
	// total: "│" (1) + space(1) + content + "│" (1)
	w := 1 + 1 + maxContent + 1
	if w < tabMinWidth {
		w = tabMinWidth
	}
	if w > tabMaxWidth {
		w = tabMaxWidth
	}
	return w
}

// RenderLines returns exactly `height` lines, each of width b.Width().
func (b *AgentTabBar) RenderLines(height, spinFrame int) []string {
	w := b.width
	if w == 0 || height <= 0 {
		lines := make([]string, height)
		for i := range lines {
			lines[i] = ""
		}
		return lines
	}

	borderStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Border)
	border := borderStyle.Render("│")

	lines := make([]string, 0, height)

	for i, tab := range b.tabs {
		if len(lines) >= height {
			break
		}
		lines = append(lines, b.renderTabLine(tab, w, spinFrame, border, borderStyle))

		// Separator after each tab (if room remains and not last tab)
		if len(lines) < height && i < len(b.tabs)-1 {
			lines = append(lines, b.renderSeparator(w, border, borderStyle))
		}
	}

	// Fill remaining lines with empty bar lines
	for len(lines) < height {
		lines = append(lines, b.renderEmptyLine(w, border))
	}

	return lines
}

// renderTabLine renders a single tab line of the given total width.
func (b *AgentTabBar) renderTabLine(tab AgentTab, totalWidth, spinFrame int, border string, borderStyle lipgloss.Style) string {
	symbol := b.statusSymbol(tab, spinFrame)
	elapsed := formatElapsed(tab.Elapsed)

	// Available space for label: totalWidth - "│"(1) - " "(1) - symbol_visual(1) - " "(1) - elapsed - "│"(1)
	elapsedWidth := rendering.StringWidth(elapsed)
	// inner = totalWidth - 2 (for the two │ chars)
	inner := totalWidth - 2
	// inner layout: " " + symbol(1) + " " + label + " " + elapsed
	// space for label = inner - 1 - 1 - 1 - 1 - elapsedWidth
	labelSpace := inner - 1 - 1 - 1 - elapsedWidth
	if labelSpace < 0 {
		labelSpace = 0
	}

	label := tab.Label
	if rendering.StringWidth(label) > labelSpace {
		label = rendering.TruncateVisual(label, labelSpace, "")
	}

	// Pad label to fill the space
	labelPad := labelSpace - rendering.StringWidth(label)
	if labelPad < 0 {
		labelPad = 0
	}

	// Style the label and elapsed based on active state
	var styledLabel, styledElapsed string
	if tab.IsActive {
		labelStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Fg).Bold(true)
		elapsedStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Muted)
		styledLabel = labelStyle.Render(label)
		styledElapsed = elapsedStyle.Render(elapsed)
	} else {
		mutedStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Muted)
		styledLabel = mutedStyle.Render(label)
		styledElapsed = mutedStyle.Render(elapsed)
	}

	return border + " " + symbol + " " + styledLabel + strings.Repeat(" ", labelPad) + " " + styledElapsed + borderStyle.Render("│")
}

// renderSeparator renders a thin horizontal separator line.
func (b *AgentTabBar) renderSeparator(totalWidth int, border string, borderStyle lipgloss.Style) string {
	inner := totalWidth - 2
	sepStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Border)
	return border + sepStyle.Render(strings.Repeat("─", inner)) + borderStyle.Render("│")
}

// renderEmptyLine renders an empty bar line filled with spaces.
func (b *AgentTabBar) renderEmptyLine(totalWidth int, border string) string {
	inner := totalWidth - 2
	borderStyle := lipgloss.NewStyle().Foreground(b.theme.Colors.Border)
	return border + strings.Repeat(" ", inner) + borderStyle.Render("│")
}

// statusSymbol returns the styled status symbol for a tab.
func (b *AgentTabBar) statusSymbol(tab AgentTab, spinFrame int) string {
	switch {
	case tab.Status == "running":
		color := b.theme.Colors.Accent
		style := lipgloss.NewStyle().Foreground(color)
		if components.BlinkOn(spinFrame) {
			return style.Render("●")
		}
		return style.Render("○")

	case tab.Status == "completed":
		return lipgloss.NewStyle().Foreground(b.theme.Colors.Success).Render("✓")

	case strings.HasPrefix(tab.Status, "failed"):
		return lipgloss.NewStyle().Foreground(b.theme.Colors.Error).Render("✗")

	case tab.Status == "cancelled":
		return lipgloss.NewStyle().Foreground(b.theme.Colors.Warning).Render("!")

	default:
		if tab.IsActive {
			return lipgloss.NewStyle().Foreground(b.theme.Colors.Accent).Render("■")
		}
		return lipgloss.NewStyle().Foreground(b.theme.Colors.Muted).Render("■")
	}
}

// TabList returns a copy of the current tab list.
func (b *AgentTabBar) TabList() []AgentTab {
	result := make([]AgentTab, len(b.tabs))
	copy(result, b.tabs)
	return result
}

// StatusSymbol returns the styled status symbol for a tab (exported for agent view rendering).
func (b *AgentTabBar) StatusSymbol(tab AgentTab, spinFrame int) string {
	return b.statusSymbol(tab, spinFrame)
}

// FormatElapsed formats a duration in seconds as a compact string (exported).
func FormatElapsed(seconds int) string {
	return formatElapsed(seconds)
}

// HitTest returns which tab was clicked given x, y coordinates.
// y is 0-based line index. Each tab occupies line i*2, with separators on i*2+1.
// Returns the tab ID and whether the click is on a close area (not applicable for main).
func (b *AgentTabBar) HitTest(x, y int) (tabID string, isClose bool) {
	if b.width == 0 || x < 0 || x >= b.width || y < 0 {
		return "", false
	}

	// Each tab is at line i*2, separator at i*2+1
	for i, tab := range b.tabs {
		tabLine := i * 2
		if y == tabLine {
			// Hit on this tab
			return tab.ID, false
		}
	}

	return "", false
}

// formatElapsed formats a duration in seconds as a compact string.
func formatElapsed(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%dh", seconds/3600)
}
