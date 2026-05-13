package widgets

import (
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

func RenderHeader(th *theme.Theme, modelName string, yolo bool, startTime time.Time, width int) string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	uptime := time.Since(startTime).Truncate(time.Second)
	left := th.Styles.Header.Render("crude") +
		"  " + dim.Render(rendering.TruncateLine(modelName, (width-4)/3))

	var middle string
	if yolo {
		middle = " " + th.Styles.DangerText.Render("⚠ YOLO")
	}

	var rightStr string
	if middle != "" {
		rightStr = middle + " " + dim.Render(uptime.String())
	} else {
		rightStr = dim.Render(uptime.String())
	}
	innerW := width - th.Spacing.PadLR*2
	spacer := innerW - lipgloss.Width(left) - lipgloss.Width(rightStr)
	if spacer < 0 {
		spacer = 0
	}
	return left + strings.Repeat(" ", spacer) + rightStr
}

func RenderSep(width int, color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("─", width))
}
