package widgets

import (
	"fmt"
	"math"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

// FormatTurnCount formats a turn count for display.
func FormatTurnCount(turns int) string {
	if turns == 1 {
		return "1 turn"
	}
	return fmt.Sprintf("%d turns", turns)
}

// LauncherGroup is a date-grouped set of sessions for the launcher.
type LauncherGroup struct {
	Label    string
	Sessions []sessiondb.SessionMeta
}

// BuildLauncherGroups groups sessions by date for display.
func BuildLauncherGroups(sessions []sessiondb.SessionMeta) []LauncherGroup {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	var groups []LauncherGroup
	var currentKey string
	var currentLabel string
	var currentSessions []sessiondb.SessionMeta

	for _, s := range sessions {
		var key, label string
		if s.CreatedAt > 0 {
			t := time.Unix(0, s.CreatedAt)
			day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			key = day.Format("2006-01-02")
			if day.Equal(today) {
				label = "today"
			} else if day.Equal(yesterday) {
				label = "yesterday"
			} else {
				label = day.Format("Mon Jan 2, 2006")
			}
		} else {
			key = "unknown"
			label = "unknown"
		}

		if key != currentKey {
			if len(currentSessions) > 0 {
				groups = append(groups, LauncherGroup{Label: currentLabel, Sessions: currentSessions})
			}
			currentKey = key
			currentLabel = label
			currentSessions = nil
		}
		currentSessions = append(currentSessions, s)
	}
	if len(currentSessions) > 0 {
		groups = append(groups, LauncherGroup{Label: currentLabel, Sessions: currentSessions})
	}

	return groups
}

// RenderLauncher renders the full-screen session launcher view.
// Returns a slice of lines (without padding — caller applies padding).
// selected is 0-based: 0 = "New Session", 1..n = sessions in visible order.
func RenderLauncher(sessions []sessiondb.SessionMeta, selected int, filterActive bool, filterText string, th *theme.Theme, width, height, spinFrame int, idleSeconds float64) []string {
	innerW := width - th.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	lines := make([]string, 0, height)

	// "New Session" entry (always first, index 0)
	newSessionLines := RenderNewSessionEntry(selected == 0, th, innerW, spinFrame, idleSeconds)
	for _, l := range newSessionLines {
		lines = append(lines, l)
	}

	if len(sessions) > 0 {
		// A visual transition to the past sessions list
		transitionLine := th.Styles.DimLabel.Render(strings.Repeat(" ", innerW/2 - 12) + "↓ or continue a past session ↓")
		lines = append(lines, transitionLine)
		lines = append(lines, "")
	} else {
		lines = append(lines, "") // blank spacer
	}

	// Session list with date groups
	groups := BuildLauncherGroups(sessions)
	flatIdx := 1 // starts at 1 because 0 is "New Session"

	for _, g := range groups {
		// Date separator
		lines = append(lines, renderLauncherDateSep(g.Label, innerW, th))
		for _, s := range g.Sessions {
			isSelected := flatIdx == selected
			lines = append(lines, renderLauncherSessionLine(s, isSelected, innerW, th))
			flatIdx++
		}
	}

	if len(sessions) == 0 {
		lines = append(lines, th.Styles.DimLabel.Render("No past sessions found."))
	}

	return lines
}

// RenderLauncherHints renders the keybinding hints bar.
func RenderLauncherHints(filterActive bool, filterText string, th *theme.Theme, width int) string {
	if filterActive {
		filterDisplay := "/" + filterText
		return th.Styles.DimLabel.Render("[ ") +
			th.Styles.Body.Render(filterDisplay) +
			th.Styles.DimLabel.Render(" · esc: cancel · enter: select · ↑↓: navigate ]")
	}
	return th.Styles.DimLabel.Render("[ ↑↓: navigate · enter: select · /: filter · d: delete · q: quit ]")
}

// RenderLauncherPreviewHints renders a single combined line with session preview and keybinding hints.
func RenderLauncherPreviewHints(preview string, filterActive bool, filterText string, th *theme.Theme, width int) string {
	innerW := width - th.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	if preview == "" {
		return th.Styles.DimLabel.Render("[ ") +
			th.Styles.Body.Render("Start a new conversation") +
			th.Styles.DimLabel.Render(" · ↑↓: navigate · enter: select ]")
	}

	previewRendered := th.Styles.Body.Render(preview)

	var hints string
	if filterActive {
		filterDisplay := "/" + filterText
		hints = th.Styles.Body.Render(filterDisplay) +
			th.Styles.DimLabel.Render(" · esc: cancel · enter: select · ↑↓: navigate")
	} else {
		hints = th.Styles.DimLabel.Render("↑↓: navigate · enter: select · /: filter · d: delete · q: quit")
	}

	previewW := lipgloss.Width(previewRendered)
	dotW := lipgloss.Width(th.Styles.DimLabel.Render(" · "))
	bracketW := lipgloss.Width(th.Styles.DimLabel.Render("[ ") + th.Styles.DimLabel.Render(" ]"))
	availForHints := innerW - bracketW - dotW - previewW
	hintsRendered := hints

	if lipgloss.Width(hintsRendered) > availForHints && availForHints > 0 {
		hintsRendered = hints
		for lipgloss.Width(hintsRendered) > availForHints {
			hintsRendered = hintsRendered[:len(hintsRendered)-1]
		}
	}

	return th.Styles.DimLabel.Render("[ ") + previewRendered + th.Styles.DimLabel.Render(" · ") + hintsRendered + th.Styles.DimLabel.Render(" ]")
}

// RenderLauncherPreview renders the preview pane for the selected session.
func RenderLauncherPreview(previewLines []string, th *theme.Theme, width int) []string {
	innerW := width - th.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	var result []string
	for _, line := range previewLines {
		if lipgloss.Width(line) > innerW {
			line = line[:innerW-1] + "…"
		}
		result = append(result, th.Styles.DimLabel.Render(line))
	}
	return result
}

var sparkleFrames = []string{"✨", "🌟", "✨", "💫", "✨", "⭐"}

func sparkle(frame int) string {
	return sparkleFrames[frame%len(sparkleFrames)]
}

func RenderNewSessionEntry(selected bool, th *theme.Theme, width int, spinFrame int, idleSeconds float64) []string {
	boxW := width

	titleStyle := lipgloss.NewStyle().Bold(true)
	if selected {
		titleStyle = titleStyle.Foreground(th.Colors.Accent)
	} else {
		titleStyle = titleStyle.Foreground(th.Colors.Fg)
	}

	icon := sparkle(spinFrame / 10000) // change every ~10s
	title := icon + " New Session"
	hint := "enter ↵"
	tagline := "Start a blank canvas and explore new ideas..."

	dim := th.Styles.DimLabel

	innerW := boxW - 4

	topLine := titleStyle.Render(title)

	taglineW := lipgloss.Width(dim.Render(tagline))
	hintW := lipgloss.Width(dim.Render(hint))
	spacerW := innerW - taglineW - hintW
	if spacerW < 1 {
		spacerW = 1
	}
	middleLine := dim.Render(tagline) + strings.Repeat(" ", spacerW) + dim.Render(hint)

	content := topLine + "\n" + middleLine

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(boxW)

	if selected && idleSeconds >= 5.0 {
		boxStyle = boxStyle.BorderForeground(pulseColor(spinFrame, th.Colors.Border, th.Colors.Accent))
	} else if selected {
		boxStyle = boxStyle.BorderForeground(th.Colors.Accent)
	} else {
		boxStyle = boxStyle.BorderForeground(th.Colors.Border)
	}

	box := boxStyle.Render(content)
	return strings.Split(box, "\n")
}

// pulseColor creates a slow breathing glow between two colors.
// spinFrame is the Unix millisecond timestamp; period is 3 seconds.
func pulseColor(spinFrame int, c1, c2 lipgloss.Color) lipgloss.Color {
	r1, g1, b1 := parseHexColor(string(c1))
	r2, g2, b2 := parseHexColor(string(c2))

	// 3-second breathing period using millis
	phase := float64(spinFrame%3000) / 3000.0
	t := 0.5 - 0.5*math.Cos(2*math.Pi*phase)

	r := lerp(r1, r2, t)
	g := lerp(g1, g2, t)
	b := lerp(b1, b2, t)

	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

func parseHexColor(hex string) (r, g, b uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 128, 128, 128
	}
	r = hexByte(hex[0:2])
	g = hexByte(hex[2:4])
	b = hexByte(hex[4:6])
	return
}

func hexByte(s string) uint8 {
	var v uint8
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint8(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint8(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint8(c-'A') + 10
		}
	}
	return v
}

func renderLauncherDateSep(label string, width int, th *theme.Theme) string {
	content := " " + label + " "
	contentWidth := lipgloss.Width(content)
	remaining := width - contentWidth
	if remaining < 0 {
		remaining = 0
	}
	left := remaining / 2
	right := remaining - left
	sepStyle := th.Styles.Separator
	return sepStyle.Render(strings.Repeat("─", left) + content + strings.Repeat("─", right))
}

func renderLauncherSessionLine(s sessiondb.SessionMeta, selected bool, width int, th *theme.Theme) string {
	var cursor string
	if selected {
		cursor = th.Styles.InputPrompt.Render("❯") + " "
	} else {
		cursor = "  "
	}

	projectName := s.ProjectName
	if projectName == "" {
		projectName = "untitled"
	}

	firstPrompt := ""
	if s.FirstPrompt.Valid {
		firstPrompt = s.FirstPrompt.String
	}

	// Column-aligned meta: right-align turn count so numbers start at same column
	turnStr := FormatTurnCount(s.TurnCount)
	modelStr := s.Model

	cursorWidth := lipgloss.Width(cursor)
	projectWidth := 14
	turnColWidth := 10 // e.g. "12 turns "
	dotWidth := 3     // " · "
	modelColWidth := 24 // e.g. "claude-opus-4-6       "
	metaTotalWidth := turnColWidth + dotWidth + modelColWidth

	// Right-pad turn count to fixed width so the "·" aligns
	paddedTurn := turnStr
	for lipgloss.Width(paddedTurn) < turnColWidth {
		paddedTurn += " "
	}

	// Right-align model name (left-pad) so right edge aligns across rows
	paddedModel := modelStr
	for lipgloss.Width(paddedModel) < modelColWidth {
		paddedModel = " " + paddedModel
	}

	// Calculate prompt width from remaining space
	maxPrompt := width - cursorWidth - projectWidth - 4 - metaTotalWidth
	if maxPrompt < 8 {
		maxPrompt = 8
	}
	if len(firstPrompt) > maxPrompt {
		firstPrompt = firstPrompt[:maxPrompt-1] + "…"
	}

	nameStr := projectName
	if len(nameStr) > projectWidth {
		nameStr = nameStr[:projectWidth-1] + "…"
	}
	for len(nameStr) < projectWidth {
		nameStr += " "
	}

	promptStr := firstPrompt
	for len(promptStr) < maxPrompt {
		promptStr += " "
	}

	metaStr := th.Styles.DimLabel.Render(paddedTurn) + th.Styles.DimLabel.Render(" · ") + th.Styles.DimLabel.Render(paddedModel)

	return cursor + th.Styles.Body.Render(nameStr) + "  " +
		th.Styles.DimLabel.Render(promptStr) + "  " +
		metaStr
}
