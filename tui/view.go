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
		if m.showHint && !m.thinking && m.inputLen() == 0 {
			addFull(pad + dim.Render("[ enter: send · alt+enter: new line · ↑↓: history · ctrl+s: settings · esc: exit ]"))
		}
		var inputContent string
		if m.thinking && !m.blocking {
			inputContent = pad + dim.Italic(true).Render("▶ waiting for response...")
		} else if m.inputSelecting {
			lines := m.inputLines()
			textW := m.inputTextWidth()
			selStart, selEnd := m.inputSelectionRange()
			var inputLines []string

			bufPos := 0
			for li, logLine := range lines {
				logStr := string(logLine)
				segments := hardWrap(logStr, textW)

				for _, seg := range segments {
					prefix := "❯ "
					if li > 0 || bufPos > 0 {
						prefix = "  "
					}
					segRunes := []rune(seg)
					segStart := bufPos
					segEnd := bufPos + len(segRunes)

					rendered := pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+seg)
					if selStart < segEnd && selEnd > segStart {
						localStart := selStart - segStart
						if localStart < 0 {
							localStart = 0
						}
						localEnd := selEnd - segStart
						if localEnd > len(segRunes) {
							localEnd = len(segRunes)
						}
						if localEnd > localStart {
							startCol := padLR + 2
							for j := 0; j < localStart; j++ {
								startCol += runewidth.RuneWidth(segRunes[j])
							}
							endCol := startCol
							for j := localStart; j < localEnd; j++ {
								endCol += runewidth.RuneWidth(segRunes[j])
							}
							rendered = applySelectionReverse(rendered, startCol, endCol)
						}
					}
					inputLines = append(inputLines, rendered)
				}

				bufPos += len(logLine)
				if li < len(lines)-1 {
					bufPos++ // account for \n between logical lines
				}
			}

			inputContent = strings.Join(inputLines, "\n")
		} else {
			lines := m.inputLines()
			curLine, curCol := m.cursorLineCol()
			textW := m.inputTextWidth()

			cursorLineWidth := 0
			if curLine < len(lines) {
				cursorLineWidth = runewidth.StringWidth(string(lines[curLine][:curCol]))
			}

			var inputLines []string

			for li, logLine := range lines {
				logStr := string(logLine)
				segments := hardWrap(logStr, textW)

				cursorSeg := -1
				cursorSegOffset := 0
				if li == curLine {
					accumW := 0
					for si, seg := range segments {
						segW := runewidth.StringWidth(seg)
						if cursorLineWidth <= accumW+segW {
							cursorSeg = si
							cursorSegOffset = cursorLineWidth - accumW
							if cursorSegOffset < 0 {
								cursorSegOffset = 0
							}
							break
						}
						accumW += segW
					}
					if cursorSeg == -1 && len(segments) > 0 {
						cursorSeg = len(segments) - 1
						cursorSegOffset = runewidth.StringWidth(segments[len(segments)-1])
					}
				}

				for si, seg := range segments {
					prefix := "❯ "
					if li > 0 || si > 0 {
						prefix = "  "
					}

					var line string
					if li == curLine && si == cursorSeg {
						leftW := 0
						splitIdx := 0
						segRunes := []rune(seg)
						for splitIdx < len(segRunes) && leftW < cursorSegOffset {
							leftW += runewidth.RuneWidth(segRunes[splitIdx])
							if leftW > cursorSegOffset {
								break
							}
							splitIdx++
						}
						leftSeg := string(segRunes[:splitIdx])
						rightSeg := string(segRunes[splitIdx:])

						var cursorAndRight string
						if splitIdx < len(segRunes) {
							underCursor := string(segRunes[splitIdx : splitIdx+1])
							rest := string(segRunes[splitIdx+1:])
							cursorAndRight = lipgloss.NewStyle().Reverse(true).Render(underCursor) + lipgloss.NewStyle().Foreground(colorMainText).Render(rest)
						} else {
							cursor := "█"
							if time.Since(m.lastInputTime) > 800*time.Millisecond {
								cursor = blockCursor(m.spinFrame, colorAccent)
							}
							cursorAndRight = lipgloss.NewStyle().Foreground(colorAccent).Render(cursor) + lipgloss.NewStyle().Foreground(colorMainText).Render(rightSeg)
						}
						line = pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+leftSeg) + cursorAndRight
					} else {
						line = pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+seg)
					}
					inputLines = append(inputLines, line)
				}
			}

			inputContent = strings.Join(inputLines, "\n")
		}
		for _, il := range strings.Split(inputContent, "\n") {
			addFull(il)
		}

		inputStr := m.inputText()
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
		newTokens := m.tokenPrompt - m.tokenCached
		if m.tokenCached > 0 {
			usageStr = dim.Render(fmt.Sprintf(" %s(%s+%s)in / %s out",
				formatTokens(newTokens+m.tokenCached),
				formatTokens(newTokens), formatTokens(m.tokenCached),
				formatTokens(m.tokenCompletion)))
		} else {
			usageStr = dim.Render(fmt.Sprintf(" %s in / %s out",
				formatTokens(m.tokenPrompt), formatTokens(m.tokenCompletion)))
		}
	}
	if m.sessionCost > 0 {
		usageStr += dim.Render(" " + formatCost(m.sessionCost))
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
	inputLineCount := m.visualLineCount()
	slashLines := 0
	inputStr := m.inputText()
	if m.slashActive && !m.thinking && !strings.Contains(inputStr, "\n") {
		slashLines = len(m.slashFiltered)
		if slashLines > 8 {
			slashLines = 9
		}
	}
	hintLines := 0
	if m.showHint && !m.thinking && m.inputLen() == 0 {
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
	// Invalidate all caches on terminal width change
	if m.width != m.cachedWidth {
		m.cachedWidth = m.width
		for i := range m.messages {
			m.messages[i].cachedLines = nil
		}
		m.mdRenderer = nil
	}

	var all []string
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	contentWidth := m.width - padLR*2 - 16
	if contentWidth < 10 {
		contentWidth = 10
	}
	innerW := m.width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	for msgIdx := range m.messages {
		msg := &m.messages[msgIdx]
		isLastMsg := msgIdx == len(m.messages)-1

		all = append(all, "")

		// Use pre-rendered cache for unchanged messages
		if msg.cachedLines != nil {
			all = append(all, msg.cachedLines...)
			if msg.cursorLine >= 0 && msg.cursorLine < len(msg.cachedLines) &&
				msgIdx == len(m.messages)-1 && (m.streaming || msg.ThinkingActive) {
				cursor := blockCursor(m.spinFrame, colorAccent)
				all[len(all)-len(msg.cachedLines)+msg.cursorLine] += cursor
			}
			continue
		}

		timeLine1, timeLine2 := formatTimeLabel(msg.Time)
		timeW := max(len(timeLine1), len(timeLine2))

		var msgLines []string
		cursorLine := -1 // line in msgLines to append blinking cursor (applied after cache)

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

			msgLines = append(msgLines, userLines...)
			if len(userLines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				msgLines = append(msgLines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

		case RoleAssistant:
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

			labelStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Background(lipgloss.Color("#2a2a3a"))
			if len(asstLines) > 0 {
				label := labelStyle.Render("❯ Crude " + m.modelName + " ")
				asstLines[0] = label + asstLines[0]
			}

			for i, line := range asstLines {
				isCursorLine := m.streaming && isLastMsg && i == len(asstLines)-1
				var timePart string
				if i == 0 && !isCursorLine {
					timePart = dim.Render(timeLine1)
				} else if i == 1 && !isCursorLine {
					timePart = dim.Render(timeLine2)
				}
				if timePart != "" {
					lineW := lipgloss.Width(line)
					sp := innerW - lineW - timeW
					if sp < 1 {
						sp = 1
					}
					asstLines[i] = line + strings.Repeat(" ", sp) + timePart
				} else if isCursorLine {
					asstLines[i] = strings.TrimRight(line, " ")
				}
			}

			msgLines = append(msgLines, asstLines...)
			if len(asstLines) == 1 && !(m.streaming && isLastMsg) {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				msgLines = append(msgLines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

			if m.streaming && isLastMsg && len(asstLines) > 0 {
				cursorLine = len(msgLines) - 1
			}

		case RoleThinking:
			lines := wrapText(msg.Content, contentWidth)
			borderPrefix := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555")).Render("│ ")
			thinkingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
			for i, line := range lines {
				if i == 0 {
					if msg.ThinkingActive {
						msgLines = append(msgLines, borderPrefix+lipgloss.NewStyle().Foreground(colorAccent).Render(spinnerFrames[m.spinFrame]+" ")+thinkingStyle.Render(line))
					} else {
						msgLines = append(msgLines, borderPrefix+lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("● ")+thinkingStyle.Render(line))
					}
				} else {
					msgLines = append(msgLines, borderPrefix+"  "+thinkingStyle.Render(line))
				}
			}
			if msg.ThinkingActive && isLastMsg && len(msgLines) > 0 {
				cursorLine = len(msgLines) - 1
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

			msgLines = append(msgLines, line)

			if msg.ToolStatus == "success" && msg.Content != "" {
				resultContent := msg.Content
				fullLabel := pascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
				resultContent = strings.TrimPrefix(strings.TrimSpace(resultContent), fullLabel)
				resultContent = strings.TrimSpace(resultContent)
				if resultContent != "" {
					if isPlanTool(msg.ToolName) {
						msgLines = append(msgLines, "")
						planBox := renderPlanBox(resultContent, contentWidth-4)
						for _, pl := range strings.Split(planBox, "\n") {
							msgLines = append(msgLines, dim.Render("│ ")+pl)
						}
					} else if isDiffContent(resultContent) {
						msgLines = append(msgLines, "")
						rendered := renderDiffContent(resultContent, contentWidth-6)
						for _, dl := range strings.Split(rendered, "\n") {
							msgLines = append(msgLines, dim.Render("│ ")+dl)
						}
					} else if msg.ToolName == "read_file" && strings.Contains(resultContent, "\n") {
						// Show only the summary line for file reads, not the full content
						firstLine, _, _ := strings.Cut(resultContent, "\n")
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(colorMuted).Render(firstLine))
						msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(colorMuted).Render("… full content returned to agent"))
					} else {
						for _, rl := range strings.Split(resultContent, "\n") {
							msgLines = append(msgLines, dim.Render("│   ")+lipgloss.NewStyle().Foreground(colorMuted).Render(strings.TrimLeft(rl, " ")))
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
					msgLines = append(msgLines, styled+strings.Repeat(" ", sp)+dim.Render(timeLine1))
				} else if i == 1 {
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					msgLines = append(msgLines, styled+strings.Repeat(" ", sp)+dim.Render(timeLine2))
				} else {
					msgLines = append(msgLines, styled)
				}
			}
			if len(lines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				msgLines = append(msgLines, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}
		}

		msg.cachedLines = msgLines
		msg.cursorLine = cursorLine
		prevLen := len(all)
		all = append(all, msgLines...)
		if cursorLine >= 0 && cursorLine < len(msgLines) {
			cursor := blockCursor(m.spinFrame, colorAccent)
			all[prevLen+cursorLine] = all[prevLen+cursorLine] + cursor
		}
	}

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

// inputTextWidth returns the available display width for input text.
func (m *Model) inputTextWidth() int {
	w := m.width - padLR*2 - 2
	if w < 1 {
		w = 1
	}
	return w
}

// hardWrap breaks a string into segments each fitting within maxWidth display cells.
func hardWrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	var result []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if curW+rw > maxWidth && curW > 0 {
			result = append(result, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 || len(result) == 0 {
		result = append(result, cur.String())
	}
	return result
}

// visualLineCount returns the number of visual display lines the input buffer occupies.
func (m *Model) visualLineCount() int {
	maxW := m.inputTextWidth()
	count := 0
	for _, l := range m.inputLines() {
		segments := hardWrap(string(l), maxW)
		count += len(segments)
	}
	if count < 1 {
		count = 1
	}
	return count
}

type visualCursorInfo struct {
	logicalLine  int
	logicalCol   int
	visualLine   int
	visualCol    int
	segmentIdx   int
	segmentStart int
}

// cursorVisualInfo returns the visual position of the cursor.
func (m *Model) cursorVisualInfo() visualCursorInfo {
	lines := m.inputLines()
	curLine, curCol := m.cursorLineCol()
	textW := m.inputTextWidth()

	info := visualCursorInfo{
		logicalLine: curLine,
		logicalCol:  curCol,
	}

	visLine := 0
	for li := 0; li < curLine; li++ {
		segments := hardWrap(string(lines[li]), textW)
		visLine += len(segments)
	}

	segments := hardWrap(string(lines[curLine]), textW)
	runeOffset := 0
	segIdx := 0
	for segIdx < len(segments) {
		segRunes := []rune(segments[segIdx])
		if runeOffset+len(segRunes) > curCol {
			break
		}
		runeOffset += len(segRunes)
		segIdx++
	}
	if segIdx >= len(segments) {
		segIdx = len(segments) - 1
		if segIdx >= 0 {
			runeOffset = curCol - len([]rune(segments[segIdx]))
			if runeOffset < 0 {
				runeOffset = 0
			}
		}
	}

	info.visualLine = visLine + segIdx
	info.segmentIdx = segIdx
	info.segmentStart = runeOffset

	info.visualCol = padLR + 2
	for i := runeOffset; i < curCol && i < len(lines[curLine]); i++ {
		info.visualCol += runewidth.RuneWidth(lines[curLine][i])
	}

	return info
}

// cursorPosFromVisual returns the inputBuf index closest to the given visual position.
func (m *Model) cursorPosFromVisual(targetVisLine, targetVisCol int) int {
	lines := m.inputLines()
	textW := m.inputTextWidth()

	visCount := 0
	for li, logLine := range lines {
		segments := hardWrap(string(logLine), textW)
		for si, seg := range segments {
			if visCount == targetVisLine {
				segRunes := []rune(seg)
				targetTextCol := targetVisCol - padLR - 2
				if targetTextCol < 0 {
					targetTextCol = 0
				}

				segRuneIdx := 0
				colW := 0
				for segRuneIdx < len(segRunes) && colW < targetTextCol {
					colW += runewidth.RuneWidth(segRunes[segRuneIdx])
					if colW > targetTextCol {
						break
					}
					segRuneIdx++
				}

				pos := 0
				for i := 0; i < li; i++ {
					pos += len(lines[i]) + 1
				}
				for j := 0; j < si; j++ {
					pos += len([]rune(segments[j]))
				}
				pos += segRuneIdx
				return pos
			}
			visCount++
		}
	}
	return m.inputLen()
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

// isPlanTool returns true for state machine management tools that return plan status.
func isPlanTool(name string) bool {
	switch name {
	case "plan", "mark_step_complete", "mark_step_failed", "report_blocker", "resolve_blocker", "list_goal_status":
		return true
	default:
		return false
	}
}

// renderPlanBox renders the plan status text in a styled rounded box with
// color-coded step markers: ✓ (green), → (blue), ☐ (muted), ✗ (red), ! (amber).
func renderPlanBox(text string, maxWidth int) string {
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
			styled = lipgloss.NewStyle().Foreground(colorSuccess).Render(line)
		case strings.HasPrefix(trimmed, "→"):
			styled = lipgloss.NewStyle().Foreground(colorAccent2).Render(line)
		case strings.HasPrefix(trimmed, "✗"):
			styled = lipgloss.NewStyle().Foreground(colorError).Render(line)
		case strings.HasPrefix(trimmed, "!"):
			styled = lipgloss.NewStyle().Foreground(colorWarning).Render(line)
		case strings.HasPrefix(trimmed, "☐"):
			styled = lipgloss.NewStyle().Foreground(colorMuted).Render(line)
		case strings.HasPrefix(trimmed, "Plan:") || strings.HasPrefix(trimmed, "Goal:"):
			styled = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(line)
		case strings.HasPrefix(trimmed, "Suspended:"):
			styled = lipgloss.NewStyle().Foreground(colorWarning).Render(line)
		default:
			styled = lipgloss.NewStyle().Foreground(colorMuted).Render(line)
		}
		styledLines = append(styledLines, styled)
	}

	content := strings.Join(styledLines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(0, 1).
		Width(maxWidth)

	return box.Render(content)
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
