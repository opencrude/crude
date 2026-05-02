package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const padLR = 2

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
	} else if m.confirmForm.active {
		msg1 := pad + dim.Render("[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]")
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
					prefix = "❯ "
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
					prefix = "❯ "
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

			const slashPageSize = 8
			total := len(m.slashFiltered)
			if total > 0 {
				// Keep selected item in view
				if m.slashIdx < m.slashListOffset {
					m.slashListOffset = m.slashIdx
				}
				if m.slashIdx >= m.slashListOffset+slashPageSize {
					m.slashListOffset = m.slashIdx - slashPageSize + 1
				}
				if m.slashListOffset < 0 {
					m.slashListOffset = 0
				}

				end := m.slashListOffset + slashPageSize
				if end > total {
					end = total
				}
				for i := m.slashListOffset; i < end; i++ {
					c := m.slashFiltered[i]
					selected := i == m.slashIdx
					var line string
					if selected {
						line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
					} else {
						line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
					}
					addFull(pad + line)
				}
				if end < total {
					addFull(pad + dim.Render(fmt.Sprintf("  ... and %d more", total-end)))
				}
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
	} else if m.thinking && m.streaming {
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
	if m.confirmForm.active {
		awaitStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
		statusRight = awaitStyle.Render("⚠ confirming")
	} else if m.blocking {
		awaitStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
		statusRight = awaitStyle.Render("⚠ awaiting input")
	} else if m.thinking {
		statusRight = dim.Render(m.statusPhase)
	} else {
		statusRight = dim.Render("ready")
	}
	statusRight = statusRight + usageStr

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
	if m.confirmForm.active {
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
				all[cursorIdx] = all[cursorIdx] + cursor
			}

		case RoleThinking:
			lines := wrapText(msg.Content, contentWidth)
			borderPrefix := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555")).Render("│ ")
			thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
			for i, line := range lines {
				if i == 0 {
					if msg.ThinkingActive {
						all = append(all, borderPrefix+lipgloss.NewStyle().Foreground(colorAccent).Render(spinnerFrames[m.spinFrame]+" ")+thinkingStyle.Render(line))
					} else {
						all = append(all, borderPrefix+lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("● ")+thinkingStyle.Render(line))
					}
				} else {
					all = append(all, borderPrefix+"  "+thinkingStyle.Render(line))
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

			// Render tool output when tool succeeded
			if msg.ToolStatus == "success" && msg.Content != "" {
				resultContent := msg.Content
				// Strip the label if it matches the tool name + args
				fullLabel := pascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
				resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
				resultContent = strings.TrimSpace(resultContent)
				if resultContent != "" {
					// Render diff blocks with colored +/-
					if isDiffContent(resultContent) {
						all = append(all, "")
						rendered := renderDiffContent(resultContent, contentWidth-6)
						for _, dl := range strings.Split(rendered, "\n") {
							all = append(all, dim.Render("│ ")+dl)
						}
					} else {
						for _, rl := range strings.Split(resultContent, "\n") {
							all = append(all, dim.Render("│   ")+lipgloss.NewStyle().Foreground(colorMuted).Render(strings.TrimLeft(rl, " ")))
						}
					}
				}
			}

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

	if m.confirmForm.active {
		for _, cl := range strings.Split(m.confirmForm.View(m.spinFrame), "\n") {
			all = append(all, cl)
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

// isDiffContent detects whether text contains a unified diff.
func isDiffContent(text string) bool {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "```diff") {
		return true
	}
	hasMinus := false
	hasPlus := false
	hasHunk := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "--- ") {
			hasMinus = true
		}
		if strings.HasPrefix(line, "+++ ") {
			hasPlus = true
		}
		if strings.HasPrefix(line, "@@") {
			hasHunk = true
		}
	}
	return (hasMinus && hasPlus) || hasHunk
}

func renderDiffContent(text string, maxWidth int) string {
	// Strip markdown fences and collect lines.
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "```diff" || trimmed == "```" {
			continue
		}
		lines = append(lines, line)
	}

	// GitHub-dark inspired styles with block backgrounds.
	fileStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a8b7")).
		Background(lipgloss.Color("#1e2235")).
		Width(maxWidth)
	hunkStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#768390")).
		Background(lipgloss.Color("#0d1117")).
		Width(maxWidth)
	ctxStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8b949e")).
		Background(lipgloss.Color("#0d1117")).
		Width(maxWidth)
	addStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7ee787")).
		Background(lipgloss.Color("#1c2a20")).
		Width(maxWidth)
	delStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ff7b72")).
		Background(lipgloss.Color("#2a1d1e")).
		Width(maxWidth)

	var sb strings.Builder
	for _, line := range lines {
		display := truncateLine(line, maxWidth)
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			sb.WriteString(fileStyle.Render(display))
		case strings.HasPrefix(line, "@@"):
			sb.WriteString(hunkStyle.Render(display))
		case strings.HasPrefix(line, "+"):
			sb.WriteString(addStyle.Render(display))
		case strings.HasPrefix(line, "-"):
			sb.WriteString(delStyle.Render(display))
		default:
			sb.WriteString(ctxStyle.Render(display))
		}
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}
