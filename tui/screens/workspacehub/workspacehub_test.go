package workspacehub

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/problems"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
	"crude/workspace"
)

func paint(t *testing.T, component ui.Component, w, h int) string {
	t.Helper()
	host := ui.NewHost(theme.DefaultDark(), component)
	host.Resize(w, h)
	host.MarkLayout()
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := range h {
		for x := range w {
			if c := buf.Get(x, y); c.Char != 0 {
				text.WriteRune(c.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func usageReport(now time.Time) workspace.UsageReport {
	days := make([]workspace.DayCost, workspace.UsageReportDays)
	for i := range days {
		day := now.AddDate(0, 0, i-len(days)+1)
		days[i] = workspace.DayCost{Day: day.Format("2006-01-02")}
	}
	days[len(days)-1] = workspace.DayCost{Day: days[len(days)-1].Day, Requests: 40, CostUSD: 12.5}
	days[len(days)-3] = workspace.DayCost{Day: days[len(days)-3].Day, Requests: 10, CostUSD: 2.5}
	return workspace.UsageReport{
		Requests: 50, CostUSD: 15, UnknownCost: 2, First: now.AddDate(0, 0, -2).UnixNano(), Last: now.UnixNano(),
		Models: []workspace.ModelCost{
			{ModelID: "claude-opus-5-5", ProviderID: "proxy", Requests: 30, InputTokens: 4_200_000, CachedTokens: 3_900_000, OutputTokens: 52_000, CostUSD: 12},
			{ModelID: "deepseek-v4.1-flash", Requests: 20, InputTokens: 900_000, OutputTokens: 31_000, CostUSD: 3},
		},
		Days:     days,
		Sessions: []workspace.SessionCost{{SessionID: "a", Requests: 50, CostUSD: 15}},
	}
}

// Overview shows the cost of this session, today, the last seven days and
// the workspace, a daily chart and a table by model with lined-up columns.
func TestOverviewShowsCostTilesChartAndModels(t *testing.T) {
	now := time.Now()
	overview := NewOverview(ui.NewTokenSet(theme.DefaultDark()))
	overview.now = func() time.Time { return now }
	overview.SetData(OverviewData{Report: usageReport(now), Session: SessionSpend{CostUSD: 167.11, Known: true, Requests: 1110}, Read: now})
	screen := paint(t, overview, 140, 40)
	for _, want := range []string{"This session", "$167.11", "1,110 requests", "Today", "$12.50", "Last 7 days", "$15.00", "This workspace", "Cost per day", "highest $12.50", "today", "By model", "claude-opus-5-5", "4.2M", "3.9M", "$12.00", "80%", "2 requests had no known price"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("Overview is missing %q:\n%s", want, screen)
		}
	}
	if !strings.ContainsAny(screen, "▁▂▃▄▅▆▇█") {
		t.Fatalf("the daily chart has no bars:\n%s", screen)
	}
	// The narrow layout keeps every number and drops columns, not models.
	narrow := paint(t, overview, 62, 40)
	for _, want := range []string{"$167.11", "claude-opus-5-5", "$12.00"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow Overview is missing %q:\n%s", want, narrow)
		}
	}
}

// A refresh keeps the totals and the reader's place: the page does not
// collapse to its loading message or jump back to its top while the core
// reads, and its heading says it is updating.
func TestOverviewRefreshKeepsTotalsAndScroll(t *testing.T) {
	now := time.Now()
	overview := NewOverview(ui.NewTokenSet(theme.DefaultDark()))
	overview.now = func() time.Time { return now }
	data := OverviewData{Report: usageReport(now), Read: now}
	overview.SetData(data)
	host := ui.NewHost(theme.DefaultDark(), overview)
	host.Resize(100, 12)
	host.MarkLayout()
	host.Layout()
	for range 2 {
		host.Deliver(OverviewID, tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	}
	scrolled := overview.scroll.model.Offset()
	if scrolled == 0 {
		t.Fatal("the Overview did not scroll")
	}
	data.Loading = true
	overview.SetData(data)
	screen := paint(t, overview, 100, 12)
	if overview.scroll.model.Offset() != scrolled {
		t.Fatalf("a refresh moved the page from row %d to row %d", scrolled, overview.scroll.model.Offset())
	}
	if strings.Contains(screen, "Reading the cost") || !strings.ContainsAny(screen, "▁▂▃▄▅▆▇█") {
		t.Fatalf("a refresh replaced the totals with the loading message:\n%s", screen)
	}
	overview.scroll.model.ScrollTo(0)
	if top := paint(t, overview, 100, 12); !strings.Contains(top, "· updating…") {
		t.Fatalf("the heading does not say the totals are updating:\n%s", top)
	}
	overview.scroll.model.ScrollTo(scrolled)
	data.Loading = false
	overview.SetData(data)
	paint(t, overview, 100, 12)
	if overview.scroll.model.Offset() != scrolled {
		t.Fatalf("the refreshed totals moved the page from row %d to row %d", scrolled, overview.scroll.model.Offset())
	}
}

// The workspace tile counts sessions, else requests, never "0 sessions",
// and drops the start date before it truncates.
func TestOverviewWorkspaceTileFitsItsDetail(t *testing.T) {
	now := time.Now()
	overview := NewOverview(ui.NewTokenSet(theme.DefaultDark()))
	overview.now = func() time.Time { return now }
	report := usageReport(now)
	since := "since " + Day(time.Unix(0, report.First), now)
	overview.SetData(OverviewData{Report: report, Read: now})
	if wide := paint(t, overview, 140, 40); !strings.Contains(wide, "1 session "+since) {
		t.Fatalf("wide workspace tile lacks its sessions and start:\n%s", wide)
	}
	if narrow := paint(t, overview, 84, 40); !strings.Contains(narrow, "1 session") || strings.Contains(narrow, "…") {
		t.Fatalf("narrow workspace tile must drop the date, not truncate:\n%s", narrow)
	}
	report.Sessions = nil
	overview.SetData(OverviewData{Report: report, Read: now})
	if screen := paint(t, overview, 140, 40); strings.Contains(screen, "0 sessions") || !strings.Contains(screen, "50 requests "+since) {
		t.Fatalf("a report without sessions counts requests:\n%s", screen)
	}
}

func TestOverviewSaysWhyTotalsAreMissing(t *testing.T) {
	overview := NewOverview(ui.NewTokenSet(theme.DefaultDark()))
	if screen := paint(t, overview, 100, 12); !strings.Contains(screen, "Reading the cost of every session") {
		t.Fatalf("loading:\n%s", screen)
	}
	overview.SetData(OverviewData{Problem: "Crude cannot read the cost of this workspace: disk is busy"})
	if screen := paint(t, overview, 100, 12); !strings.Contains(screen, "disk is busy") || strings.Contains(screen, "$0.00") {
		t.Fatalf("a failed read must not look like zero cost:\n%s", screen)
	}
}

// Storage names what holds the space, with sizes, dates and only the
// cleanups that can run now.
func TestStorageShowsCategoriesAndCleanups(t *testing.T) {
	now := time.Now()
	storage := NewStorage(ui.NewTokenSet(theme.DefaultDark()))
	storage.now = func() time.Time { return now }
	storage.SetData(StorageData{Read: now, Report: workspace.StorageReport{Bytes: 5 << 30, Categories: []workspace.StorageCategory{
		{ID: workspace.StorageSessions, Bytes: 3 << 30, Items: 72, Oldest: now.AddDate(0, -2, 0).UnixNano(), Newest: now.UnixNano()},
		{ID: workspace.StorageSubagents, Bytes: 500 << 20, Items: 140},
		{ID: workspace.StorageTemporary, Bytes: 226 << 20, Items: 50, Reclaimable: 200 << 20},
		{ID: workspace.StorageHistory, Bytes: 213 << 20},
		{ID: workspace.StorageWorktrees, Bytes: 1 << 30, Items: 2},
		{ID: workspace.StorageIndex, Bytes: 75 << 20},
		{ID: workspace.StorageOther},
	}}})
	screen := paint(t, storage, 140, 30)
	for _, want := range []string{"Crude keeps 5 GB for this workspace", "Sessions", "3 GB", "72 sessions", "Temporary files", "200 MB in closed sessions can be removed", "Remove…", "Local History", "Clean up…", "Worktrees", "Remove finished…", "Workspace index", "Measure again"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("Storage is missing %q:\n%s", want, screen)
		}
	}
	if bar := strings.Split(screen, "\n")[3]; !strings.Contains(bar, "█") || strings.Contains(bar, "·") {
		t.Fatalf("the size bar does not fill its row:\n%s", screen)
	}
	result := storage.HandleAction(ui.EventContext{}, ui.Action{ID: ActionCleanup + ":" + workspace.StorageTemporary})
	if result.Action == nil || result.Action.ID != ActionCleanup || result.Action.Payload != workspace.StorageTemporary {
		t.Fatalf("cleanup intent = %+v", result.Action)
	}
	// While a cleanup runs, every action waits.
	data := storage.Data()
	data.Busy = workspace.StorageTemporary
	storage.SetData(data)
	if !strings.Contains(paint(t, storage, 140, 30), "Working…") {
		t.Fatal("a running cleanup is not shown")
	}
}

// The strip switches pages and reports the shown page.
func TestHubSwitchesPagesFromItsStrip(t *testing.T) {
	tokens := ui.NewTokenSet(theme.DefaultDark())
	overview, storage := NewOverview(tokens), NewStorage(tokens)
	hub := New(tokens, map[Tab]ui.Component{TabOverview: overview, TabTabs: ui.NewButton("tabs-page", "Tabs page", "noop"), TabStorage: storage})
	hub.SetActive(TabOverview)
	screen := paint(t, hub, 140, 20)
	for _, want := range []string{"Overview", "Tabs", "Sessions", "Local History", "Storage", "Cost and use"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("hub is missing %q:\n%s", want, screen)
		}
	}
	result := hub.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: string(TabStorage)})
	if hub.Active() != TabStorage || result.Action == nil || result.Action.ID != ActionTab || result.Action.Payload != TabStorage {
		t.Fatalf("switch result = %+v, active %s", result.Action, hub.Active())
	}
	if screen = paint(t, hub, 140, 20); !strings.Contains(screen, "Measuring the saved data") {
		t.Fatalf("Storage page:\n%s", screen)
	}
	if narrow := paint(t, hub, 60, 20); !strings.Contains(narrow, "Overview") || !strings.Contains(narrow, "Measuring") {
		t.Fatalf("the strip or page is lost at 60 columns:\n%s", narrow)
	}
}

// Open workspace Errand sits at the strip's right end on every tab. Pointer,
// keyboard and semantic control all ask for the same action, and a narrow
// panel keeps the short label.
func TestHubErrandLinkOnEveryTab(t *testing.T) {
	tokens := ui.NewTokenSet(theme.DefaultDark())
	hub := New(tokens, map[Tab]ui.Component{TabOverview: NewOverview(tokens), TabTabs: ui.NewButton("tabs-page", "Tabs page", "noop"), TabStorage: NewStorage(tokens)})
	host := ui.NewHost(theme.DefaultDark(), hub)
	hub.SetFocusHandler(host.Focus, host.Focused)
	for _, tab := range []Tab{TabOverview, TabStorage} {
		hub.SetActive(tab)
		screen := paint(t, hub, 140, 20)
		if first := strings.Split(screen, "\n")[0]; !strings.HasSuffix(strings.TrimRight(first, " "), "Open workspace Errand") {
			t.Fatalf("%s: the link is not at the strip's right end:\n%s", tab, screen)
		}
	}
	host.Resize(140, 20)
	host.Layout()
	link, ok := host.ScreenRect(ErrandID)
	if !ok || link.Empty() || link.Y != 0 {
		t.Fatalf("link bounds = %+v", link)
	}
	result := host.Handle(tea.MouseClickMsg{X: link.X + 1, Y: link.Y, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.ID != ActionErrand {
		t.Fatalf("click result = %+v", result.Action)
	}
	// Tab goes from the strip through the page to the link, then back to
	// the strip.
	host.Focus(StripID)
	for range 12 {
		if host.Focused() == ErrandID {
			break
		}
		host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if host.Focused() != ErrandID {
		t.Fatalf("Tab does not reach the link; focus is on %q", host.Focused())
	}
	if result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action == nil || result.Action.ID != ActionErrand {
		t.Fatalf("Enter result = %+v", result.Action)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if host.Focused() != StripID {
		t.Fatalf("Tab from the link focused %q", host.Focused())
	}
	page, err := host.InspectControls(control.Target{WorkspaceID: "workspace"}, control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range page.Nodes {
		if node.ID == string(ErrandID) {
			found = true
			result, err := host.Control(ErrandID, node.OpenEpoch, &node.Revision, ui.ControlOperation{Verb: "activate"})
			if err != nil || result.Action == nil || result.Action.ID != ActionErrand {
				t.Fatal("semantic activation differs from the link", err, result.Action)
			}
		}
	}
	if !found {
		t.Fatal("the link is absent from semantic control inspection")
	}
	if narrow := strings.Split(paint(t, hub, 70, 20), "\n")[0]; !strings.HasSuffix(strings.TrimRight(narrow, " "), "Errand") || strings.Contains(narrow, "Open workspace") {
		t.Fatalf("narrow strip row: %q", narrow)
	}
}

// The Errand link has the same underlined link cells as Problems' Refresh,
// in both themes and at narrow and wide widths.
func TestHubErrandLinkMatchesProblems(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{34, 80, 160} {
			tokens := ui.NewTokenSet(th)
			hub := New(tokens, map[Tab]ui.Component{TabOverview: NewOverview(tokens), TabTabs: NewStorage(tokens)})
			host := ui.NewHost(th, hub)
			host.Resize(width, 10)
			buf := surface.NewCellBuffer(width, 10)
			host.Paint(buf)
			link, ok := host.ScreenRect(ErrandID)
			if !ok || link.Empty() {
				t.Fatalf("width %d: the link is not painted", width)
			}
			got := buf.Get(link.X+1, link.Y)
			p := problems.New(th)
			p.SetModel(problems.Model{Refresh: true})
			pHost := ui.NewHost(th, p)
			pHost.Resize(width, 10)
			pBuf := surface.NewCellBuffer(width, 10)
			pHost.Paint(pBuf)
			var reference cell.Cell
			for y := range pBuf.Height() {
				for x := 0; x+len("Refresh") <= pBuf.Width(); x++ {
					match := true
					for i, char := range "Refresh" {
						match = match && pBuf.Get(x+i, y).Char == char
					}
					if match {
						reference = pBuf.Get(x, y)
					}
				}
			}
			if reference.Char != 'R' || !reference.Attr.Has(cell.AttrUnderline) {
				t.Fatal("Problems does not provide the expected underlined link")
			}
			if got.Attr != reference.Attr || !got.FG.Equal(reference.FG) || !got.BG.Equal(reference.BG) {
				t.Fatalf("width %d: Errand and Problems link cells differ: %+v vs %+v", width, got, reference)
			}
		}
	}
}
