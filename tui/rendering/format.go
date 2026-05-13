package rendering

import (
	"fmt"
	"strings"
	"time"
)

func PascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func FormatTokens(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	secs := d.Seconds()
	if secs < 1.0 {
		return fmt.Sprintf("%.1fs", secs)
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", int(secs))
	}
	mins := int(secs) / 60
	remainingSecs := int(secs) % 60
	return fmt.Sprintf("%dmin %dseconds", mins, remainingSecs)
}

func ToolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

func FormatTimeLabel(t time.Time) (string, string) {
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

func FormatCost(cost float64) string {
	switch {
	case cost >= 1.0:
		return fmt.Sprintf("$%.2f", cost)
	case cost >= 0.01:
		return fmt.Sprintf("$%.4f", cost)
	case cost >= 0.0001:
		return fmt.Sprintf("$%.5f", cost)
	case cost > 0:
		return fmt.Sprintf("$%.6f", cost)
	default:
		return ""
	}
}
