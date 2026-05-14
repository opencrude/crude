package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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

	activeTyping := (m.streaming || m.thinking) && !m.lastTokenTime.IsZero() && time.Since(m.lastTokenTime) < 400*time.Millisecond
	m.msgList.SetStreaming(m.streaming)
	m.msgList.SetSpinFrame(m.spinFrame)
	m.msgList.SetActiveTyping(activeTyping)
	m.msgList.SetPhase(m.statusbar.Phase())
	m.msgList.SetSize(m.width, m.height)
	m.msgList.SetChatAreaBounds(3, chatH)

	chatVisible := m.msgList.Render(innerW)

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

	scrollbarView := m.msgList.ScrollbarView()
	scrollbarW := m.msgList.ScrollbarWidth()
	scrollbarLines := strings.Split(scrollbarView, "\n")

	lines := make([]string, 0, m.height)

	lines = append(lines, "")

	headerLine := widgets.RenderHeader(m.theme, m.modelName, m.yolo, m.startTime, m.width)
	lines = append(lines, pad+headerLine+pad)
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	for i, line := range chatVisible {
		if scrollbarW > 0 && i < len(scrollbarLines) && scrollbarLines[i] != "" {
			visW := lipgloss.Width(line)
			padRight := m.width - m.theme.Spacing.PadLR - scrollbarW - visW
			if padRight < 0 {
				padRight = 0
			}
			line = pad + line + strings.Repeat(" ", padRight) + scrollbarLines[i] + pad
		} else {
			rightPad := m.width - m.theme.Spacing.PadLR - lipgloss.Width(line)
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
