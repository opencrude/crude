package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"crude/fileio"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/widgets"
	"crude/tui/window"
)

func (m *App) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	pad := strings.Repeat(" ", m.theme.Spacing.PadLR)
	innerW := m.width - m.theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)

	isSpecialMode := m.Mode != ModeChat

	m.syncFormHint()

	var inputView string
	var inputLineCount int
	if isSpecialMode {
		inputLineCount = 1
		hint := m.formHint
		if hint == "" {
			hint = " "
		}
		inputView = dim.Render(hint)
	} else {
		if m.sessionPickerActive && m.sessionPickerFocused {
			hint := "[ ↑↓: browse sessions · enter: resume · click here to type · ctrl+c: switch focus ]"
			inputView = m.theme.Styles.DimLabel.Render(hint) + "\n" + m.input.View(m.width, m.height, false, false, m.spinFrame)
		} else if m.sessionPickerActive && !m.sessionPickerFocused {
			hint := "[ ctrl+c: browse sessions ]"
			inputView = m.theme.Styles.DimLabel.Render(hint) + "\n" + m.input.View(m.width, m.height, false, false, m.spinFrame)
		} else {
			inputView = m.input.View(m.width, m.height, false, false, m.spinFrame)
		}
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	asyncBarLines := m.statusbar.RenderAsyncBar()
	asyncLineCount := len(asyncBarLines)

	asyncInWindow := m.win.IsOpen() && m.win.Layout() == window.TabVertical && m.win.TabCount() > 1 && asyncLineCount > 0

	nonChat := 6 + inputLineCount
	if asyncLineCount > 0 && !asyncInWindow {
		nonChat += 2 + asyncLineCount
	}

	windowLines := m.renderWindowContent()
	windowLineCount := len(windowLines)
	if windowLineCount > 0 {
		nonChat += 1 + windowLineCount
	}

	chatH := m.height - nonChat
	if chatH < 1 {
		chatH = 1
	}
	m.chatAreaHeight = chatH

	ma := m.mainAgent()
	activeTyping := ma.ComputeActiveTyping()

	nestingLevel := m.paneMgr.ScrollbarNestingLevel()
	paneSBW := nestingLevel * scrollbar.ScrollbarWidth
	ma.MsgList.SetPaneScrollbarWidth(paneSBW)

	ma.MsgList.SetStreaming(ma.Streaming)
	ma.MsgList.SetSpinFrame(m.spinFrame)
	ma.MsgList.SetActiveTyping(activeTyping)
	ma.MsgList.SetPhase(m.statusbar.Phase())
	ma.MsgList.SetSize(m.width, m.height)
	ma.MsgList.SetChatAreaBounds(3, chatH)

	chatVisible := ma.MsgList.Render(innerW)

	mainSBVisible := ma.MsgList.ScrollbarWidth() > 0

	var totalSBW int
	var mainSBLines []string
	var paneLineMap []widgets.PaneLineInfo
	var paneSBViews map[string][]string

	totalSBW = scrollbar.ScrollbarWidth * (1 + nestingLevel)
	if !mainSBVisible && nestingLevel == 0 {
		totalSBW = 0
	}
	if !mainSBVisible && nestingLevel > 0 {
		totalSBW = scrollbar.ScrollbarWidth * nestingLevel
	}

	mainScrollbarView := ma.MsgList.ScrollbarView()
	mainSBLines = strings.Split(mainScrollbarView, "\n")

	paneLineMap = ma.MsgList.PaneLineMap()

	paneSBViews = make(map[string][]string)
	if nestingLevel > 0 {
		for _, info := range paneLineMap {
			if info.PaneID != "" {
				if _, ok := paneSBViews[info.PaneID]; !ok {
					sp := m.paneMgr.Get(info.PaneID)
					if sp != nil {
						view := sp.ScrollbarView()
						paneSBViews[info.PaneID] = strings.Split(view, "\n")
					}
				}
			}
		}
	}

	lines := make([]string, 0, m.height)

	lines = append(lines, "")

	headerLine := widgets.RenderHeader(m.theme, m.modelName, m.yolo, m.startTime, m.width)
	lines = append(lines, pad+headerLine+pad)
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	for i, line := range chatVisible {
		maxContentW := m.width - 2*m.theme.Spacing.PadLR - totalSBW
		// Trim trailing padding before the overflow check. Subpane header/summary
		// lines are right-padded to innerW (which doesn't account for scrollbar
		// width), so the raw line can exceed maxContentW even though the actual
		// content fits. Without trimming, TruncateLine would append a false "…"
		// after the trailing spaces (e.g. "│ ● Thinking 8s      …").
		trimmed := rendering.TrimRightAnsiSpaces(line)
		visW := lipgloss.Width(trimmed)
		if visW > maxContentW {
			trimmed = rendering.TruncateLine(trimmed, maxContentW)
			visW = lipgloss.Width(trimmed)
		}
		line = trimmed

		if totalSBW > 0 {
			var sbColumn string

			isPaneLine := i < len(paneLineMap) && paneLineMap[i].PaneID != ""

			if nestingLevel > 0 && isPaneLine {
				info := paneLineMap[i]
				paneSBLine := ""
				if paneSBLines, ok := paneSBViews[info.PaneID]; ok {
					if info.LineIdx < len(paneSBLines) && paneSBLines[info.LineIdx] != "" {
						paneSBLine = paneSBLines[info.LineIdx]
					}
				}
				if paneSBLine == "" {
					paneSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
				}

				mainSBLine := ""
				if mainSBVisible && i < len(mainSBLines) {
					mainSBLine = mainSBLines[i]
				}
				if mainSBLine == "" {
					mainSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
				}

				sbColumn = paneSBLine + mainSBLine
			} else if nestingLevel > 0 {
				paneGap := strings.Repeat(" ", scrollbar.ScrollbarWidth)

				mainSBLine := ""
				if mainSBVisible && i < len(mainSBLines) {
					mainSBLine = mainSBLines[i]
				}
				if mainSBLine == "" {
					mainSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
				}

				sbColumn = paneGap + mainSBLine
			} else {
				if mainSBVisible && i < len(mainSBLines) && mainSBLines[i] != "" {
					sbColumn = mainSBLines[i]
				} else {
					sbColumn = ""
				}
			}

			if sbColumn != "" {
				padRight := m.width - m.theme.Spacing.PadLR - totalSBW - visW
				if padRight < 0 {
					padRight = 0
				}
				line = pad + line + strings.Repeat(" ", padRight) + sbColumn + pad
			} else {
				rightPad := m.width - 2*m.theme.Spacing.PadLR - visW
				if rightPad < 0 {
					rightPad = 0
				}
				line = pad + line + strings.Repeat(" ", rightPad) + pad
			}
		} else {
			rightPad := m.width - 2*m.theme.Spacing.PadLR - visW
			if rightPad < 0 {
				rightPad = 0
			}
			line = pad + line + strings.Repeat(" ", rightPad) + pad
		}
		lines = append(lines, line)
	}

	if windowLineCount > 0 {
		lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
		for _, wl := range windowLines {
			visW := lipgloss.Width(wl)
			rightPad := m.width - 2*m.theme.Spacing.PadLR - visW
			if rightPad < 0 {
				rightPad = 0
			}
			lines = append(lines, pad+wl+strings.Repeat(" ", rightPad)+pad)
		}
	}

	if asyncLineCount > 0 && !asyncInWindow {
		lines = append(lines, "")
		lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
		for _, al := range asyncBarLines {
			lines = append(lines, pad+al)
		}
	}

	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	if isSpecialMode {
		lines = append(lines, pad+inputView)
	} else {
		for _, il := range strings.Split(inputView, "\n") {
			lines = append(lines, pad+il)
		}
	}

	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	statusView := m.statusbar.View()
	lines = append(lines, pad+statusView)

	if len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
}

func (m *App) renderWindowContent() []string {
	if !m.win.IsOpen() {
		return nil
	}

	m.win.SetWidth(m.width - 2*m.theme.Spacing.PadLR)

	if m.win.Layout() == window.TabVertical {
		return m.renderWindowVertical()
	}
	return m.renderWindowHorizontal()
}

func (m *App) renderWindowHorizontal() []string {
	var lines []string

	tabBarLine := m.win.RenderTabBar()
	lines = append(lines, tabBarLine)

	sepLine := m.win.RenderSeparator()
	lines = append(lines, sepLine)

	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		return lines
	}

	contentLines := m.getWindowContentLines(activeTab.Title)

	for _, cl := range contentLines {
		lines = append(lines, cl)
	}

	return lines
}

func (m *App) renderWindowVertical() []string {
	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		return nil
	}

	rawContentLines := m.getWindowContentLines(activeTab.Title)

	verticalLines := m.win.RenderVerticalSidebar(rawContentLines)
	if verticalLines != nil {
		asyncItems := m.getAsyncItemsForVertical()
		if len(asyncItems) > 0 {
			verticalLines = append(verticalLines, asyncItems...)
		}
		return verticalLines
	}

	return rawContentLines
}

func (m *App) getAsyncItemsForVertical() []string {
	asyncBarLines := m.statusbar.RenderAsyncBar()
	if len(asyncBarLines) == 0 {
		return nil
	}

	visibleCmds := m.statusbar.VisibleAsyncCommands()

	var lines []string
	for i, c := range visibleCmds {
		if i >= len(asyncBarLines) {
			break
		}
		row := m.win.RenderVerticalAsyncRow(window.AsyncItem{
			Handle:   c.Handle,
			Command:  c.Command,
			Duration: c.Duration.Truncate(time.Second).String(),
			Status: func() string {
				switch c.Status {
				case fileio.AsyncRunning:
					return "running"
				case fileio.AsyncCompleted:
					return "completed"
				case fileio.AsyncFailed:
					return "failed"
				case fileio.AsyncKilled:
					return "killed"
				default:
					return "unknown"
				}
			}(),
		}, m.spinFrame)

		lines = append(lines, row)
	}
	return lines
}

func (m *App) getWindowContentLines(tabTitle string) []string {
	var rawLines []string

	contentW := m.width
	if m.win.Layout() == window.TabVertical {
		contentW = m.win.VerticalContentWidth()
	}

	switch tabTitle {
	case "Settings":
		settingsView := m.settings.View(m.spinFrame)
		for _, sl := range strings.Split(settingsView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(sl, contentW))
		}
	case "Confirm":
		m.confirm.SetWidth(contentW)
		confirmView := m.confirm.View(m.spinFrame)
		for _, cl := range strings.Split(confirmView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(cl, contentW))
		}
	case "YOLO":
		m.yoloDlg.SetWidth(contentW)
		yoloView := m.yoloDlg.View()
		for _, yl := range strings.Split(yoloView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(yl, contentW))
		}
	}

	stripLeadingTrailingEmpty := func(ss []string) []string {
		start := 0
		for start < len(ss) && ss[start] == "" {
			start++
		}
		end := len(ss)
		for end > start && ss[end-1] == "" {
			end--
		}
		return ss[start:end]
	}
	return stripLeadingTrailingEmpty(rawLines)
}

func (m *App) syncFormHint() {
	if !m.win.IsOpen() {
		m.formHint = ""
		return
	}
	activeTab := m.win.ActiveTab()
	if activeTab == nil {
		m.formHint = ""
		return
	}
	switch activeTab.Title {
	case "Settings":
		m.formHint = m.settings.Hint()
	case "Confirm":
		m.formHint = "[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]"
	case "YOLO":
		m.formHint = "[ yolo mode confirmation · ← →: choose · enter: select · y/n: quick select · esc: cancel ]"
	default:
		m.formHint = ""
	}
}
