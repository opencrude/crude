package perfdash

import (
	"crude/internal/metrics"
	"crude/internal/perfdata"
)

const (
	SeverityOK      = perfdata.SeverityOK
	SeverityUnknown = perfdata.SeverityUnknown
	SeverityWarn    = perfdata.SeverityWarn
	SeverityCrit    = perfdata.SeverityCrit
)

// Diagnose turns performance data into findings.
func Diagnose(d Data) []perfdata.Finding { return perfdata.Diagnose(perfdata.Data(d)) }

// Verdict returns the severity and the summary of a finding set.
func Verdict(findings []perfdata.Finding) (perfdata.Severity, string) {
	return perfdata.Verdict(findings)
}
func severityOf(v int64, meta metrics.Meta) perfdata.Severity { return perfdata.SeverityOf(v, meta) }
func lastSet(s metrics.GaugeSeries) int64                     { return perfdata.LastSet(s) }
