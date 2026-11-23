package perfdata

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"crude/internal/buildinfo"
	"crude/internal/metrics"
)

// Report is one shareable performance report: a Markdown file a user can
// read in Crude and send to whoever asked for it. Every part is optional. A
// report from the command line has no live window, and a report from the
// Performance panel still works when the saved history cannot be read.
type Report struct {
	Created    time.Time
	Host       ReportHost
	Live       *Data     // the panel's last refresh of the shell and the core
	History    []History // saved hours, one per process role
	HistoryErr string
}

// ReportHost names the build and the machine, without personal data.
type ReportHost struct {
	Version, Commit, Build, Cohort string
	Platform, GoVersion            string
	CPUs, Parallelism              int
	Terminal                       string
}

// CurrentHost describes this process's build and machine. getenv reads the
// environment that names the terminal; nil leaves the terminal unknown.
func CurrentHost(getenv func(string) string) ReportHost {
	host := ReportHost{Version: buildinfo.Version, Commit: buildinfo.Commit, Build: buildinfo.ExecutableID(),
		Cohort: buildinfo.ProtocolBuild, Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(),
		CPUs: runtime.NumCPU(), Parallelism: runtime.GOMAXPROCS(0)}
	if getenv != nil {
		host.Terminal = terminalName(getenv)
	}
	return host
}

// terminalName names the terminal program from its usual environment marks.
func terminalName(getenv func(string) string) string {
	switch {
	case getenv("WT_SESSION") != "":
		return "Windows Terminal"
	case getenv("TERM_PROGRAM") != "":
		name := getenv("TERM_PROGRAM")
		if version := getenv("TERM_PROGRAM_VERSION"); version != "" {
			name += " " + version
		}
		return name
	}
	return getenv("TERM")
}

// ReadReportHistory reads the saved history of both processes for a report.
// A history that cannot be read is named in the report, not fatal to it.
func ReadReportHistory(ctx context.Context, archive string, r *Report) {
	for _, role := range []string{"shell", "core"} {
		history, err := ReadHistory(ctx, archive, role, ReportHistoryHours)
		if err != nil {
			r.HistoryErr = "Some saved measurements could not be read."
			continue
		}
		r.History = append(r.History, history)
	}
}

// ReportHistoryHours is the saved history a report includes.
const ReportHistoryHours = 24

// ReportsDir is where saved reports go in the selected Crude profile.
func ReportsDir(home string) string {
	return filepath.Join(home, buildinfo.DataDir(), "reports")
}

// SaveReport writes r to a new file in dir and returns its path.
func SaveReport(dir string, r Report) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "crude-performance-"+r.Created.Format("20060102-150405")+"-*.md")
	if err != nil {
		return "", err
	}
	writeErr := WriteReport(f, r)
	closeErr := f.Close()
	if err := cmp.Or(writeErr, closeErr); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// The report's category order is the dashboard's: the UI thread first,
// then the work it waits for.
var reportGroups = []string{metrics.GroupUI, metrics.GroupRender, metrics.GroupRPC, metrics.GroupCore, metrics.GroupAgents,
	metrics.GroupTools, metrics.GroupLSP, metrics.GroupFiles, metrics.GroupGit, metrics.GroupSuggest, metrics.GroupStorage, metrics.GroupRuntime}

func groupRank(group string) int {
	if i := slices.Index(reportGroups, group); i >= 0 {
		return i
	}
	return len(reportGroups)
}

// WriteReport writes r as Markdown.
func WriteReport(w io.Writer, r Report) error {
	b := &reportWriter{}
	b.line("# Crude performance report")
	b.line("")
	b.line("Created %s. User text, raw errors, and private operation labels are not included.", r.Created.Format(time.RFC3339))
	b.line("")
	b.line("[Recent stalls](#stalls) · [Build and system](#build-and-system) · [Measurements](#measurements)")
	if r.Live != nil {
		live := shareableData(*r.Live)
		r.Live = &live
		writeLiveSummary(b, live)
	} else {
		b.line("")
		b.line("This report uses saved history only. Open Performance in Crude to include current measurements.")
	}
	stalls := writeStalls(b, r)
	b.line("")
	b.line("## Build and system")
	b.line("")
	b.table([]string{"Item", "Value"}, [][]string{
		{"Crude", fmt.Sprintf("%s (commit %s, build %s)", r.Host.Version, shortID(r.Host.Commit), shortID(r.Host.Build))},
		{"Source", shortID(r.Host.Cohort)},
		{"System", fmt.Sprintf("%s, %d logical CPUs, Go parallelism %d, %s", r.Host.Platform, r.Host.CPUs, r.Host.Parallelism, r.Host.GoVersion)},
		{"Terminal", cmp.Or(r.Host.Terminal, "unknown")},
	})
	b.line("")
	b.line("## Measurements")
	if r.Live != nil {
		writeLive(b, *r.Live)
	}
	for _, h := range r.History {
		writeHistory(b, h)
	}
	if r.HistoryErr != "" {
		b.line("")
		b.line("> Some saved measurements could not be read.")
	}
	writeTraces(b, stalls)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeLiveSummary(b *reportWriter, d Data) {
	findings := Diagnose(d)
	severity, verdict := Verdict(findings)
	b.line("")
	b.line("## Summary")
	b.line("")
	b.line("**%s**", cell(verdict))
	if severity != SeverityOK {
		b.line("")
		for _, f := range findings {
			b.line("- %s %s: %s", severityMark(f.Severity), cell(f.Title), cell(f.Detail))
		}
	}
	b.line("")
	for _, p := range d.Processes() {
		s := d.Snapshot(p)
		b.line("- %s process (pid %d): %s window, sampled %s", p.Label(), s.PID,
			durationText(time.Duration(s.Seconds)*time.Second), s.Taken.Format(time.RFC3339))
		if s.EventsOverwritten > 0 {
			b.line("  Older events were overwritten before this snapshot.")
		}
	}
	if headlines := OverviewHeadlines(d); len(headlines) > 0 {
		b.line("")
		rows := make([][]string, 0, len(headlines))
		for _, h := range headlines {
			rows = append(rows, []string{h.Label, h.Value})
		}
		b.table([]string{"Reading", "Value"}, rows)
	}
}

func writeLive(b *reportWriter, d Data) {
	b.line("")
	b.line("### Current window")
	b.line("")
	b.line("Percentiles are estimates from sampled latency buckets. A blank or missing reading does not mean zero.")
	for _, group := range liveGroups(d) {
		b.line("")
		b.line("### %s", group)
		var latencies [][]string
		var levels [][]string
		var tables []tableBlock
		for _, p := range d.Processes() {
			s := d.Snapshot(p)
			for _, l := range s.Latencies {
				if l.Meta.Group != group || l.Window.Count == 0 {
					continue
				}
				w := l.Window
				latencies = append(latencies, []string{l.Meta.Title, p.Label(), FormatCount(float64(w.Count)),
					FormatMicros(w.P50), FormatMicros(w.P95), FormatMicros(w.P99), FormatMicros(w.Max)})
			}
			for _, c := range s.Counters {
				if c.Meta.Group == group && c.Window > 0 {
					levels = append(levels, []string{c.Meta.Title, p.Label(), FormatValue(float64(c.Window), c.Meta.Unit) + " in the window",
						FormatValue(float64(c.Total), c.Meta.Unit) + " in total"})
				}
			}
			for _, g := range s.Gauges {
				if g.Meta.Group == group && g.WindowObserved {
					levels = append(levels, []string{g.Meta.Title, p.Label(), FormatValue(float64(g.Current), g.Meta.Unit) + " now",
						FormatValue(float64(g.Peak), g.Meta.Unit) + " at most"})
				}
			}
			for _, t := range s.Tables {
				if t.Meta.Group == group && len(t.Rows) > 0 {
					tables = append(tables, tableBlock{title: t.Meta.Title + " · " + p.Label(), rows: DiagnosticRows(t), folded: t.Folded})
				}
			}
		}
		if len(latencies) > 0 {
			b.line("")
			b.table([]string{"Measure", "Process", "Count", "p50", "p95", "p99", "Max"}, latencies)
		}
		if len(levels) > 0 {
			b.line("")
			b.table([]string{"Measure", "Process", "Window", "Overall"}, levels)
		}
		for _, t := range tables {
			b.line("")
			b.line("%s:", t.title)
			b.line("")
			rows := make([][]string, 0, min(len(t.rows), 10))
			// Show both the largest cumulative costs and the worst individual calls.
			for _, row := range reportTableRows(t.rows) {
				rows = append(rows, []string{ShortKey(row.Key), FormatCount(float64(row.Count)), FormatMicros(row.Sum), FormatMicros(row.Max)})
			}
			b.table([]string{"Operation", "Calls", "Total time", "Max"}, rows)
			if len(rows) < len(t.rows) || t.folded > 0 {
				b.line("")
				b.line("Shows %d of %d retained operation rows, ordered by total time first. Additional names may be combined as Other.", len(rows), len(t.rows))
			}
		}
	}
	for _, view := range ResourceViews(d) {
		b.line("")
		b.line("### Resources · %s", view.Label)
		b.line("")
		rows := [][]string{}
		for _, h := range ResourceHeadlines(view) {
			rows = append(rows, []string{h.Label, h.Value})
		}
		for _, p := range view.Processes {
			rows = append(rows, []string{"Process " + DisplayProcessName(p.Name), processUsage(p)})
		}
		if disk := view.Disk; disk != nil && !disk.MeasuredAt.IsZero() {
			rows = append(rows, []string{"Saved data", FormatBytes(float64(disk.Total))},
				[]string{"Saved workspaces", fmt.Sprintf("%d, %s", disk.WorkspaceCount, FormatBytes(float64(disk.WorkspaceBytes)))})
		}
		b.table([]string{"Reading", "Value"}, rows)
	}
}

type tableBlock struct {
	title  string
	rows   []metrics.TableRow
	folded uint64
}

func reportTableRows(rows []metrics.TableRow) []metrics.TableRow {
	ordered := slices.Clone(rows)
	slices.SortStableFunc(ordered, func(a, b metrics.TableRow) int { return cmp.Compare(b.Sum, a.Sum) })
	if len(ordered) <= 10 {
		return ordered
	}
	out := append([]metrics.TableRow(nil), ordered[:7]...)
	ordered = ordered[7:]
	slices.SortStableFunc(ordered, func(a, b metrics.TableRow) int { return cmp.Compare(b.Max, a.Max) })
	return append(out, ordered[:3]...)
}

func processUsage(p ResourceProcess) string {
	memory, cpu := "memory unknown", "CPU unknown"
	if p.Memory >= 0 {
		memory = FormatBytes(float64(p.Memory))
	}
	if p.CPU >= 0 {
		cpu = fmt.Sprintf("CPU %.0f%%", p.CPU)
	}
	return memory + ", " + cpu
}

// liveGroups lists the groups that have a measurement, in report order.
func liveGroups(d Data) []string {
	seen := map[string]bool{}
	for _, p := range d.Processes() {
		s := d.Snapshot(p)
		for _, l := range s.Latencies {
			seen[l.Meta.Group] = seen[l.Meta.Group] || l.Window.Count > 0
		}
		for _, c := range s.Counters {
			seen[c.Meta.Group] = seen[c.Meta.Group] || c.Window > 0
		}
		for _, g := range s.Gauges {
			seen[g.Meta.Group] = seen[g.Meta.Group] || g.WindowObserved
		}
		for _, t := range s.Tables {
			seen[t.Meta.Group] = seen[t.Meta.Group] || len(t.Rows) > 0
		}
	}
	var groups []string
	for group, observed := range seen {
		if observed {
			groups = append(groups, group)
		}
	}
	slices.SortFunc(groups, func(a, b string) int { return cmp.Or(cmp.Compare(groupRank(a), groupRank(b)), strings.Compare(a, b)) })
	return groups
}

// The builds table compares these measures; each role's own first.
var buildMeasures = map[string][]string{
	"shell": {"ui.input", "ui.update", "ui.view", "render.flush"},
	"core":  {"core.actor", "storage.receipts", "suggest.first_byte", "files.watch"},
}

func writeHistory(b *reportWriter, h History) {
	role := historyRole(h.Role)
	b.line("")
	b.line("## %s · last %d hours", role, h.Hours)
	b.line("")
	if !h.Saved {
		b.line("No performance history is saved on this computer yet.")
		return
	}
	if len(h.Builds) == 0 {
		b.line("Nothing was saved in this period.")
		return
	}
	if h.Partial || h.StallsOmitted > 0 || h.EventsOverwritten {
		b.line("Saved coverage is partial. The archive can omit stall rows or lose older events when its buffers fill.")
		b.line("")
	}
	newest := h.Builds[0]
	b.line("Build %s (Crude %s), %s observed in %d %s, %s to %s UTC.", shortID(newest.Build), newest.Version,
		durationText(newest.Observed), newest.Processes, plural(newest.Processes, "process", "processes"),
		newest.First.UTC().Format("Jan 2 15:04"), newest.Last.UTC().Format("Jan 2 15:04"))
	b.line("Process time can overlap. These measurements are from this computer, not a controlled benchmark.")
	type row struct {
		group, title string
		kind         metrics.Kind
		cells        []string
	}
	var rows []row
	for name, r := range newest.Series {
		meta, ok := metrics.MetaOf(name)
		if !ok {
			meta = metrics.Meta{Name: name, Title: name, Group: "Other"}
		}
		var cells []string
		switch r.Kind {
		case metrics.KindLatency:
			cells = []string{meta.Title, FormatCount(float64(r.Count)), FormatMicros(r.Percentile(newest.Bounds, .5)),
				FormatMicros(r.Percentile(newest.Bounds, .95)), FormatMicros(r.Percentile(newest.Bounds, .99)), FormatMicros(r.Max)}
		case metrics.KindCounter:
			cells = []string{meta.Title, FormatValue(float64(r.Sum), r.Unit), FormatValue(float64(r.Max), r.Unit) + "/s"}
		case metrics.KindGauge:
			if r.Count == 0 {
				continue
			}
			cells = []string{meta.Title, FormatValue(float64(r.Sum)/float64(r.Count), r.Unit), FormatValue(float64(r.Max), r.Unit)}
		default:
			continue
		}
		rows = append(rows, row{meta.Group, meta.Title, r.Kind, cells})
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(cmp.Compare(groupRank(a.group), groupRank(b.group)), strings.Compare(a.group, b.group), cmp.Compare(a.kind, b.kind), strings.Compare(a.title, b.title))
	})
	for i := 0; i < len(rows); {
		group := rows[i].group
		b.line("")
		b.line("### %s · %s", group, role)
		for i < len(rows) && rows[i].group == group {
			kind := rows[i].kind
			var cells [][]string
			for ; i < len(rows) && rows[i].group == group && rows[i].kind == kind; i++ {
				cells = append(cells, rows[i].cells)
			}
			b.line("")
			switch kind {
			case metrics.KindLatency:
				b.table([]string{"Measure", "Count", "p50", "p95", "p99", "Max"}, cells)
			case metrics.KindCounter:
				b.table([]string{"Measure", "Total", "Peak rate"}, cells)
			case metrics.KindGauge:
				b.table([]string{"Measure", "Average", "Peak"}, cells)
			}
		}
	}
	if len(h.Builds) > 1 {
		measures := buildMeasures[h.Role]
		header := []string{"Build", "Version / system", "Observed"}
		for _, name := range measures {
			title := name
			if meta, ok := metrics.MetaOf(name); ok {
				title = meta.Title
			}
			header = append(header, title+" p95")
		}
		var cells [][]string
		for _, build := range h.Builds {
			r := []string{shortID(build.Build), strings.TrimSpace(build.Version + " " + build.Platform), durationText(build.Observed)}
			for _, name := range measures {
				value := "-"
				if p95, ok := build.Percentile(name, .95); ok {
					value = FormatMicros(p95)
				}
				r = append(r, value)
			}
			cells = append(cells, r)
		}
		b.line("")
		b.line("### Builds · %s", role)
		b.line("")
		b.table(header, cells)
	}
}

// reportStall is one stall from either the live window or the history.
type reportStall struct {
	at                     time.Time
	process, title, series string
	label, severity, group string
	build                  string
	micros                 int64
	stack                  []string
}

func writeStalls(b *reportWriter, r Report) []reportStall {
	var stalls []reportStall
	seen := map[string]bool{}
	key := func(s reportStall) string { return s.process + s.series + s.at.Format(time.RFC3339Nano) }
	if r.Live != nil {
		for _, e := range r.Live.Events() {
			s := reportStall{at: e.At, process: e.Process.Label(), title: e.Title, series: e.Series,
				group: e.Group, severity: e.Severity, label: e.Label, micros: e.Micros, stack: e.Stack}
			seen[key(s)] = true
			stalls = append(stalls, s)
		}
	}
	total := 0
	for _, h := range r.History {
		total += h.StallsTotal
		for _, saved := range h.Stalls {
			meta, ok := metrics.MetaOf(saved.Series)
			if !ok {
				meta = metrics.Meta{Title: saved.Series}
			}
			s := reportStall{at: saved.At, process: historyRole(h.Role), title: meta.Title, series: saved.Series,
				group: meta.Group, severity: saved.Severity, build: saved.Build, label: saved.Label, micros: saved.Micros, stack: saved.Stack}
			if !seen[key(s)] {
				seen[key(s)] = true
				stalls = append(stalls, s)
			}
		}
	}
	b.line("")
	b.line("## Stalls")
	b.line("")
	if len(stalls) == 0 {
		b.line("No stall was recorded in the available evidence.")
		return nil
	}
	slices.SortFunc(stalls, func(a, b reportStall) int { return b.at.Compare(a.at) })
	if total > 0 {
		b.line("%d saved in the history; the newest are listed. A stall is one measurement past its limit.", total)
		b.line("")
	}
	const timelineLimit = 40
	rows := make([][]string, 0, min(timelineLimit, len(stalls)))
	for _, s := range stalls[:min(timelineLimit, len(stalls))] {
		took := "—"
		if s.series != "" {
			took = FormatMicros(s.micros)
		}
		build := "live"
		if s.build != "" {
			build = shortID(s.build)
		}
		detail := s.title
		if s.label != "" {
			detail += ": " + ShortKey(s.label)
		}
		rows = append(rows, []string{s.at.UTC().Format("Jan 2 15:04:05"), s.process + " / " + build,
			cmp.Or(s.group, "Other"), eventSeverity(s.severity) + " · " + detail, took})
	}
	b.table([]string{"UTC", "Process / Build", "Area", "Event", "Took"}, rows)
	if len(stalls) > timelineLimit {
		b.line("")
		b.line("Showing the latest %d of %d available events.", timelineLimit, len(stalls))
	}
	return stalls
}

func writeTraces(b *reportWriter, stalls []reportStall) {
	shown := 0
	for _, s := range stalls {
		if len(s.stack) == 0 || shown == 10 {
			continue
		}
		if shown == 0 {
			b.line("")
			b.line("## Captured traces")
			b.line("")
			b.line("Up to ten stalls with stacks, newest first. A missing trace does not identify the cause.")
		}
		shown++
		b.line("")
		b.line("### %s · %s · %s · %s UTC", s.process, s.title, FormatMicros(s.micros), s.at.UTC().Format("Jan 2 15:04:05"))
		b.line("")
		b.line("```text")
		for _, frame := range s.stack {
			b.line("%s", strings.ReplaceAll(frame, "`", "'"))
		}
		b.line("```")
	}
}

// savedLabel keeps a stall's label only when its series names code-defined
// causes, the rule the saved history follows; other labels can name files.
func savedLabel(series, label string) string {
	if meta, ok := metrics.MetaOf(series); ok && meta.SaveLabels {
		return label
	}
	return ""
}

func historyRole(role string) string {
	if role == "core" {
		return "Core"
	}
	return "Shell"
}

func eventSeverity(severity string) string {
	switch severity {
	case "crit":
		return "Critical"
	case "warn":
		return "Warning"
	default:
		return "Recorded"
	}
}

// shareableData strips private inputs before deriving findings, not after
// formatting them. The dashboard still uses the original local snapshot.
func shareableData(d Data) Data {
	if d.CoreErr != "" {
		d.CoreErr = "No core sample was returned."
	}
	d.Shell = shareableSnapshot(d.Shell)
	if d.Core != nil {
		core := shareableSnapshot(*d.Core)
		d.Core = &core
	}
	return d
}

func shareableSnapshot(s metrics.Snapshot) metrics.Snapshot {
	s.Events = slices.Clone(s.Events)
	for i := range s.Events {
		e := &s.Events[i]
		e.Label, e.Detail = savedLabel(e.Series, e.Label), ""
	}
	s.Tables = slices.Clone(s.Tables)
	for i := range s.Tables {
		t := &s.Tables[i]
		if t.Meta.SaveLabels {
			continue
		}
		rows := slices.Clone(DiagnosticRows(*t))
		for j := range rows {
			rows[j].Key = fmt.Sprintf("Operation %d", j+1)
		}
		t.Rows, t.DiagnosticRows = rows, rows
	}
	if s.Resources != nil && s.Resources.Disk != nil && s.Resources.Disk.Err != "" {
		resources, disk := *s.Resources, *s.Resources.Disk
		disk.Err = "sample incomplete"
		resources.Disk = &disk
		s.Resources = &resources
	}
	return s
}

func severityMark(s Severity) string {
	switch s {
	case SeverityCrit:
		return "✕"
	case SeverityWarn:
		return "⚠"
	case SeverityUnknown:
		return "?"
	}
	return "✓"
}

func shortID(id string) string {
	if len(id) > 16 {
		return id[:16]
	}
	return cmp.Or(id, "unknown")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func durationText(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
}

// cell makes text safe inside a Markdown table cell or list item.
func cell(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.NewReplacer(`\`, `\\`, "|", `\|`, "<", `\<`, ">", `\>`, "[", `\[`, "]", `\]`, "`", "\\`").Replace(s)
}

type reportWriter struct{ strings.Builder }

func (w *reportWriter) line(format string, args ...any) {
	if len(args) == 0 {
		w.WriteString(format)
	} else {
		fmt.Fprintf(&w.Builder, format, args...)
	}
	w.WriteByte('\n')
}

func (w *reportWriter) table(header []string, rows [][]string) {
	w.WriteString("| " + strings.Join(header, " | ") + " |\n|")
	for range header {
		w.WriteString("---|")
	}
	w.WriteByte('\n')
	for _, row := range rows {
		cells := make([]string, len(header))
		for i := range cells {
			if i < len(row) {
				cells[i] = cell(row[i])
			}
		}
		w.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
}
