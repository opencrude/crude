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
	return FormatCount(n)
}

func FormatCount(n int) string {
	switch {
	case n >= 1000000:
		v := float64(n) / 1000000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dM", int(v))
		}
		return fmt.Sprintf("%.1fM", v)
	case n >= 1000:
		v := float64(n) / 1000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dK", int(v))
		}
		return fmt.Sprintf("%.1fK", v)
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

// FormatThinkingDuration renders a compact human duration for thinking
// blocks: "42s", "2m 10s", "1h 03m 05s".
func FormatThinkingDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		return "<1s"
	}
	secs := int(d.Seconds())
	hours := secs / 3600
	mins := (secs % 3600) / 60
	rem := secs % 60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %02dm %02ds", hours, mins, rem)
	case mins > 0:
		return fmt.Sprintf("%dm %02ds", mins, rem)
	default:
		return fmt.Sprintf("%ds", rem)
	}
}

func ToolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

func FormatTimeLabel(t time.Time) string {
	timeStr := t.Format("15:04")
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if t.After(todayStart) {
		return "today at " + timeStr
	}
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	if t.After(yesterdayStart) {
		return "yesterday at " + timeStr
	}
	return t.Format("01/02") + " " + timeStr
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
