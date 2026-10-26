package perfdata

import (
	"fmt"
	"slices"
	"strings"

	"crude/metrics"
)

// Severity orders findings; the verdict is the worst one.
type Severity uint8

const (
	SeverityOK Severity = iota
	SeverityUnknown
	SeverityWarn
	SeverityCrit
)

// Finding is one diagnosed condition in plain language: what is wrong, how
// bad, and which owner to look at.
type Finding struct {
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	// Section is the dashboard tab holding the evidence.
	Section string `json:"section"`
}

func SeverityOf(v int64, meta metrics.Meta) Severity {
	return ThresholdSeverity(float64(v), float64(meta.Warn), float64(meta.Crit))
}
func ThresholdSeverity(value, warn, crit float64) Severity {
	if crit > 0 && value >= crit {
		return SeverityCrit
	}
	if warn > 0 && value >= warn {
		return SeverityWarn
	}
	return SeverityOK
}

const (
	BusyWarn = 40
	BusyCrit = 80
)

// Diagnose turns both snapshots into ranked findings. Rules read thresholds
// from series metadata, so the owner of a series defines what is unhealthy.
func Diagnose(d Data) []Finding {
	var out []Finding
	add := func(f Finding) {
		if f.Severity > SeverityOK {
			out = append(out, f)
		}
	}
	if d.CoreErr != "" {
		add(Finding{Severity: SeverityUnknown, Title: "Core metrics unavailable", Detail: d.CoreErr, Section: SectionCore})
	}
	if len(d.Processes()) == 0 {
		add(Finding{Severity: SeverityUnknown, Title: "Measurements unavailable", Detail: "no process snapshot was supplied", Section: SectionRuntime})
	}
	for _, p := range d.Processes() {
		name, section := "ui.input", SectionUI
		if p == ProcessCore {
			name, section = "core.actor", SectionCore
		}
		if s, ok := d.Latency(p, name); !ok || s.Window.Count == 0 {
			add(Finding{Severity: SeverityUnknown, Title: p.Label() + " latency not observed", Detail: "no observations in this window; absence of work is not a responsiveness measurement", Section: section})
		}
	}
	add(blockingFinding(d))
	add(freezeFinding(d))
	add(inputFinding(d))
	add(busyFinding(d))
	add(latencyFinding(d, ProcessShell, "render.flush", SectionUI, "Terminal writes are slow",
		"the renderer goroutine waits on the terminal; input is not blocked, but frames reach the screen late"))
	add(latencyFinding(d, ProcessShell, "ui.present", SectionUI, "Frame handoff waits",
		"the UI goroutine waited to hand a frame to the renderer"))
	add(actorFinding(d))
	add(gaugePeakFinding(d, ProcessCore, "core.queue", SectionCore, "Core backlog",
		"work queued for a workspace actor; agent events and requests wait behind it"))
	add(resyncFinding(d))
	for _, name := range []string{"storage.receipts"} {
		add(latencyFinding(d, ProcessCore, name, SectionServices, "Slow durable commits",
			"each core mutation writes receipts on the workspace actor; a slow disk delays everything behind it"))
	}
	add(tableFinding(d, ProcessCore, "storage.session", SectionServices, "Slow session database writes"))
	add(tableFinding(d, ProcessShell, "link.requests", SectionCore, "Slow core requests"))
	add(tableFinding(d, ProcessCore, "core.requests", SectionCore, "Slow core work"))
	add(latencyFinding(d, ProcessShell, "link.fetch", SectionCore, "Slow change fetch",
		"turning core change notices into shell updates takes long; transcripts and status lag"))
	add(tableFinding(d, ProcessCore, "lsp.requests", SectionServices, "Slow language server"))
	add(tableFinding(d, ProcessCore, "git.commands", SectionServices, "Slow Git"))
	add(tableFinding(d, ProcessCore, "git.remote", SectionServices, "Slow Git remote"))
	add(tableFinding(d, ProcessCore, "agent.first_output", SectionAgents, "Slow model responses"))
	add(noteFinding(d, metrics.GroupLSP, SectionServices))
	add(noteFinding(d, metrics.GroupAgents, SectionAgents))
	for _, p := range d.Processes() {
		add(runtimeFinding(d, p))
	}
	for _, f := range ResourceFindings(d) {
		add(f)
	}
	slices.SortStableFunc(out, func(a, b Finding) int { return int(b.Severity) - int(a.Severity) })
	return out
}

// Verdict is the overall state and a one-line summary.
func Verdict(findings []Finding) (Severity, string) {
	if len(findings) == 0 {
		return SeverityOK, "No issues observed in available measurements"
	}
	worst := findings[0].Severity
	title := findings[0].Title
	for _, f := range findings {
		if f.Severity > worst {
			worst = f.Severity
			title = f.Title
		}
	}
	count := 0
	for _, f := range findings {
		if f.Severity == worst {
			count++
		}
	}
	label := "Degraded"
	if worst == SeverityUnknown {
		label = "Unknown"
	}
	if worst == SeverityCrit {
		label = "Problems found"
	}
	if count == 1 {
		return worst, label + " — " + title
	}
	return worst, fmt.Sprintf("%s — %s and %d more", label, title, len(findings)-1)
}

func blockingFinding(d Data) Finding {
	counter, ok := d.Counter(ProcessShell, "ui.blocking")
	if !ok || counter.Window == 0 {
		return Finding{}
	}
	labels := map[string]int{}
	for _, event := range d.Shell.Events {
		if event.Series == "ui.blocking" {
			labels[event.Title+" "+event.Label]++
		}
	}
	return Finding{Severity: SeverityCrit, Section: SectionUI,
		Title:  fmt.Sprintf("%s blocking call(s) on the UI thread", FormatCount(float64(counter.Window))),
		Detail: "input waits while they run: " + topLabels(labels, 3)}
}

func freezeFinding(d Data) Finding {
	worst := int64(0)
	label := ""
	count := 0
	for _, event := range d.Shell.Events {
		if event.Series != "ui.update" && event.Series != "ui.view" {
			continue
		}
		count++
		if event.Micros > worst {
			worst, label = event.Micros, event.Label
			if event.Series == "ui.view" {
				label = "frame paint after " + label
			}
		}
	}
	if count == 0 {
		return Finding{}
	}
	return Finding{Severity: SeverityCrit, Section: SectionUI,
		Title:  fmt.Sprintf("UI froze %d× (worst %s)", count, FormatMicros(worst)),
		Detail: "longest in " + ShortKey(label)}
}

func inputFinding(d Data) Finding {
	input, ok := d.Latency(ProcessShell, "ui.input")
	if !ok || input.Window.Count == 0 {
		return Finding{}
	}
	sev := SeverityOf(input.Window.P95, input.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	detail := "Update + paint per input"
	if wait, ok := d.Latency(ProcessShell, "ui.input.wait"); ok && wait.Window.P95 >= 4_000 {
		detail = fmt.Sprintf("input queued %s (p95) behind other UI work", FormatMicros(wait.Window.P95))
	}
	if owner := topTableKey(d, ProcessShell, "ui.messages"); owner != "" {
		detail += "; top UI consumer " + owner
	}
	return Finding{Severity: sev, Section: SectionUI,
		Title:  "Input latency " + FormatMicros(input.Window.P95) + " (p95)",
		Detail: detail}
}

func busyFinding(d Data) Finding {
	busy, ok := metrics.UIBusy(d.Shell)
	if !ok {
		return Finding{}
	}
	peak := busy.Peak
	sev := BusySeverity(peak)
	if sev == SeverityOK {
		return Finding{}
	}
	detail := "work on the UI goroutine delays input"
	if owner := topTableKey(d, ProcessShell, "ui.messages"); owner != "" {
		detail = "most time in " + owner
	}
	return Finding{Severity: sev, Section: SectionUI,
		Title: fmt.Sprintf("UI thread busy %.0f%% at peak", peak), Detail: detail}
}

func actorFinding(d Data) Finding {
	worst := int64(0)
	label := ""
	count := 0
	if d.Core != nil {
		for _, event := range d.Core.Events {
			if event.Series == "core.actor" {
				count++
				if event.Micros > worst {
					worst, label = event.Micros, event.Label
				}
			}
		}
	}
	if count > 0 {
		return Finding{Severity: SeverityCrit, Section: SectionCore,
			Title:  fmt.Sprintf("Workspace actor stalled %d× (worst %s)", count, FormatMicros(worst)),
			Detail: "agent events and requests waited behind " + label}
	}
	return latencyFinding(d, ProcessCore, "core.actor", SectionCore, "Workspace actor is slow",
		"agent events and requests wait while each step runs")
}

func resyncFinding(d Data) Finding {
	total := uint64(0)
	if c, ok := d.Counter(ProcessCore, "core.resync"); ok {
		total += c.Window
	}
	if c, ok := d.Counter(ProcessShell, "link.resync"); ok {
		total += c.Window
	}
	if total == 0 {
		return Finding{}
	}
	return Finding{Severity: SeverityWarn, Section: SectionCore,
		Title:  fmt.Sprintf("Shell reloaded workspace snapshots %d×", total),
		Detail: "the shell fell behind the core's change stream or reconnected; each reload is a burst of work"}
}

func latencyFinding(d Data, p Process, name, section, title, detail string) Finding {
	series, ok := d.Latency(p, name)
	if !ok || series.Window.Count == 0 {
		return Finding{}
	}
	sev := SeverityOf(series.Window.P95, series.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title:  fmt.Sprintf("%s: %s p95, %s max", title, FormatMicros(series.Window.P95), FormatMicros(series.Window.Max)),
		Detail: detail}
}

func gaugePeakFinding(d Data, p Process, name, section, title, detail string) Finding {
	series, ok := d.Gauge(p, name)
	if !ok {
		return Finding{}
	}
	sev := SeverityOf(series.Peak, series.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title: fmt.Sprintf("%s peaked at %s", title, FormatValue(float64(series.Peak), series.Meta.Unit)), Detail: detail}
}

// tableFinding reports the slowest key whose worst observation crossed the
// table's thresholds.
func tableFinding(d Data, p Process, name, section, title string) Finding {
	table, ok := d.Table(p, name)
	if !ok {
		return Finding{}
	}
	var worst metrics.TableRow
	sev := SeverityOK
	for _, row := range DiagnosticRows(table) {
		if row.Count == 0 {
			continue
		}
		mean := row.Sum / int64(row.Count)
		rowSev := max(SeverityOf(mean, table.Meta), min(SeverityOf(row.Max, table.Meta), SeverityWarn))
		if rowSev > sev || rowSev == sev && rowSev > SeverityOK && row.Max > worst.Max {
			sev, worst = rowSev, row
		}
	}
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title: fmt.Sprintf("%s: %s", title, ShortKey(worst.Key)),
		Detail: fmt.Sprintf("%s calls, %s average, %s worst", FormatCount(float64(worst.Count)),
			FormatMicros(worst.Sum/int64(max(worst.Count, 1))), FormatMicros(worst.Max))}
}

// noteFinding surfaces explicit failures (server crashes, start failures)
// recorded by an owner.
func noteFinding(d Data, group, section string) Finding {
	var labels = map[string]int{}
	sev := SeverityOK
	title := ""
	for _, event := range d.Events() {
		if event.Group != group || event.Series != "" {
			continue
		}
		eventSev := SeverityWarn
		if event.Severity == "crit" {
			eventSev = SeverityCrit
		}
		if eventSev > sev {
			sev, title = eventSev, event.Title
		}
		labels[event.Title+": "+event.Label]++
	}
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section, Title: title, Detail: topLabels(labels, 2)}
}

func runtimeFinding(d Data, p Process) Finding {
	if cpu, ok := d.Gauge(p, "runtime.cpu"); ok {
		recent := LastSet(cpu)
		if sev := SeverityOf(recent, cpu.Meta); sev > SeverityOK {
			return Finding{Severity: sev, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s CPU at %d%%", p.Label(), recent),
				Detail: "see which series grew at the same time on the other tabs"}
		}
	}
	if pause, ok := d.Gauge(p, "runtime.gc.pause"); ok {
		if sev := SeverityOf(pause.Peak, pause.Meta); sev > SeverityOK {
			return Finding{Severity: SeverityWarn, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s GC pause up to %s", p.Label(), FormatMicros(pause.Peak)),
				Detail: "allocation churn; every pause also stalls that process's work"}
		}
	}
	if goroutines, ok := d.Gauge(p, "runtime.goroutines"); ok {
		first, last := goroutines.WindowFirst, goroutines.WindowLast
		if first > 0 && last-first > 500 && last > 2*first {
			return Finding{Severity: SeverityWarn, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s goroutines grew %d → %d", p.Label(), first, last),
				Detail: "steady growth without matching work suggests a leak"}
		}
	}
	return Finding{}
}

func FirstSet(series metrics.GaugeSeries) int64 {
	for _, p := range series.Points {
		if p.Set {
			return p.Last
		}
	}
	return 0
}

func LastSet(series metrics.GaugeSeries) int64 {
	for i := len(series.Points) - 1; i >= 0; i-- {
		if series.Points[i].Set {
			return series.Points[i].Last
		}
	}
	return series.Current
}

func topTableKey(d Data, p Process, name string) string {
	table, ok := d.Table(p, name)
	if !ok || len(table.Rows) == 0 {
		return ""
	}
	row := table.Rows[0]
	return fmt.Sprintf("%s (%.0f%%)", ShortKey(row.Key), TableShare(table, row)*100)
}

func topLabels(labels map[string]int, n int) string {
	type entry struct {
		label string
		count int
	}
	entries := make([]entry, 0, len(labels))
	for label, count := range labels {
		entries = append(entries, entry{strings.TrimSpace(label), count})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.count != b.count {
			return b.count - a.count
		}
		return strings.Compare(a.label, b.label)
	})
	parts := make([]string, 0, n)
	for i, e := range entries {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d more", len(entries)-n))
			break
		}
		if e.count > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", e.label, e.count))
		} else {
			parts = append(parts, e.label)
		}
	}
	return strings.Join(parts, ", ")
}

// BusySeverity is shared by the headline and window finding.
func BusySeverity(percent float64) Severity {
	if percent >= BusyCrit {
		return SeverityCrit
	}
	if percent >= BusyWarn {
		return SeverityWarn
	}
	return SeverityOK
}
