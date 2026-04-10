package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const padLR = 2

// activeBackendLabel is set by main to display the current backend in the footer.
var activeBackendLabel = "local"

func (m *Model) View() string {
	return m.chatView()
}

func (m *Model) chatView() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	pad := strings.Repeat(" ", padLR)
	innerW := m.width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	lines := make([]string, 0, m.height)
	add := func(s string) {
		if len(lines) < m.height {
			lines = append(lines, pad+s)
		}
	}
	addFull := func(s string) {
		if len(lines) < m.height {
			lines = append(lines, s)
		}
	}

	dim := lipgloss.NewStyle().Foreground(colorMuted)

	// top padding
	add("")

	// Header
	uptime := time.Since(m.startTime).Truncate(time.Second)
	left := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("crude") +
		" " + dim.Render(truncateLine(m.modelName, innerW/3))
	right := dim.Render(uptime.String())
	spacer := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if spacer < 0 {
		spacer = 0
	}
	add(left + strings.Repeat(" ", spacer) + right)
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	// Chat area
	chatH := m.chatAreaHeight()
	allLines := m.renderAllMessages()
	m.cachedAllLines = allLines

	total := len(allLines)
	maxOffset := total - chatH
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.chatOffset > maxOffset {
		m.chatOffset = maxOffset
	}
	if m.chatOffset < 0 {
		m.chatOffset = 0
	}

	endIdx := total - m.chatOffset
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	visible := allLines[startIdx:endIdx]

	if m.selecting {
		sLine, sCol, eLine, eCol := m.selectionRange()
		for i := range visible {
			allIdx := startIdx + i
			if allIdx < sLine || allIdx > eLine {
				continue
			}
			lineStartCol := 0
			lineEndCol := 1 << 30
			if allIdx == sLine {
				lineStartCol = sCol
			}
			if allIdx == eLine {
				lineEndCol = eCol
			}
			visible[i] = applySelectionReverse(visible[i], lineStartCol, lineEndCol)
		}
	}

	// Pad empty lines at the TOP of chat area so messages stay anchored to bottom
	emptyTop := chatH - len(visible)
	for i := 0; i < emptyTop; i++ {
		add("")
	}
	for _, l := range visible {
		add(l)
	}

	// Input area
	sepStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	if m.settingsActive {
		msg1 := pad + dim.Render("[ interactive settings active · arrows/tab: navigate · enter: edit · ctrl+s: save · esc: return ]")
		addFull(msg1)
	} else {
		if m.showHint && !m.thinking && len(m.inputBuf) == 0 {
			addFull(pad + dim.Render("[ enter: send · alt+enter: new line · ↑↓: history · ctrl+s: settings · esc: exit ]"))
		}
		var inputContent string
		if m.thinking && !m.blocking {
			inputContent = pad + dim.Italic(true).Render("▶ waiting for response...")
		} else if m.inputSelecting {
			lines := m.inputLines()
			for i, il := range lines {
				prefix := "  "
				if i == 0 {
					prefix = "> "
				}
				lineStr := string(il)
				rendered := pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+lineStr)
				startCol, endCol := m.inputSelVisibleRange(i)
				if startCol >= 0 && endCol > startCol {
					rendered = applySelectionReverse(rendered, startCol, endCol)
				}
				inputContent += rendered
				if i < len(lines)-1 {
					inputContent += "\n"
				}
			}
		} else {
			lines := m.inputLines()
			curLine, curCol := m.cursorLineCol()
			for i, il := range lines {
				prefix := "  "
				if i == 0 {
					prefix = "> "
				}
				lineStr := string(il)
				if i == curLine {
					left := string(il[:curCol])
					right := string(il[curCol:])
					var cursorAndRight string
					if curCol < len(il) {
						underCursor := string(il[curCol : curCol+1])
						rest := string(il[curCol+1:])
						cursorAndRight = lipgloss.NewStyle().Reverse(true).Render(underCursor) + lipgloss.NewStyle().Foreground(colorMainText).Render(rest)
					} else {
						cursorAndRight = blockCursor(m.spinFrame, colorAccent) + lipgloss.NewStyle().Foreground(colorMainText).Render(right)
					}
					inputContent += pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+left) + cursorAndRight
				} else {
					inputContent += pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+lineStr)
				}
				if i < len(lines)-1 {
					inputContent += "\n"
				}
			}
		}
		for _, il := range strings.Split(inputContent, "\n") {
			addFull(il)
		}

		inputStr := string(m.inputBuf)
		if m.slashActive && !m.thinking && !strings.Contains(inputStr, "\n") {
			accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
			highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

			for i, c := range m.slashFiltered {
				if i >= 8 {
					addFull(pad + dim.Render(fmt.Sprintf("  ... and %d more", len(m.slashFiltered)-8)))
					break
				}
				selected := i == m.slashIdx
				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
				}
				addFull(pad + line)
			}
		}
	}

	// Bottom border below input
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	// Status bar
	var errLeft string
	if m.infoMsg != "" {
		errLeft = pad + lipgloss.NewStyle().Foreground(colorMuted).Render(
			truncateLine(m.infoMsg, m.width/2))
	} else if m.lastError != "" {
		errLeft = pad + lipgloss.NewStyle().Foreground(colorError).Render(
			truncateLine(m.lastError, m.width/2))
	} else if m.streaming {
		errLeft = pad + streamAnimation(m.spinFrame, m.statusPhase)
	} else if m.thinking {
		errLeft = pad + workAnimation(m.spinFrame, m.statusPhase)
	}

	// Token usage display
	var usageStr string
	if m.tokenPrompt > 0 || m.tokenCompletion > 0 {
		usageStr = dim.Render(fmt.Sprintf(" %s in / %s out", formatTokens(m.tokenPrompt), formatTokens(m.tokenCompletion)))
	}

	var statusRight string
	if m.blocking {
		awaitStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
		statusRight = awaitStyle.Render("⚠ awaiting input")
	} else if m.thinking {
		statusRight = dim.Render(m.statusPhase)
	} else {
		statusRight = dim.Render("ready")
	}
	statusRight = dim.Render(activeBackendLabel) + " " + statusRight + usageStr

	statusW := m.width - lipgloss.Width(errLeft) - lipgloss.Width(statusRight) - padLR
	if statusW < 1 {
		statusW = 1
	}
	statusBar := errLeft + strings.Repeat(" ", statusW) + statusRight + pad

	for len(lines) < m.height-2 {
		addFull("")
	}
	addFull(statusBar)
	addFull("") // bottom margin

	return strings.Join(lines, "\n")
}

// chatAreaHeight returns the available lines for the chat area,
// dynamically accounting for multi-line input.
func (m *Model) chatAreaHeight() int {
	if m.settingsActive {
		h := m.height - 7
		if h < 1 {
			h = 1
		}
		return h
	}
	inputLineCount := 1 + strings.Count(string(m.inputBuf), "\n")
	slashLines := 0
	inputStr := string(m.inputBuf)
	if m.slashActive && !m.thinking && !strings.Contains(inputStr, "\n") {
		slashLines = len(m.slashFiltered)
		if slashLines > 8 {
			slashLines = 9
		}
	}
	hintLines := 0
	if m.showHint && !m.thinking && len(m.inputBuf) == 0 {
		hintLines = 1
	}
	reserved := 2 + 1 + hintLines + inputLineCount + slashLines + 1 + 1 + 1
	h := m.height - reserved
	if h < 1 {
		h = 1
	}
	return h
}

// renderAllMessages converts all ChatMessages into display lines.
func (m *Model) renderAllMessages() []string {
	var all []string
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	contentWidth := m.width - padLR*2 - 16 // leave room for timestamp
	if contentWidth < 10 {
		contentWidth = 10
	}
	innerW := m.width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	for msgIdx, msg := range m.messages {
		isLastMsg := msgIdx == len(m.messages)-1
		timeLine1, timeLine2 := formatTimeLabel(msg.Time)
		timeW := max(len(timeLine1), len(timeLine2))

		// Breathing room between messages
		all = append(all, "")

		switch msg.Role {
		case RoleUser:
			var rendered string
			isMarkdown := false
			md := m.makeMdRenderer()
			if md != nil {
				r, err := md.Render(msg.Content)
				if err == nil {
					rendered = strings.TrimRight(r, "\n")
					isMarkdown = true
				}
			}
			var userLines []string
			if !isMarkdown {
				userLines = wrapText(msg.Content, contentWidth)
			} else {
				userLines = strings.Split(rendered, "\n")
			}

			labelStyle := lipgloss.NewStyle().Foreground(colorMainText).Bold(true).Background(lipgloss.Color("#2a2a3a"))
			if len(userLines) > 0 {
				label := labelStyle.Render("❯ " + m.username + " ")
				userLines[0] = label + userLines[0]
			}

			for i, line := range userLines {
				var timePart string
				if i == 0 {
					timePart = dim.Render(timeLine1)
				} else if i == 1 {
					timePart = dim.Render(timeLine2)
				}
				if timePart != "" {
					lineW := lipgloss.Width(line)
					sp := innerW - lineW - timeW
					if sp < 1 {
						sp = 1
					}
					userLines[i] = line + strings.Repeat(" ", sp) + timePart
				}
			}

			all = append(all, userLines...)
			if len(userLines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

		case RoleAssistant:
			// Try rendering as Markdown
			var rendered string
			isMarkdown := false
			md := m.makeMdRenderer()
			if md != nil {
				r, err := md.Render(msg.Content)
				if err == nil {
					rendered = strings.TrimRight(r, "\n")
					isMarkdown = true
				}
			}
			var asstLines []string
			if !isMarkdown {
				asstLines = wrapText(msg.Content, contentWidth)
			} else {
				asstLines = strings.Split(rendered, "\n")
			}

			// First line: background label + content
			labelStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Background(lipgloss.Color("#2a2a3a"))
			if len(asstLines) > 0 {
				label := labelStyle.Render("❯ Crude " + m.modelName + " ")
				asstLines[0] = label + asstLines[0]
			}

			// Remaining lines: just indented, no background

			// Add timestamps (skip last line when streaming so cursor stays at content end)
			skipLastForStream := m.streaming && isLastMsg
			for i, line := range asstLines {
				if skipLastForStream && i == len(asstLines)-1 {
					continue
				}
				var timePart string
				if i == 0 {
					timePart = dim.Render(timeLine1)
				} else if i == 1 {
					timePart = dim.Render(timeLine2)
				}
				if timePart != "" {
					lineW := lipgloss.Width(line)
					sp := innerW - lineW - timeW
					if sp < 1 {
						sp = 1
					}
					asstLines[i] = line + strings.Repeat(" ", sp) + timePart
				}
			}

			all = append(all, asstLines...)
			// If only one line, show date on the next line
			if len(asstLines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

			// Streaming cursor right after the last content token
			if m.streaming && isLastMsg && len(asstLines) > 0 {
				cursor := blockCursor(m.spinFrame, colorAccent)
				cursorIdx := len(all) - 1
				if len(asstLines) == 1 && len(all) >= 2 {
					cursorIdx = len(all) - 2
				}
				all[cursorIdx] = strings.TrimRight(all[cursorIdx], " ") + cursor
			}

		case RoleThinking:
			lines := wrapText(msg.Content, contentWidth)
			borderPrefix := dim.Render("│ ")
			for i, line := range lines {
				if i == 0 {
					if msg.ThinkingActive {
						all = append(all, borderPrefix+dim.Render(spinnerFrames[m.spinFrame]+" ")+dim.Italic(true).Render(line))
					} else {
						all = append(all, borderPrefix+dim.Render("● ")+dim.Italic(true).Render(line))
					}
				} else {
					all = append(all, borderPrefix+"  "+dim.Italic(true).Render(line))
				}
			}

		case RoleTool:
			isCalling := msg.ToolStatus == "calling"

			spinColor := spinnerColorDefault
			if m.statusPhase == "grounding" {
				spinColor = spinnerColorGrounding
			} else if m.statusPhase == "planning" {
				spinColor = spinnerColorPlanning
			} else if m.statusPhase == "executing" {
				spinColor = spinnerColorExecuting
			}
			if msg.ToolStatus == "success" {
				spinColor = spinnerColorDone
			} else if msg.ToolStatus == "error" {
				spinColor = spinnerColorError
			}

			spinStyle := lipgloss.NewStyle().Foreground(spinColor)
			label := pascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
			nameStyle := lipgloss.NewStyle().Foreground(colorMuted)

			var line string
			if isCalling {
				line = dim.Render("│ ") + spinStyle.Render(spinnerFrames[m.spinFrame]+" ") + nameStyle.Render(label)
			} else if msg.ToolStatus == "success" {
				line = dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(label)
			} else if msg.ToolStatus == "error" {
				line = dim.Render("│ ") + spinStyle.Render("● ") + nameStyle.Render(label)
			} else {
				line = dim.Render("│ ") + spinStyle.Render("○ ") + nameStyle.Render(label)
			}

			all = append(all, line)

		case RoleError:
			lines := wrapText(msg.Content, contentWidth)
			for i, line := range lines {
				styled := lipgloss.NewStyle().Foreground(colorError).Render("  " + line)
				if i == 0 {
					label := lipgloss.NewStyle().Foreground(colorError).Render("error")
					styled = label + " " + lipgloss.NewStyle().Foreground(colorError).Render(line)
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine1))
				} else if i == 1 {
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine2))
				} else {
					all = append(all, styled)
				}
			}
			if len(lines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}
		}
	}

	// If settings is active, render the form at the bottom of messages.
	if m.settingsActive {
		for _, sl := range strings.Split(m.inlineForm.View(m.spinFrame), "\n") {
			all = append(all, sl)
		}
	}

	return all
}

// formatTimeLabel returns two right-aligned strings: "HH:MM" and "today"/"yesterday"/"MM:DD".
func formatTimeLabel(t time.Time) (string, string) {
	line1 := t.Format("15:04")
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if t.After(todayStart) {
		return line1, "today"
	}
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	if t.After(yesterdayStart) {
		return line1, "yesterday"
	}
	return line1, t.Format("01/02")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrapText breaks text into lines of at most width display cells.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var result []string
	for _, para := range strings.Split(text, "\n") {
		if runewidth.StringWidth(para) <= width {
			result = append(result, para)
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		current := words[0]
		for _, w := range words[1:] {
			if runewidth.StringWidth(current)+1+runewidth.StringWidth(w) <= width {
				current += " " + w
			} else {
				result = append(result, current)
				current = w
			}
		}
		result = append(result, current)
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

func truncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}
