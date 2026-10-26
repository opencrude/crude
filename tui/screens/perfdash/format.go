package perfdash

import (
	"crude/perfdata"
	"time"
)

func formatMicros(us int64) string              { return perfdata.FormatMicros(us) }
func formatCount(n float64) string              { return perfdata.FormatCount(n) }
func formatBytes(n float64) string              { return perfdata.FormatBytes(n) }
func formatValue(v float64, unit string) string { return perfdata.FormatValue(v, unit) }
func formatAgo(now, t time.Time) string         { return perfdata.FormatAgo(now, t) }
