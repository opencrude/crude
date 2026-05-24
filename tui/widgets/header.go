package widgets

import (
	"image/color"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
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
	spacer := innerW - rendering.VisibleWidth(left) - rendering.VisibleWidth(rightStr)
	if spacer < 0 {
		spacer = 0
	}
	return left + strings.Repeat(" ", spacer) + rightStr
}

func RenderSep(width int, clr color.Color) string {
	return lipgloss.NewStyle().Foreground(clr).Render(strings.Repeat("─", width))
}
