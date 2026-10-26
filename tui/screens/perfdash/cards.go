package perfdash

import (
	"fmt"
	"time"

	"crude/metrics"
	"crude/perfdata"
)

// chartKind selects how a card reads its series.
type chartKind uint8

const (
	chartLatency chartKind = iota
	chartRate              // counter: per-second rate
	chartLevel             // gauge: level
	chartPercent           // derived percentage (UI busy)
)

// card is one prepared chart: values per point plus the readings shown
// around it. Building it once per refresh keeps paint allocation-free.
type card struct {
	ref      seriesRef
	title    string
	help     string
	kind     chartKind
	unit     string
	values   []float64 // p95 (latency), rate, level or percent per point
	peaks    []float64 // max per point (latency/gauge); nil otherwise
	counts   []uint64  // observations per point (latency)
	warn     float64
	crit     float64
	headline string
	headSev  Severity
	footer   string
	step     int
	end      int64
}

func (c *card) severity(v float64) Severity {
	return perfdata.ThresholdSeverity(v, c.warn, c.crit)
}

// format renders a value in the card's unit.
func (c *card) format(v float64) string {
	switch c.kind {
	case chartLatency:
		return formatMicros(int64(v))
	case chartRate:
		return formatValue(v, c.unit) + "/s"
	case chartPercent:
		return formatValue(v, "%")
	}
	return formatValue(v, c.unit)
}

// readout describes point i for the hover line.
func (c *card) readout(i int) string {
	if i < 0 || i >= len(c.values) {
		return ""
	}
	at := time.Unix(c.end-int64((len(c.values)-1-i)*c.step), 0).Format("15:04:05")
	switch c.kind {
	case chartLatency:
		if c.counts[i] == 0 {
			return at + " · idle"
		}
		return fmt.Sprintf("%s · p95 %s · max %s · %s×", at, c.format(c.values[i]), c.format(c.peaks[i]), formatCount(float64(c.counts[i])))
	case chartLevel:
		if c.peaks != nil && c.peaks[i] > c.values[i] {
			return fmt.Sprintf("%s · %s · peak %s", at, c.format(c.values[i]), c.format(c.peaks[i]))
		}
	}
	return at + " · " + c.format(c.values[i])
}

func titleFor(d Data, ref seriesRef, title string) string {
	if d.Core != nil && ref.name != busyName && sectionOfName(ref.name) == SectionRuntime {
		return title + " · " + ref.process.Label()
	}
	return title
}

func sectionOfName(name string) string {
	if len(name) >= 8 && name[:8] == "runtime." {
		return SectionRuntime
	}
	return ""
}

// buildCard prepares the chart for ref, or reports false for tables and
// absent series.
func buildCard(d Data, ref seriesRef) (card, bool) {
	snapshot := d.snapshot(ref.process)
	if snapshot == nil {
		return card{}, false
	}
	c := card{ref: ref, step: max(snapshot.Step, 1), end: snapshot.End}
	if ref.name == busyName {
		values, ok := d.busySeries()
		if !ok {
			return card{}, false
		}
		c.title, c.kind, c.values = "UI busy", chartPercent, values
		c.help = "Share of wall time the UI goroutine spent in Update and frame paint. Input waits whenever it is busy."
		c.warn, c.crit = perfdata.BusyWarn, perfdata.BusyCrit
		recent := d.recentBusy()
		c.headline, c.headSev = c.format(recent), c.severity(recent)
		peak := 0.0
		for _, v := range values {
			peak = max(peak, v)
		}
		c.footer = "peak " + c.format(peak) + " · " + windowLabel(d)
		return c, true
	}
	if series, ok := d.latency(ref.process, ref.name); ok {
		c.title, c.help, c.kind = titleFor(d, ref, series.Meta.Title), series.Meta.Help, chartLatency
		c.warn, c.crit = float64(series.Meta.Warn), float64(series.Meta.Crit)
		c.values = make([]float64, len(series.Points))
		c.peaks = make([]float64, len(series.Points))
		c.counts = make([]uint64, len(series.Points))
		for i, p := range series.Points {
			c.values[i], c.peaks[i], c.counts[i] = float64(p.P95), float64(p.Max), p.Count
		}
		current := series.Recent.P95
		c.headline, c.headSev = "p95 "+formatMicros(current), severityOf(current, series.Meta)
		if series.Recent.Count == 0 {
			c.headline, c.headSev = "idle", SeverityOK
		}
		w := series.Window
		c.footer = fmt.Sprintf("p50 %s · p99 %s · max %s · %s×", formatMicros(w.P50), formatMicros(w.P99), formatMicros(w.Max), formatCount(float64(w.Count)))
		return c, true
	}
	if series, ok := d.counter(ref.process, ref.name); ok {
		c.title, c.help, c.kind, c.unit = titleFor(d, ref, series.Meta.Title), series.Meta.Help, chartRate, series.Meta.Unit
		c.warn, c.crit = float64(series.Meta.Warn), float64(series.Meta.Crit)
		c.values = make([]float64, len(series.Points))
		for i, n := range series.Points {
			c.values[i] = float64(n) / float64(c.step)
		}
		recent := 0.0
		if n := len(c.values); n > 0 {
			recent = c.values[n-1]
			if n > 1 && c.step == 1 {
				recent = (c.values[n-1] + c.values[n-2]) / 2
			}
		}
		c.headline, c.headSev = c.format(recent), c.severity(recent)
		c.footer = fmt.Sprintf("%s in window · %s total", formatValue(float64(series.Window), series.Meta.Unit), formatValue(float64(series.Total), series.Meta.Unit))
		return c, true
	}
	if series, ok := d.gauge(ref.process, ref.name); ok {
		c.title, c.help, c.kind, c.unit = titleFor(d, ref, series.Meta.Title), series.Meta.Help, chartLevel, series.Meta.Unit
		c.warn, c.crit = float64(series.Meta.Warn), float64(series.Meta.Crit)
		c.values = make([]float64, len(series.Points))
		c.peaks = make([]float64, len(series.Points))
		last := 0.0
		for i, p := range series.Points {
			if p.Set {
				last = float64(p.Last)
			}
			// A gauge holds its level between samples.
			c.values[i], c.peaks[i] = last, max(last, float64(p.Max))
		}
		current := float64(lastSet(series))
		c.headline, c.headSev = c.format(current), c.severity(current)
		c.footer = "peak " + c.format(float64(series.Peak)) + " · " + windowLabel(d)
		return c, true
	}
	return card{}, false
}

func windowLabel(d Data) string {
	seconds := d.windowSeconds()
	if seconds >= 60 {
		return fmt.Sprintf("last %dm", seconds/60)
	}
	return fmt.Sprintf("last %ds", seconds)
}

// tableBlockData is a prepared keyed table.
type tableBlockData struct {
	ref   seriesRef
	title string
	help  string
	meta  metrics.Meta
	rows  []metrics.TableRow
	total int64
}

func buildTable(d Data, ref seriesRef) (tableBlockData, bool) {
	table, ok := d.table(ref.process, ref.name)
	if !ok {
		return tableBlockData{}, false
	}
	out := tableBlockData{ref: ref, title: table.Meta.Title, help: table.Meta.Help, meta: table.Meta, rows: table.Rows}
	for _, row := range perfdata.DiagnosticRows(table) {
		out.total += row.Sum
	}
	return out, true
}
