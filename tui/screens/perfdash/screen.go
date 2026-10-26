// Package perfdash is the Performance surface: live charts, ranked owners
// and plain-language findings for the shell and core processes. It reads
// metrics snapshots handed to it by the viewmodel and performs no I/O.
package perfdash

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/metrics"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "perfdash"
	TabsID      ui.ComponentID = "perfdash/tabs"
	BodyID      ui.ComponentID = "perfdash/body"
	ScrollbarID ui.ComponentID = "perfdash/scrollbar"
	WindowID    ui.ComponentID = "perfdash/window"
	PauseID     ui.ComponentID = "perfdash/pause"
	CopyID      ui.ComponentID = "perfdash/copy"
)

// Actions. ActionRefresh and ActionCopy bubble to the viewmodel.
const (
	ActionSection = "perfdash.section"
	ActionWindow  = "perfdash.window"
	ActionPause   = "perfdash.pause"
	// ActionCopy carries the plain-text report as its payload.
	ActionCopy = "perfdash.copy"
	// ActionRefresh asks the owner to fetch fresh snapshots now.
	ActionRefresh = "perfdash.refresh"
)

// windows are the selectable history spans in seconds.
var windows = []int{60, 120, 300}

type Screen struct {
	ui.Base
	tokens    *ui.TokenSet
	tabs      *ui.Tabs
	body      *body
	scrollbar *ui.ScrollbarControl
	window    *ui.Button
	pause     *ui.Button
	copy      *ui.Button

	section         string
	windowIndex     int
	paused          bool
	data            Data
	hasData         bool
	findings        []Finding
	dockHeaderInset int
	buttonsOnStrip  bool
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: tokens, section: SectionOverview, windowIndex: 1}
	s.tabs = ui.NewTabs(TabsID, tokens)
	s.tabs.SetChrome(ui.TabsShell)
	s.syncTabs()
	s.body = newBody(s)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.body.scroll, tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorBg)
	s.window = s.newButton(WindowID, "", ActionWindow)
	s.pause = s.newButton(PauseID, "Pause", ActionPause)
	s.copy = s.newButton(CopyID, "Copy report", ActionCopy)
	s.syncButtons()
	return s
}

func (s *Screen) newButton(id ui.ComponentID, label, action string) *ui.Button {
	button := ui.NewButton(id, label, action)
	button.SetTokens(s.tokens)
	button.SetVariant(ui.VariantSecondary)
	button.SetSurfaceBackground(s.tokens.Color(ui.ColorBg))
	return button
}

func (s *Screen) syncTabs() {
	items := make([]ui.TabItem, 0, len(sectionLabels))
	for _, section := range sectionLabels {
		label := section.label
		if section.id == SectionOverview && len(s.findings) > 0 {
			sev, _ := Verdict(s.findings)
			switch sev {
			case SeverityCrit:
				label = "✕ " + label
			case SeverityUnknown:
				label = "? " + label
			default:
				label = "⚠ " + label
			}
		}
		if section.id == SectionEvents && s.hasData {
			if n := len(s.data.events()); n > 0 {
				label = fmt.Sprintf("%s %d", label, n)
			}
		}
		items = append(items, ui.TabItem{ID: section.id, Label: label})
	}
	s.tabs.SetItems(items, s.section)
}

func (s *Screen) syncButtons() {
	s.window.SetLabel(fmt.Sprintf("Window %dm", windows[s.windowIndex]/60))
	if s.paused {
		s.pause.SetLabel("Resume")
	} else {
		s.pause.SetLabel("Pause")
	}
}

// SetData installs a refresh. A paused dashboard keeps its frozen data.
func (s *Screen) SetData(d Data) {
	if s.paused {
		return
	}
	s.data, s.hasData = d, true
	s.findings = Diagnose(d)
	s.syncTabs()
	s.body.rebuild()
}

// Paused reports whether refreshes are suspended.
func (s *Screen) Paused() bool { return s.paused }

// Query is the snapshot window and resolution the current layout can show.
func (s *Screen) Query() metrics.Query {
	return metrics.Query{Seconds: windows[s.windowIndex], Points: min(windows[s.windowIndex], s.body.chartPoints())}
}

// Section is the active tab.
func (s *Screen) Section() string { return s.section }

// Findings returns the current diagnosis, worst first.
func (s *Screen) Findings() []Finding { return s.findings }

// SetSection activates a tab.
func (s *Screen) SetSection(section string) {
	for _, known := range sectionLabels {
		if known.id == section {
			s.section = section
			s.body.scroll.ScrollTo(0)
			s.syncTabs()
			s.body.rebuild()
			return
		}
	}
}

func (s *Screen) SetDockHeaderInset(width int) { s.dockHeaderInset = max(0, width) }

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.tabs.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	for _, button := range []*ui.Button{s.window, s.pause, s.copy} {
		button.SetTokens(tokens)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) PreferredFocus() ui.ComponentID { return BodyID }

func (s *Screen) Children() []ui.Component {
	kids := []ui.Component{s.tabs, s.body, s.scrollbar}
	if s.window.Bounds().W > 0 {
		kids = append(kids, s.window, s.pause, s.copy)
	}
	return kids
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	width, height := max(0, r.W), max(0, r.H)
	pad := min(s.tokens.PadLR(), width/8)
	stripW := max(0, width-s.dockHeaderInset)
	s.tabs.Layout(ctx, ui.Rect{X: 0, Y: 0, W: stripW, H: min(ui.StripHeight, height)})
	// Buttons sit after the tab labels when they fit, else on their own row.
	buttons := []*ui.Button{s.window, s.pause, s.copy}
	buttonsW := 0
	for _, button := range buttons {
		buttonsW += button.Measure(ctx, ui.Loose(width, 1)).W + 1
	}
	top := min(ui.StripHeight, height)
	labelsEnd := s.tabs.LabelRowEnd()
	s.buttonsOnStrip = labelsEnd+2+buttonsW <= stripW
	bx, by := stripW-buttonsW, 0
	if !s.buttonsOnStrip {
		bx, by = pad, top
		if height-top < 4 || buttonsW > width-pad {
			bx = -1 // too small: the keyboard shortcuts remain
		} else {
			top++
		}
	}
	for _, button := range buttons {
		w := button.Measure(ctx, ui.Loose(width, 1)).W
		if bx < 0 {
			button.Layout(ctx, ui.Rect{})
			continue
		}
		button.Layout(ctx, ui.Rect{X: bx, Y: by, W: w, H: 1})
		bx += w + 1
	}
	bodyH := max(0, height-top)
	contentW := max(0, width-2*pad-1)
	s.body.Layout(ctx, ui.Rect{X: pad, Y: top, W: contentW, H: bodyH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, width-1), Y: top, W: 1, H: bodyH})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	ui.FillRect(buf, ox, oy, s.Bounds(), cell.BlankCell(s.tokens.Color(ui.ColorBg)))
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch {
	case evt.Key.Code == tea.KeyTab:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: BodyID}}
	case evt.Key.Text == "p":
		return s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionPause})
	case evt.Key.Text == "w":
		return s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionWindow})
	case evt.Key.Text == "c":
		return s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCopy})
	case evt.Key.Code == tea.KeyLeft || evt.Key.Code == tea.KeyRight:
		delta := 1
		if evt.Key.Code == tea.KeyLeft {
			delta = -1
		}
		for i, section := range sectionLabels {
			if section.id == s.section {
				next := sectionLabels[(i+delta+len(sectionLabels))%len(sectionLabels)].id
				return s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionSection, Payload: next})
			}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate", ActionSection:
		section, _ := action.Payload.(string)
		if section == "cards" {
			return ui.EventResult{Handled: true}
		}
		s.SetSection(section)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "tabs.enter":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: BodyID}}
	case ActionWindow:
		s.windowIndex = (s.windowIndex + 1) % len(windows)
		s.syncButtons()
		if s.paused {
			s.paused = false
			s.syncButtons()
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionRefresh}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case ActionPause:
		s.paused = !s.paused
		s.syncButtons()
		result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		if !s.paused {
			result.Action = &ui.Action{ID: ActionRefresh}
		}
		return result
	case ActionCopy:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionCopy, Payload: s.Report()}}
	}
	return ui.EventResult{Action: &action}
}

// statusLine is the verdict row's right-aligned freshness note.
func (s *Screen) statusLine() string {
	window := fmt.Sprintf("%dm window", windows[s.windowIndex]/60)
	if s.paused {
		return "paused at " + s.data.Fetched.Format("15:04:05") + " · " + window
	}
	return window
}

// Report renders the diagnosis and headline numbers as plain text for the
// clipboard (and for agents asked to investigate).
func (s *Screen) Report() string {
	if !s.hasData {
		return "Crude performance: no measurements yet."
	}
	return Report(s.data)
}

// Report renders a plain-text summary of d.
func Report(d Data) string {
	var b strings.Builder
	findings := Diagnose(d)
	_, verdict := Verdict(findings)
	fmt.Fprintf(&b, "Crude performance report — %s (%s)\n", d.Fetched.Format(time.DateTime), windowLabel(d))
	fmt.Fprintf(&b, "Verdict: %s\n", verdict)
	for _, f := range findings {
		mark := "warn"
		if f.Severity == SeverityUnknown {
			mark = "unknown"
		}
		if f.Severity == SeverityCrit {
			mark = "CRIT"
		}
		fmt.Fprintf(&b, "  [%s] %s — %s\n", mark, f.Title, f.Detail)
	}
	for _, t := range overviewTiles(d) {
		fmt.Fprintf(&b, "%s: %s\n", t.label, t.value)
	}
	for _, view := range resources(d) {
		b.WriteString("\nResources\n")
		for _, t := range resourceTiles(view) {
			fmt.Fprintf(&b, "  %s: %s\n", t.label, t.value)
		}
		for _, row := range processRows(view) {
			fmt.Fprintf(&b, "  %-20s %-36s %10s cpu %s\n", row.label, row.detail, row.value, row.extra)
		}
		if disk := view.Disk; disk != nil {
			fmt.Fprintf(&b, "  %s\n", diskHeading(disk, d.Fetched))
			for _, row := range diskPartRows(disk) {
				fmt.Fprintf(&b, "    %-34s %10s\n", row.label, row.value)
			}
			for _, row := range workspaceRows(disk) {
				fmt.Fprintf(&b, "    %-34s %10s  %s\n", row.label, row.value, row.detail)
			}
			if note := orphanNote(disk); note != "" {
				fmt.Fprintf(&b, "  %s\n", note)
			}
		}
	}
	for _, p := range d.processes() {
		snapshot := d.snapshot(p)
		fmt.Fprintf(&b, "\n%s process (pid %d)\n", p.Label(), snapshot.PID)
		for _, series := range snapshot.Latencies {
			if series.Window.Count == 0 {
				continue
			}
			w := series.Window
			fmt.Fprintf(&b, "  %-22s p50 %-7s p95 %-7s p99 %-7s max %-7s n=%d\n", series.Meta.Name,
				formatMicros(w.P50), formatMicros(w.P95), formatMicros(w.P99), formatMicros(w.Max), w.Count)
		}
		for _, table := range snapshot.Tables {
			fmt.Fprintf(&b, "  %s:\n", table.Meta.Name)
			for i, row := range table.Rows {
				if i == 6 {
					break
				}
				fmt.Fprintf(&b, "    %-40s %-7s n=%-6d max %s\n", shortKey(row.Key), formatMicros(row.Sum), row.Count, formatMicros(row.Max))
			}
		}
	}
	if events := d.events(); len(events) > 0 {
		b.WriteString("\nEvents (newest first)\n")
		for i, event := range events {
			if i == 20 {
				break
			}
			fmt.Fprintf(&b, "  %s %s %s %s %s %s\n", event.At.Format("15:04:05"), event.Process.Label(), event.Title,
				formatMicros(event.Micros), shortKey(event.Label), event.Detail)
		}
	}
	return b.String()
}

// ClearHover drops a stale hover readout.
func (s *Screen) ClearHover() bool { return s.body.clearHover() }
