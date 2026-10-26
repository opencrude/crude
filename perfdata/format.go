package perfdata

import (
	"strconv"
	"time"
)

// formatMicros renders a duration in microseconds with two significant
// figures, the precision a person compares at a glance.
func FormatMicros(us int64) string {
	switch {
	case us <= 0:
		return "0"
	case us < 1_000:
		return strconv.FormatInt(us, 10) + "µs"
	case us < 10_000:
		return strconv.FormatFloat(float64(us)/1_000, 'f', 1, 64) + "ms"
	case us < 1_000_000:
		return strconv.FormatInt((us+500)/1_000, 10) + "ms"
	case us < 10_000_000:
		return strconv.FormatFloat(float64(us)/1_000_000, 'f', 1, 64) + "s"
	case us < 60_000_000:
		return strconv.FormatInt((us+500_000)/1_000_000, 10) + "s"
	default:
		d := time.Duration(us) * time.Microsecond
		return strconv.Itoa(int(d.Minutes())) + "m" + pad2(int(d.Seconds())%60) + "s"
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// formatCount renders a count compactly: 950, 1.2k, 34k, 1.2M.
func FormatCount(n float64) string {
	switch {
	case n < 0:
		return "-" + FormatCount(-n)
	case n < 1:
		if n == 0 {
			return "0"
		}
		return strconv.FormatFloat(n, 'f', 2, 64)
	case n < 10:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', 1, 64)
	case n < 1_000:
		return strconv.FormatInt(int64(n+0.5), 10)
	case n < 10_000:
		return strconv.FormatFloat(n/1_000, 'f', 1, 64) + "k"
	case n < 1_000_000:
		return strconv.FormatInt(int64(n/1_000+0.5), 10) + "k"
	case n < 10_000_000:
		return strconv.FormatFloat(n/1_000_000, 'f', 1, 64) + "M"
	default:
		return strconv.FormatInt(int64(n/1_000_000+0.5), 10) + "M"
	}
}

// formatBytes renders a byte quantity in binary units.
func FormatBytes(n float64) string {
	const unit = 1024
	switch {
	case n < unit:
		return strconv.FormatInt(int64(n), 10) + " B"
	case n < unit*unit:
		return strconv.FormatFloat(n/unit, 'f', oneDecimalBelowTen(n/unit), 64) + " KiB"
	case n < unit*unit*unit:
		return strconv.FormatFloat(n/(unit*unit), 'f', oneDecimalBelowTen(n/(unit*unit)), 64) + " MiB"
	case n < unit*unit*unit*unit:
		return strconv.FormatFloat(n/(unit*unit*unit), 'f', 1, 64) + " GiB"
	default:
		return strconv.FormatFloat(n/(unit*unit*unit*unit), 'f', 1, 64) + " TiB"
	}
}

func oneDecimalBelowTen(v float64) int {
	if v < 10 {
		return 1
	}
	return 0
}

// formatValue renders a gauge or counter value in its unit.
func FormatValue(v float64, unit string) string {
	switch unit {
	case "bytes":
		return FormatBytes(v)
	case "bytes/s":
		return FormatBytes(v) + "/s"
	case "%":
		return strconv.FormatInt(int64(v+0.5), 10) + "%"
	case "µs":
		return FormatMicros(int64(v))
	case "", "items", "cycles", "chars":
		return FormatCount(v)
	}
	return FormatCount(v) + " " + unit
}

// formatAgo renders how long ago t was, relative to now.
func FormatAgo(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 2*time.Second:
		return "now"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s ago"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	}
	return t.Format("15:04")
}
