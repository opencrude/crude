package tools

import (
	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func RenderToolMessage(msg types.ChatMessage, th *theme.Theme, contentWidth, innerW, width, spinFrame, height int, phase string) []string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	isActive := msg.ToolStatus == "streaming" || msg.ToolStatus == "calling"

	spinColor := th.Colors.Spinner.Default
	if isActive {
		switch phase {
		case "grounding":
			spinColor = th.Colors.Spinner.Grounding
		case "planning":
			spinColor = th.Colors.Spinner.Planning
		case "executing":
			spinColor = th.Colors.Spinner.Executing
		}
	} else if msg.ToolStatus == "success" {
		spinColor = th.Colors.Spinner.Done
	} else if msg.ToolStatus == "error" {
		spinColor = th.Colors.Spinner.Error
	}
	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	nameStyle := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	title := BuildToolTitle(msg, phase)

	var msgLines []string

	if isActive {
		var dot string
		if components.BlinkOn(spinFrame) {
			dot = "● "
		} else {
			dot = "○ "
		}
		line := dim.Render("│ ") + spinStyle.Render(dot) + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "success" && msg.ToolName == "plan" {
	} else if msg.ToolStatus == "success" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else if msg.ToolStatus == "error" {
		line := dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	} else {
		line := dim.Render("│ ") + spinStyle.Render("○ ") + nameStyle.Render(title)
		msgLines = append(msgLines, line)
	}

	if msg.ToolStatus == "success" && msg.Content != "" {
		resultContent := msg.Content
		fullLabel := rendering.PascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
		resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
		resultContent = strings.TrimSpace(resultContent)
		if resultContent != "" {
			if IsPlanTool(msg.ToolName) {
				planBox := RenderPlanBox(resultContent, contentWidth-4, th)
				for _, pl := range strings.Split(planBox, "\n") {
					msgLines = append(msgLines, dim.Render("│ ")+pl)
				}
			} else if msg.ToolName == "read_file" {
				path := msg.ToolArgs
				if path == "" {
					path = msg.ToolProgressLabel
				}
				if path == "" {
					path = "file"
				}
				if strings.Contains(resultContent, "— (empty file)") {
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("(empty file)"))
				} else if strings.HasPrefix(resultContent, "Error:") {
					firstLine := strings.SplitN(resultContent, "\n", 2)[0]
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(firstLine))
				} else {
					contentLines := len(strings.Split(resultContent, "\n"))
					if strings.HasPrefix(resultContent, "Lines") {
						contentLines--
					}
					if contentLines < 1 {
						contentLines = 1
					}
					suffix := "s"
					if contentLines == 1 {
						suffix = ""
					}
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("Read %d line%s from %s", contentLines, suffix, path)))
					if strings.HasPrefix(resultContent, "Lines") {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("… partial content returned to agent"))
					} else {
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render("… full content returned to agent"))
					}
				}
			} else if isDiffTool(msg.ToolName) || IsDiffContent(resultContent) {
				// Use the new unified RenderDiff for all diff-like content
				maxRes := TerminalResultHeight(height)
				diffWidth := innerW - 4
				if diffWidth < 20 {
					diffWidth = 20
				}
				renderedDiff := RenderDiff(resultContent, maxRes, diffWidth, th)
				if renderedDiff != "" {
					for _, dl := range strings.Split(renderedDiff, "\n") {
						msgLines = append(msgLines, dim.Render("│ ")+dl)
					}
				} else {
					// Fallback for non-diff content
					firstLine := strings.SplitN(resultContent, "\n", 2)[0]
					msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(firstLine))
				}
			} else {
				toolLineWidth := width - th.Spacing.PadLR*2 - 6
				if toolLineWidth < 20 {
					toolLineWidth = 20
				}
				var wrappedLines []string
				for _, rl := range strings.Split(resultContent, "\n") {
					trimmed := strings.TrimLeft(rl, " ")
					for _, seg := range rendering.HardWrap(trimmed, toolLineWidth) {
						wrappedLines = append(wrappedLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(seg))
					}
				}
				maxRes := TerminalResultHeight(height)
				for i, wl := range wrappedLines {
					if i >= maxRes-1 {
						remaining := len(wrappedLines) - i
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(fmt.Sprintf("+%d more lines", remaining)))
						break
					}
					msgLines = append(msgLines, wl)
				}
			}
		}
	}

	return msgLines
}

// isDiffTool returns true for tool names that produce diff-like output.
func isDiffTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "delete_lines", "replace_lines":
		return true
	}
	return false
}

func RenderPlanBox(text string, maxWidth int, th *theme.Theme) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}

	var styledLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			styledLines = append(styledLines, "")
			continue
		}

		var styled string
		switch {
		case strings.HasPrefix(trimmed, "✓"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Success).Render(line)
		case strings.HasPrefix(trimmed, "→"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Accent2).Render(line)
		case strings.HasPrefix(trimmed, "✗"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Error).Render(line)
		case strings.HasPrefix(trimmed, "!"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Warning).Render(line)
		case strings.HasPrefix(trimmed, "☐"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(line)
		case strings.HasPrefix(trimmed, "Plan:") || strings.HasPrefix(trimmed, "Goal:"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render(line)
		case strings.HasPrefix(trimmed, "Suspended:"):
			styled = lipgloss.NewStyle().Foreground(th.Colors.Warning).Render(line)
		default:
			styled = lipgloss.NewStyle().Foreground(th.Colors.Muted).Render(line)
		}
		styledLines = append(styledLines, styled)
	}

	content := strings.Join(styledLines, "\n")

	box := lipgloss.NewStyle().
		Padding(0, 1).
		Width(maxWidth)

	return box.Render(content)
}

func TerminalResultHeight(height int) int {
	h := height / 3
	if h < 4 {
		h = 4
	}
	return h
}
