package widgets

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
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
	if width < 20 {
		width = 20
	}

	lines := make([]string, 0, height)

	// "New Session" entry (always first, index 0)
	newSessionLines := RenderNewSessionEntry(selected == 0, th, width, spinFrame, idleSeconds)
	for _, l := range newSessionLines {
		lines = append(lines, l)
	}

	if len(sessions) > 0 {
		// A visual transition to the past sessions list
		transitionLine := th.Styles.DimLabel.Render(strings.Repeat(" ", width/2 - 12) + "↓ or continue a past session ↓")
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
		lines = append(lines, renderLauncherDateSep(g.Label, width, th))
		for _, s := range g.Sessions {
			isSelected := flatIdx == selected
			lines = append(lines, renderLauncherSessionLine(s, isSelected, width, th))
			flatIdx++
		}
	}

	if len(sessions) == 0 {
		lines = append(lines, th.Styles.DimLabel.Render("No past sessions found."))
	}

	// Pad every line to width so no gap appears on the right, matching
	// how chat/editor content fills the full content width.
	for i, l := range lines {
		lw := rendering.VisibleWidth(l)
		if lw < width {
			lines[i] = l + strings.Repeat(" ", width-lw)
		}
	}

	return lines
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

	taglineW := rendering.VisibleWidth(dim.Render(tagline))
	hintW := rendering.VisibleWidth(dim.Render(hint))
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
func pulseColor(spinFrame int, c1, c2 color.Color) color.Color {
	r1, g1, b1, _ := c1.RGBA()
	r2, g2, b2, _ := c2.RGBA()

	// 3-second breathing period using millis
	phase := float64(spinFrame%3000) / 3000.0
	t := 0.5 - 0.5*math.Cos(2*math.Pi*phase)

	r := lerp(uint8(r1>>8), uint8(r2>>8), t)
	g := lerp(uint8(g1>>8), uint8(g2>>8), t)
	b := lerp(uint8(b1>>8), uint8(b2>>8), t)

	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

func renderLauncherDateSep(label string, width int, th *theme.Theme) string {
	content := " " + label + " "
	contentWidth := rendering.VisibleWidth(content)
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

	cursorWidth := rendering.VisibleWidth(cursor)
	projectWidth := 14
	turnColWidth := 10 // e.g. "12 turns "
	dotWidth := 3     // " · "
	modelColWidth := 24 // e.g. "claude-opus-4-6       "
	metaTotalWidth := turnColWidth + dotWidth + modelColWidth

	// Right-pad turn count to fixed width so the "·" aligns
	paddedTurn := turnStr
	for rendering.VisibleWidth(paddedTurn) < turnColWidth {
		paddedTurn += " "
	}

	// Right-align model name (left-pad) so right edge aligns across rows
	paddedModel := modelStr
	for rendering.VisibleWidth(paddedModel) < modelColWidth {
		paddedModel = " " + paddedModel
	}

	// Calculate prompt width from remaining space
	maxPrompt := width - cursorWidth - projectWidth - 4 - metaTotalWidth
	if maxPrompt < 8 {
		maxPrompt = 8
	}
	if rendering.VisibleWidth(firstPrompt) > maxPrompt {
		firstPrompt = rendering.TruncateLine(firstPrompt, maxPrompt)
	}

	nameStr := projectName
	if rendering.VisibleWidth(nameStr) > projectWidth {
		nameStr = rendering.TruncateLine(nameStr, projectWidth)
	}
	for rendering.VisibleWidth(nameStr) < projectWidth {
		nameStr += " "
	}

	promptStr := firstPrompt
	for rendering.VisibleWidth(promptStr) < maxPrompt {
		promptStr += " "
	}

	metaStr := th.Styles.DimLabel.Render(paddedTurn) + th.Styles.DimLabel.Render(" · ") + th.Styles.DimLabel.Render(paddedModel)

	return cursor + th.Styles.Body.Render(nameStr) + "  " +
		th.Styles.DimLabel.Render(promptStr) + "  " +
		metaStr
}
