package perfdata

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/metrics"
	"crude/types/inspect"
)

// Headline is a neutral, formatted reading, not a rendered control.
type Headline struct {
	Label    string   `json:"label"`
	Value    string   `json:"value"`
	Severity Severity `json:"severity"`
}

// Result derives findings and headline evidence from exactly the supplied sample.
// Unknown coverage is reported separately from positive problem evidence.
type Result struct {
	Role      string     `json:"role"`
	Severity  Severity   `json:"severity"`
	Verdict   string     `json:"verdict"`
	Findings  []Finding  `json:"findings"`
	Headlines []Headline `json:"headlines"`
}

func Analyze(snapshot metrics.Snapshot, role string) Result {
	d := Data{Fetched: snapshot.Completed}
	switch strings.ToLower(role) {
	case "core":
		d.Core = &snapshot
	case "shell":
		d.Shell = snapshot
	default:
		f := []Finding{{Severity: SeverityUnknown, Title: "Process role unavailable", Detail: "expected shell or core; snapshot was not attributed", Section: SectionRuntime}}
		sev, verdict := Verdict(f)
		return Result{Role: role, Severity: sev, Verdict: verdict, Findings: f}
	}
	f := Diagnose(d)
	sev, verdict := Verdict(f)
	return Result{Role: role, Severity: sev, Verdict: verdict, Findings: f, Headlines: OverviewHeadlines(d)}
}

// InspectionRecords decorates the metrics owner's existing inspection snapshot.
// It never collects, stores or refreshes evidence, so UI and agents use the same
// window, provenance and canonical diagnosis rules.
func InspectionRecords(snapshot metrics.Snapshot, role string) []inspect.Record {
	result := Analyze(snapshot, role)
	record := func(kind, id string, v any) inspect.Record {
		raw, _ := json.Marshal(v)
		return inspect.Record{Kind: kind, ID: id, Value: raw}
	}
	out := []inspect.Record{record("verdict", "performance.verdict", struct {
		Role     string   `json:"role"`
		Severity Severity `json:"severity"`
		Verdict  string   `json:"verdict"`
	}{result.Role, result.Severity, result.Verdict})}
	for i, f := range result.Findings {
		out = append(out, record("finding", fmt.Sprintf("performance.finding.%d", i), f))
	}
	for i, h := range result.Headlines {
		out = append(out, record("headline", fmt.Sprintf("performance.headline.%d", i), h))
	}
	return out
}

// OverviewHeadlines supplies the same headline readings to all consumers.
func OverviewHeadlines(d Data) []Headline {
	var out []Headline
	addLatency := func(label string, p Process, name string) {
		series, ok := d.Latency(p, name)
		if !ok {
			return
		}
		if series.Recent.Count == 0 {
			out = append(out, Headline{Label: label, Value: "idle (no recent observations)", Severity: SeverityUnknown})
			return
		}
		out = append(out, Headline{label, FormatMicros(series.Recent.P95), SeverityOf(series.Recent.P95, series.Meta)})
	}
	addLatency("Input", ProcessShell, "ui.input")
	if _, ok := d.BusySeries(); ok {
		busy := d.RecentBusy()
		out = append(out, Headline{"UI busy", FormatValue(busy, "%"), BusySeverity(busy)})
	}
	addLatency("Paint", ProcessShell, "ui.view")
	addLatency("Terminal", ProcessShell, "render.flush")
	addLatency("Core actor", ProcessCore, "core.actor")
	for _, v := range ResourceViews(d) {
		out = append(out, ResourceHeadlines(v)[:2]...)
	}
	if streams, ok := d.Gauge(ProcessCore, "agent.streams"); ok && LastSet(streams) > 0 {
		out = append(out, Headline{Label: "Model streams", Value: FormatCount(float64(LastSet(streams)))})
	}
	return out
}

func ResourceHeadlines(v ResourceView) []Headline {
	memory := FormatBytes(float64(v.Memory))
	memorySev := SeverityOK
	if !v.MemoryComplete {
		memory += " known (partial)"
		memorySev = SeverityUnknown
		if v.MemoryKnown == 0 {
			memory = "unknown"
		}
	}
	cpu := "unknown / warming up"
	cpuSev := SeverityUnknown
	if v.CPU >= 0 {
		cpu = FormatValue(v.CPU, "%")
		cpuSev = SeverityOK
		if !v.CPUComplete {
			cpu += " known (partial)"
			cpuSev = SeverityUnknown
		}
	}
	out := []Headline{{v.Label + " memory", memory, memorySev}, {v.Label + " CPU", cpu, cpuSev}}
	if s := v.System; s.MemoryTotal > 0 && s.Error == "" {
		out = append(out, Headline{v.Label + " host memory", fmt.Sprintf("%s of %s", FormatBytes(float64(s.MemoryTotal-s.MemoryAvailable)), FormatBytes(float64(s.MemoryTotal))), MemorySeverity(s)})
	}
	if v.System.CPU >= 0 && v.System.Error == "" {
		out = append(out, Headline{Label: v.Label + " host CPU", Value: fmt.Sprintf("%s of %d cores", FormatValue(v.System.CPU, "%"), v.System.CPUs)})
	}
	if disk := v.Disk; disk != nil {
		value := "measuring…"
		sev := SeverityUnknown
		if !disk.MeasuredAt.IsZero() {
			value = FormatBytes(float64(disk.Total))
			sev = SeverityOK
		}
		if disk.Err != "" {
			value += " (" + disk.Err + ")"
			sev = SeverityUnknown
		}
		out = append(out, Headline{v.Label + " data", value, sev})
		if disk.DeviceTotal > 0 {
			out = append(out, Headline{v.Label + " disk free", fmt.Sprintf("%s of %s", FormatBytes(float64(disk.DeviceFree)), FormatBytes(float64(disk.DeviceTotal))), DiskSeverity(disk)})
		}
	}
	return out
}
