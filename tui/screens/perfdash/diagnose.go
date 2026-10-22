package perfdash

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
	SeverityWarn
	SeverityCrit
)

// Finding is one diagnosed condition in plain language: what is wrong, how
// bad, and which owner to look at.
type Finding struct {
	Severity Severity
	Title    string
	Detail   string
	// Section is the dashboard tab holding the evidence.
	Section string
}

func severityOf(v int64, meta metrics.Meta) Severity {
	switch {
	case meta.Crit > 0 && v >= meta.Crit:
		return SeverityCrit
	case meta.Warn > 0 && v >= meta.Warn:
		return SeverityWarn
	}
	return SeverityOK
}

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
		add(Finding{Severity: SeverityWarn, Title: "Core metrics unavailable", Detail: d.CoreErr, Section: SectionCore})
	}
	add(blockingFinding(d))
	add(freezeFinding(d))
	add(inputFinding(d))
	add(busyFinding(d))
	add(latencyFinding(d, processShell, "render.flush", SectionUI, "Terminal writes are slow",
		"the renderer goroutine waits on the terminal; input is not blocked, but frames reach the screen late"))
	add(latencyFinding(d, processShell, "ui.present", SectionUI, "Frame handoff waits",
		"the UI goroutine waited to hand a frame to the renderer"))
	add(actorFinding(d))
	add(gaugePeakFinding(d, processCore, "core.queue", SectionCore, "Core backlog",
		"work queued for a workspace actor; agent events and requests wait behind it"))
	add(resyncFinding(d))
	for _, name := range []string{"storage.receipts"} {
		add(latencyFinding(d, processCore, name, SectionServices, "Slow durable commits",
			"each core mutation writes receipts on the workspace actor; a slow disk delays everything behind it"))
	}
	add(tableFinding(d, processCore, "storage.session", SectionServices, "Slow session database writes"))
	add(tableFinding(d, processShell, "link.requests", SectionCore, "Slow core requests"))
	add(tableFinding(d, processCore, "core.requests", SectionCore, "Slow core work"))
	add(latencyFinding(d, processShell, "link.fetch", SectionCore, "Slow change fetch",
		"turning core change notices into shell updates takes long; transcripts and status lag"))
	add(tableFinding(d, processCore, "lsp.requests", SectionServices, "Slow language server"))
	add(tableFinding(d, processCore, "git.commands", SectionServices, "Slow Git"))
	add(tableFinding(d, processCore, "agent.first_output", SectionAgents, "Slow model responses"))
	add(noteFinding(d, metrics.GroupLSP, SectionServices))
	add(noteFinding(d, metrics.GroupAgents, SectionAgents))
	for _, p := range d.processes() {
		add(runtimeFinding(d, p))
	}
	slices.SortStableFunc(out, func(a, b Finding) int { return int(b.Severity) - int(a.Severity) })
	return out
}

// Verdict is the overall state and a one-line summary.
func Verdict(findings []Finding) (Severity, string) {
	if len(findings) == 0 {
		return SeverityOK, "Responsive — no issues in this window"
	}
	worst := findings[0].Severity
	count := 0
	for _, f := range findings {
		if f.Severity == worst {
			count++
		}
	}
	label := "Degraded"
	if worst == SeverityCrit {
		label = "Problems found"
	}
	if count == 1 {
		return worst, label + " — " + findings[0].Title
	}
	return worst, fmt.Sprintf("%s — %s and %d more", label, findings[0].Title, len(findings)-1)
}

func blockingFinding(d Data) Finding {
	counter, ok := d.counter(processShell, "ui.blocking")
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
		Title:  fmt.Sprintf("%s blocking call(s) on the UI thread", formatCount(float64(counter.Window))),
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
		Title:  fmt.Sprintf("UI froze %d× (worst %s)", count, formatMicros(worst)),
		Detail: "longest in " + shortKey(label)}
}

func inputFinding(d Data) Finding {
	input, ok := d.latency(processShell, "ui.input")
	if !ok || input.Window.Count == 0 {
		return Finding{}
	}
	sev := severityOf(input.Window.P95, input.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	detail := "Update + paint per input"
	if wait, ok := d.latency(processShell, "ui.input.wait"); ok && wait.Window.P95 >= 4_000 {
		detail = fmt.Sprintf("input queued %s (p95) behind other UI work", formatMicros(wait.Window.P95))
	}
	if owner := topTableKey(d, processShell, "ui.messages"); owner != "" {
		detail += "; top UI consumer " + owner
	}
	return Finding{Severity: sev, Section: SectionUI,
		Title:  "Input latency " + formatMicros(input.Window.P95) + " (p95)",
		Detail: detail}
}

func busyFinding(d Data) Finding {
	busy, ok := d.busySeries()
	if !ok {
		return Finding{}
	}
	peak := 0.0
	for _, v := range busy {
		peak = max(peak, v)
	}
	sev := SeverityOK
	switch {
	case peak >= 80:
		sev = SeverityCrit
	case peak >= 40:
		sev = SeverityWarn
	}
	if sev == SeverityOK {
		return Finding{}
	}
	detail := "work on the UI goroutine delays input"
	if owner := topTableKey(d, processShell, "ui.messages"); owner != "" {
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
			Title:  fmt.Sprintf("Workspace actor stalled %d× (worst %s)", count, formatMicros(worst)),
			Detail: "agent events and requests waited behind " + label}
	}
	return latencyFinding(d, processCore, "core.actor", SectionCore, "Workspace actor is slow",
		"agent events and requests wait while each step runs")
}

func resyncFinding(d Data) Finding {
	total := uint64(0)
	if c, ok := d.counter(processCore, "core.resync"); ok {
		total += c.Window
	}
	if c, ok := d.counter(processShell, "link.resync"); ok {
		total += c.Window
	}
	if total == 0 {
		return Finding{}
	}
	return Finding{Severity: SeverityWarn, Section: SectionCore,
		Title:  fmt.Sprintf("Shell reloaded workspace snapshots %d×", total),
		Detail: "the shell fell behind the core's change stream or reconnected; each reload is a burst of work"}
}

func latencyFinding(d Data, p process, name, section, title, detail string) Finding {
	series, ok := d.latency(p, name)
	if !ok || series.Window.Count == 0 {
		return Finding{}
	}
	sev := severityOf(series.Window.P95, series.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title:  fmt.Sprintf("%s: %s p95, %s max", title, formatMicros(series.Window.P95), formatMicros(series.Window.Max)),
		Detail: detail}
}

func gaugePeakFinding(d Data, p process, name, section, title, detail string) Finding {
	series, ok := d.gauge(p, name)
	if !ok {
		return Finding{}
	}
	sev := severityOf(series.Peak, series.Meta)
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title: fmt.Sprintf("%s peaked at %s", title, formatValue(float64(series.Peak), series.Meta.Unit)), Detail: detail}
}

// tableFinding reports the slowest key whose worst observation crossed the
// table's thresholds.
func tableFinding(d Data, p process, name, section, title string) Finding {
	table, ok := d.table(p, name)
	if !ok {
		return Finding{}
	}
	var worst metrics.TableRow
	sev := SeverityOK
	for _, row := range table.Rows {
		if row.Count == 0 {
			continue
		}
		mean := row.Sum / int64(row.Count)
		rowSev := max(severityOf(mean, table.Meta), min(severityOf(row.Max, table.Meta), SeverityWarn))
		if rowSev > sev || rowSev == sev && rowSev > SeverityOK && row.Max > worst.Max {
			sev, worst = rowSev, row
		}
	}
	if sev == SeverityOK {
		return Finding{}
	}
	return Finding{Severity: sev, Section: section,
		Title: fmt.Sprintf("%s: %s", title, shortKey(worst.Key)),
		Detail: fmt.Sprintf("%s calls, %s average, %s worst", formatCount(float64(worst.Count)),
			formatMicros(worst.Sum/int64(max(worst.Count, 1))), formatMicros(worst.Max))}
}

// noteFinding surfaces explicit failures (server crashes, start failures)
// recorded by an owner.
func noteFinding(d Data, group, section string) Finding {
	var labels = map[string]int{}
	sev := SeverityOK
	title := ""
	for _, event := range d.events() {
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

func runtimeFinding(d Data, p process) Finding {
	if cpu, ok := d.gauge(p, "runtime.cpu"); ok {
		recent := lastSet(cpu)
		if sev := severityOf(recent, cpu.Meta); sev > SeverityOK {
			return Finding{Severity: sev, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s CPU at %d%%", p.label(), recent),
				Detail: "see which series grew at the same time on the other tabs"}
		}
	}
	if pause, ok := d.gauge(p, "runtime.gc.pause"); ok {
		if sev := severityOf(pause.Peak, pause.Meta); sev > SeverityOK {
			return Finding{Severity: SeverityWarn, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s GC pause up to %s", p.label(), formatMicros(pause.Peak)),
				Detail: "allocation churn; every pause also stalls that process's work"}
		}
	}
	if goroutines, ok := d.gauge(p, "runtime.goroutines"); ok {
		first, last := firstSet(goroutines), lastSet(goroutines)
		if first > 0 && last-first > 500 && last > 2*first {
			return Finding{Severity: SeverityWarn, Section: SectionRuntime,
				Title:  fmt.Sprintf("%s goroutines grew %d → %d", p.label(), first, last),
				Detail: "steady growth without matching work suggests a leak"}
		}
	}
	return Finding{}
}

func firstSet(series metrics.GaugeSeries) int64 {
	for _, p := range series.Points {
		if p.Set {
			return p.Last
		}
	}
	return 0
}

func lastSet(series metrics.GaugeSeries) int64 {
	for i := len(series.Points) - 1; i >= 0; i-- {
		if series.Points[i].Set {
			return series.Points[i].Last
		}
	}
	return series.Current
}

func topTableKey(d Data, p process, name string) string {
	table, ok := d.table(p, name)
	if !ok || len(table.Rows) == 0 {
		return ""
	}
	row := table.Rows[0]
	return fmt.Sprintf("%s (%.0f%%)", shortKey(row.Key), tableShare(table, row)*100)
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
