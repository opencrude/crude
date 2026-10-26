package perfdash

import (
	"crude/metrics"
	"crude/perfdata"
)

type Severity = perfdata.Severity
type Finding = perfdata.Finding

const (
	SeverityOK      = perfdata.SeverityOK
	SeverityUnknown = perfdata.SeverityUnknown
	SeverityWarn    = perfdata.SeverityWarn
	SeverityCrit    = perfdata.SeverityCrit
)

func Diagnose(d Data) []Finding                      { return perfdata.Diagnose(perfdata.Data(d)) }
func Verdict(findings []Finding) (Severity, string)  { return perfdata.Verdict(findings) }
func severityOf(v int64, meta metrics.Meta) Severity { return perfdata.SeverityOf(v, meta) }
func lastSet(s metrics.GaugeSeries) int64            { return perfdata.LastSet(s) }
