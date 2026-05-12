package widgets

import (
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

func RenderHeader(th *theme.Theme, modelName string, yolo bool, startTime time.Time, innerW int) string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	uptime := time.Since(startTime).Truncate(time.Second)
	left := lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render("crude") +
		" " + dim.Render(rendering.TruncateLine(modelName, innerW/3))

	var middle string
	if yolo {
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
	return left + strings.Repeat(" ", spacer) + rightStr
}

func RenderSep(width int, color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("─", width))
}
