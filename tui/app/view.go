package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"crude/tui/scrollbar"
	"crude/tui/widgets"
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
		inputView = m.input.View(m.width, m.height, false, false, m.spinFrame)
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	asyncBarLines := m.statusbar.RenderAsyncBar()
	asyncLineCount := len(asyncBarLines)

	nonChat := 6 + inputLineCount
	if asyncLineCount > 0 {
		nonChat += 2 + asyncLineCount
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

	var overlayLines []string
	if m.Mode == ModeSettings {
		settingsView := m.settings.View(m.spinFrame)
		for _, sl := range strings.Split(settingsView, "\n") {
			overlayLines = append(overlayLines, sl)
		}
	} else if m.Mode == ModeConfirm {
		m.confirm.SetWidth(m.width)
		confirmView := m.confirm.View(m.spinFrame)
		for _, cl := range strings.Split(confirmView, "\n") {
			overlayLines = append(overlayLines, cl)
		}
	} else if m.Mode == ModeYolo {
		m.yoloDlg.SetWidth(m.width)
		yoloView := m.yoloDlg.View()
		for _, yl := range strings.Split(yoloView, "\n") {
			overlayLines = append(overlayLines, yl)
		}
	}

	chatVisible = m.appendOverlay(chatVisible, overlayLines, chatH)

	mainSBVisible := ma.MsgList.ScrollbarWidth() > 0

	totalSBW := scrollbar.ScrollbarWidth * (1 + nestingLevel)
	if !mainSBVisible && nestingLevel == 0 {
		totalSBW = 0
	}
	if !mainSBVisible && nestingLevel > 0 {
		totalSBW = scrollbar.ScrollbarWidth * nestingLevel
	}

	mainScrollbarView := ma.MsgList.ScrollbarView()
	mainSBLines := strings.Split(mainScrollbarView, "\n")

	paneLineMap := ma.MsgList.PaneLineMap()

	paneSBViews := make(map[string][]string)
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
		visW := lipgloss.Width(line)

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

	if asyncLineCount > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Render(strings.Repeat("─", m.width)))
	}
	for _, al := range asyncBarLines {
		lines = append(lines, pad+al)
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

func (m *App) appendOverlay(chatVisible []string, overlayLines []string, chatH int) []string {
	if len(overlayLines) == 0 {
		return chatVisible
	}

	result := make([]string, 0, chatH)
	overlayStart := chatH - len(overlayLines)
	if overlayStart < 0 {
		overlayStart = 0
	}

	for i := 0; i < chatH; i++ {
		if i >= overlayStart && (i-overlayStart) < len(overlayLines) {
			result = append(result, overlayLines[i-overlayStart])
		} else if i < len(chatVisible) {
			result = append(result, chatVisible[i])
		} else {
			result = append(result, "")
		}
	}

	return result
}

func (m *App) syncFormHint() {
	switch m.Mode {
	case ModeSettings:
		m.formHint = m.settings.Hint()
	case ModeConfirm:
		m.formHint = "[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]"
	case ModeYolo:
		m.formHint = "[ yolo mode confirmation · ← →: choose · enter: select · y/n: quick select · esc: cancel ]"
	default:
		m.formHint = ""
	}
}
