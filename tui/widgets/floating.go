package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func RenderFloatingBox(th *theme.Theme, title string, body []string, hint string, width, height int) []string {
	if width < 20 || height < 6 {
		return nil
	}
	boxW := width * 72 / 100
	if boxW > 100 {
		boxW = 100
	}
	if boxW < 32 {
		boxW = 32
	}
	boxH := height * 68 / 100
	if boxH < 8 {
		boxH = 8
	}
	contentH := boxH - 4
	if contentH < 1 {
		contentH = 1
	}

	innerW := boxW - 4
	if innerW < 1 {
		innerW = 1
	}

	if len(body) > contentH {
		body = body[:contentH]
	}
	for len(body) < contentH {
		body = append(body, "")
	}

	border := lipgloss.NewStyle().Foreground(th.Colors.Border)
	titleStyle := lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	bodyStyle := lipgloss.NewStyle().Background(th.Colors.Code.Bg).Foreground(th.Colors.Fg)

	top := border.Render("╭" + strings.Repeat("─", boxW-2) + "╮")
	bottom := border.Render("╰" + strings.Repeat("─", boxW-2) + "╯")

	var box []string
	titleText := " " + title + " "
	if rendering.VisibleWidth(titleText) > innerW {
		titleText = rendering.TruncateLine(titleText, innerW)
	}
	box = append(box, top)
	box = append(box, border.Render("│ ")+titleStyle.Render(padRight(titleText, innerW))+border.Render(" │"))
	for _, line := range body {
		line = rendering.TruncateLine(line, innerW)
		box = append(box, border.Render("│ ")+bodyStyle.Render(padRight(line, innerW))+border.Render(" │"))
	}
	if hint == "" {
		hint = "esc cancel · ctrl+s save"
	}
	hint = " " + hint + " "
	box = append(box, border.Render("│ ")+dim.Render(padRight(rendering.TruncateLine(hint, innerW), innerW))+border.Render(" │"))
	box = append(box, bottom)

	topPad := (height - len(box)) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (width - boxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	prefix := strings.Repeat(" ", leftPad)
	lines := make([]string, 0, topPad+len(box))
	for i := 0; i < topPad; i++ {
		lines = append(lines, "")
	}
	for _, line := range box {
		lines = append(lines, prefix+line)
	}
	return lines
}

func padRight(s string, width int) string {
	pad := width - rendering.VisibleWidth(s)
	if pad < 0 {
		pad = 0
	}
	return s + strings.Repeat(" ", pad)
}
