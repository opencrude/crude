package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"crude/tui/rendering"
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

	isSpecialMode := m.mode == modeSettings || m.mode == modeConfirm || m.mode == modeYolo

	var inputView string
	var inputLineCount int
	if isSpecialMode {
		inputLineCount = 1
		switch m.mode {
		case modeSettings:
			inputView = "  " + dim.Render("[ interactive settings active · arrows/tab: navigate · enter: edit · ctrl+s: save · esc: return ]")
		case modeConfirm:
			inputView = "  " + dim.Render("[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]")
		case modeYolo:
			inputView = "  " + dim.Render("[ yolo mode confirmation · ← →: choose · enter: select · y/n: quick select · esc: cancel ]")
		}
	} else {
		inputView = m.input.View(m.width, m.height, false, false, m.spinFrame)
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	asyncBarLines := m.statusbar.RenderAsyncBar()
	asyncLineCount := len(asyncBarLines)

	nonChat := 7 + inputLineCount
	if asyncLineCount > 0 {
		nonChat += 2 + asyncLineCount
	}

	chatH := m.height - nonChat
	if chatH < 1 {
		chatH = 1
	}

	chatLines := m.renderChatArea()
	total := len(chatLines)

	m.chat.ClampOffset(total - chatH)

	endIdx := total - m.chat.ChatOffset()
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	visible := chatLines[startIdx:endIdx]

	lines := make([]string, 0, m.height)

	lines = append(lines, "")

	uptime := time.Since(m.startTime).Truncate(time.Second)
	left := m.theme.Styles.Header.Render("crude") +
		" " + dim.Render(rendering.TruncateLine(m.modelName, innerW/3))

	var middle string
	if m.yolo {
		yoloStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Bold(true)
		middle = " " + yoloStyle.Render("⚠ YOLO")
	}

	var rightStr string
	if middle != "" {
		rightStr = middle + " " + dim.Render(uptime.String())
	} else {
		rightStr = dim.Render(uptime.String())
	}
	spacer := innerW - lipgloss.Width(left) - lipgloss.Width(rightStr)
	if spacer < 0 {
		spacer = 0
	}
	lines = append(lines, pad+left+strings.Repeat(" ", spacer)+rightStr)
	lines = append(lines, m.theme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	emptyTop := chatH - len(visible)
	for i := 0; i < emptyTop; i++ {
		lines = append(lines, "")
	}
	for _, line := range visible {
		lines = append(lines, pad+line)
	}

	if asyncLineCount > 0 {
		lines = append(lines, "")
		lines = append(lines, dim.Render(strings.Repeat("─", m.width)))
	}
	for _, al := range asyncBarLines {
		lines = append(lines, al)
	}

	lines = append(lines, m.theme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	if isSpecialMode {
		lines = append(lines, inputView)
	} else {
		for _, il := range strings.Split(inputView, "\n") {
			lines = append(lines, il)
		}
	}

	lines = append(lines, m.theme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	statusView := m.statusbar.View()
	lines = append(lines, statusView)

	lines = append(lines, "")

	if len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
}

func (m *App) renderChatArea() []string {
	all := widgets.RenderAllMessages(m.chat.Messages(), m.theme, m.width, m.spinFrame, m.chat.MdRenderer(), m.modelName, m.streaming, m.height, m.username, m.statusbar.Phase())

	if m.mode == modeSettings {
		settingsView := m.settings.View(m.spinFrame)
		for _, sl := range strings.Split(settingsView, "\n") {
			all = append(all, sl)
		}
	} else if m.mode == modeConfirm {
		confirmView := m.confirm.View(m.spinFrame)
		for _, cl := range strings.Split(confirmView, "\n") {
			all = append(all, cl)
		}
	} else if m.mode == modeYolo {
		yoloView := m.yoloDlg.View()
		for _, yl := range strings.Split(yoloView, "\n") {
			all = append(all, yl)
		}
	}

	return all
}
