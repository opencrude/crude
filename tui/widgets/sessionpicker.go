package widgets

import (
	"fmt"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/theme"
)

type SessionPickerGroup struct {
	Label    string
	Sessions []sessiondb.SessionMeta
}

func BuildSessionGroups(sessions []sessiondb.SessionMeta) []SessionPickerGroup {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)

	var groups []SessionPickerGroup
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
				groups = append(groups, SessionPickerGroup{Label: currentLabel, Sessions: currentSessions})
			}
			currentKey = key
			currentLabel = label
			currentSessions = nil
		}
		currentSessions = append(currentSessions, s)
	}
	if len(currentSessions) > 0 {
		groups = append(groups, SessionPickerGroup{Label: currentLabel, Sessions: currentSessions})
	}

	return groups
}

func RenderSessionPicker(sessions []sessiondb.SessionMeta, selected int, focused bool, th *theme.Theme, contentWidth int) []string {
	if len(sessions) == 0 {
		return []string{
			th.Styles.DimLabel.Render("No past sessions found."),
			th.Styles.DimLabel.Render("Type a message to start a new conversation."),
		}
	}

	groups := BuildSessionGroups(sessions)
	var lines []string
	flatIdx := 0

	for _, g := range groups {
		lines = append(lines, renderDateSep(g.Label, contentWidth, th))
		for _, s := range g.Sessions {
			isSelected := flatIdx == selected
			lines = append(lines, renderSessionLine(s, isSelected, focused, contentWidth, th))
			flatIdx++
		}
	}

	return lines
}

func renderDateSep(label string, width int, th *theme.Theme) string {
	sepStyle := th.Styles.Separator
	content := " " + label + " "
	contentWidth := rendering.VisibleWidth(content)
	remaining := width - contentWidth
	if remaining < 0 {
		remaining = 0
	}
	left := remaining / 2
	right := remaining - left
	return sepStyle.Render(strings.Repeat("─", left) + content + strings.Repeat("─", right))
}

func renderSessionLine(s sessiondb.SessionMeta, selected bool, focused bool, width int, th *theme.Theme) string {
	var cursor string
	if selected && focused {
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

	meta := fmt.Sprintf("%d turns · %s", s.TurnCount, s.Model)

	cursorWidth := rendering.VisibleWidth(cursor)
	metaWidth := rendering.VisibleWidth(meta)
	projectWidth := 14
	spacingWidth := 4

	maxPrompt := width - cursorWidth - projectWidth - spacingWidth - metaWidth
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
	promptPad := maxPrompt - rendering.VisibleWidth(promptStr)
	if promptPad > 0 {
		promptStr += strings.Repeat(" ", promptPad)
	}

	if selected && focused {
		line := cursor + nameStr + "  " + promptStr + "  " + meta
		return th.Styles.Highlight.Render(line)
	}

	return cursor + th.Styles.Body.Render(nameStr) + "  " +
		th.Styles.DimLabel.Render(promptStr) + "  " +
		th.Styles.DimLabel.Render(meta)
}

func SessionPickerFlatIndex(sessions []sessiondb.SessionMeta, selected int) string {
	if selected >= 0 && selected < len(sessions) {
		return sessions[selected].ID
	}
	return ""
}
