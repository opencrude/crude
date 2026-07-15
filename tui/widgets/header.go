package widgets

import (
	"image/color"
	"strings"
	"time"

	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
)

func RenderHeader(th *theme.Theme, modelName string, yolo bool, startTime time.Time, usage, viewLabel string, width int) string {
	dim := lipgloss.NewStyle().Foreground(th.Colors.Muted)
	uptime := time.Since(startTime).Truncate(time.Second)
	innerW := width - th.Spacing.PadLR*2
	if innerW < 1 {
		innerW = 1
	}
	left := th.Styles.Header.Render("crude") +
		"  " + dim.Render(rendering.TruncateLine(modelName, (width-4)/3))
	if viewLabel != "" {
		left += "  " + lipgloss.NewStyle().Foreground(th.Colors.Accent).Bold(true).Render("⟨"+viewLabel+"⟩")
	}

	var rightParts []string
	if yolo {
		rightParts = append(rightParts, th.Styles.DangerText.Render("⚠ YOLO"))
	}
	if usage != "" {
		rightParts = append(rightParts, usage)
	}
	rightParts = append(rightParts, dim.Render(uptime.String()))
	sep := dim.Render("  ·  ")
	rightStr := strings.Join(rightParts, sep)
	overflow := rendering.VisibleWidth(left) + rendering.VisibleWidth(rightStr) - innerW
	if overflow > 0 && usage != "" {
		usage = rendering.TruncateLine(usage, max(0, rendering.VisibleWidth(usage)-overflow))
		rightParts = rightParts[:0]
		if yolo {
			rightParts = append(rightParts, th.Styles.DangerText.Render("⚠ YOLO"))
		}
		if usage != "" {
			rightParts = append(rightParts, usage)
		}
		rightParts = append(rightParts, dim.Render(uptime.String()))
		rightStr = strings.Join(rightParts, sep)
	}
	leftBudget := innerW - rendering.VisibleWidth(rightStr)
	if leftBudget < rendering.VisibleWidth(left) {
		left = rendering.TruncateLine(left, max(0, leftBudget))
	}
	spacer := innerW - rendering.VisibleWidth(left) - rendering.VisibleWidth(rightStr)
	if spacer < 0 {
		spacer = 0
	}
	return left + strings.Repeat(" ", spacer) + rightStr
}

func RenderSep(width int, clr color.Color) string {
	return lipgloss.NewStyle().Foreground(clr).Render(strings.Repeat("─", width))
}
