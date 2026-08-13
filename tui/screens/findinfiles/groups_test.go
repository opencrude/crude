package findinfiles

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func groupedFixtureResults() []fileio.ProjectSearchMatch {
	aContext := "needle first\nmid one\nmid two\nmid three\nmid four\n  needle second"
	return []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 4, Column: 0, EndColumn: 6,
			LineText: "needle first", TextMatches: []int{0, 1, 2, 3, 4, 5},
			Context: aContext, ContextStart: 4},
		{Path: "/root/a.go", RelativePath: "a.go", Line: 9, Column: 2, EndColumn: 8,
			LineText: "  needle second", TextMatches: []int{2, 3, 4, 5, 6, 7},
			Context: aContext, ContextStart: 4},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, Column: 0, EndColumn: 6,
			LineText: "needle beta", TextMatches: []int{0, 1, 2, 3, 4, 5},
			Context: "needle beta", ContextStart: 2},
	}
}

func groupedHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	screen, host := searchHost(t)
	screen.SetQuery("needle")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, groupedFixtureResults(), nil, time.Millisecond)
	return screen, host
}

func TestGroupedResultsBuildHeadersWithCounts(t *testing.T) {
	screen, host := groupedHost(t)

	// a.go (2 matches) + b.go (1 match) → 2 headers + 3 match rows.
	if screen.DisplayRowCount() != 5 {
		t.Fatalf("display rows=%d want 5", screen.DisplayRowCount())
	}
	if screen.Selected() != 0 || screen.SelectedRow() != 1 || screen.SelectedRowIsHeader() {
		t.Fatalf("initial selection=%d row=%d header=%v", screen.Selected(), screen.SelectedRow(), screen.SelectedRowIsHeader())
	}

	host.Layout()
	buf := surface.NewCellBuffer(80, 24)
	host.Paint(buf)
	painted := bufferText(buf)
	if !strings.Contains(painted, "a.go") || !strings.Contains(painted, "b.go") {
		t.Fatalf("headers missing:\n%s", painted)
	}
	aRow := ""
	for _, row := range strings.Split(painted, "\n") {
		if strings.Contains(row, "a.go") && !strings.Contains(row, ":") {
			aRow = row
			break
		}
	}
	if aRow == "" {
		t.Fatalf("no a.go header row painted:\n%s", painted)
	}
	if !strings.Contains(aRow, "2") {
		t.Fatalf("a.go header should show match count 2: %q", aRow)
	}
	if !strings.Contains(painted, "4: needle first") {
		t.Fatalf("match rows should show line numbers and text:\n%s", painted)
	}
	if !strings.Contains(painted, "9: needle second") {
		t.Fatalf("indented match text should be trimmed:\n%s", painted)
	}
}

func TestGroupCollapseExpandWithKeyboard(t *testing.T) {
	screen, host := groupedHost(t)
	host.Focus(ListID)

	// Left on a match row jumps to its group header.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !screen.SelectedRowIsHeader() || screen.SelectedRow() != 0 {
		t.Fatalf("Left on match should land on header, row=%d header=%v", screen.SelectedRow(), screen.SelectedRowIsHeader())
	}

	// Left on the header collapses the group: its two match rows disappear.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !screen.GroupCollapsed("/root/a.go") {
		t.Fatal("Left on header should collapse the group")
	}
	if screen.DisplayRowCount() != 3 {
		t.Fatalf("collapsed rows=%d want 3 (header + b.go header + b.go match)", screen.DisplayRowCount())
	}

	// Down from the collapsed header goes straight to b.go's header.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if !screen.SelectedRowIsHeader() || screen.Selected() != 2 {
		t.Fatalf("Down should skip collapsed matches, selection=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}

	// Back up and expand with Right.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.GroupCollapsed("/root/a.go") {
		t.Fatal("Right on collapsed header should expand the group")
	}
	if screen.DisplayRowCount() != 5 {
		t.Fatalf("expanded rows=%d want 5", screen.DisplayRowCount())
	}

	// Right on the expanded header steps into the first match row.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if screen.SelectedRowIsHeader() || screen.Selected() != 0 {
		t.Fatalf("Right on expanded header should enter first match, selection=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}

	// Enter on a header toggles collapse instead of opening.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd != nil {
		t.Fatal("Enter on header must not emit an open command")
	}
	if !screen.GroupCollapsed("/root/a.go") {
		t.Fatal("Enter on header should collapse the group")
	}
}

func TestGroupHeaderClickToggles(t *testing.T) {
	screen, host := groupedHost(t)

	// List starts at Y=2; the a.go header is its first row.
	res := host.Handle(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if res.Cmd != nil {
		t.Fatal("header click must not open a result")
	}
	if !screen.GroupCollapsed("/root/a.go") {
		t.Fatal("header click should collapse the group")
	}
	if screen.DisplayRowCount() != 3 {
		t.Fatalf("collapsed rows=%d want 3", screen.DisplayRowCount())
	}

	host.Handle(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if screen.GroupCollapsed("/root/a.go") {
		t.Fatal("second header click should expand the group")
	}
	if screen.DisplayRowCount() != 5 {
		t.Fatalf("expanded rows=%d want 5", screen.DisplayRowCount())
	}
}

func TestGroupedOpenKeepsWorkingAfterCollapseOfOtherGroup(t *testing.T) {
	screen, host := groupedHost(t)
	host.Focus(ListID)

	// Collapse a.go, then walk to b.go's match and open it.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.SelectedRowIsHeader() {
		t.Fatalf("expected match row, got header (row=%d)", screen.SelectedRow())
	}
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Cmd == nil {
		t.Fatal("Enter on match should open")
	}
	msg, ok := res.Cmd().(OpenMsg)
	if !ok || msg.Match.Path != "/root/b.go" {
		t.Fatalf("open message=%#v", msg)
	}
}

func TestPreviewHighlightsAllMatchesOnPreviewedLines(t *testing.T) {
	screen, _ := searchHost(t)
	screen.SetQuery("needle")
	screen.ScheduleSearch()
	context := "needle one\nplain line\nneedle two"
	results := []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, Column: 0, EndColumn: 6,
			LineText: "needle one", TextMatches: []int{0, 1, 2, 3, 4, 5},
			Context: context, ContextStart: 1},
		{Path: "/root/a.go", RelativePath: "a.go", Line: 3, Column: 0, EndColumn: 6,
			LineText: "needle two", TextMatches: []int{0, 1, 2, 3, 4, 5},
			Context: context, ContextStart: 1},
		{Path: "/root/other.go", RelativePath: "other.go", Line: 7, Column: 0, EndColumn: 6,
			LineText: "needle far", TextMatches: []int{0, 1, 2, 3, 4, 5},
			Context: "needle far", ContextStart: 7},
	}
	screen.ApplySearchResults(screen.generation, results, nil, time.Millisecond)

	if screen.previewSrc == nil {
		t.Fatal("preview should exist for the selected match")
	}
	st := screen.previewSrc.Search()
	if !st.Open {
		t.Fatal("preview search highlighting should be enabled")
	}
	if len(st.Matches) != 2 {
		t.Fatalf("preview matches=%d want 2 (both a.go hits inside the excerpt)", len(st.Matches))
	}
	if st.CurrentIdx != 0 {
		t.Fatalf("current match=%d want 0", st.CurrentIdx)
	}
	if st.Matches[0].Start.Line != 0 || st.Matches[1].Start.Line != 2 {
		t.Fatalf("match lines=%d,%d want 0,2", st.Matches[0].Start.Line, st.Matches[1].Start.Line)
	}
	if st.Matches[1].Start.Col != 0 || st.Matches[1].End.Col != 6 {
		t.Fatalf("second match cols=%d..%d want 0..6", st.Matches[1].Start.Col, st.Matches[1].End.Col)
	}
}

func TestPreviewNonSelectedMatchPaintsHighlightBg(t *testing.T) {
	screen, host := groupedHost(t)
	if screen.previewSrc == nil {
		t.Fatal("preview missing")
	}
	host.Layout()
	buf := surface.NewCellBuffer(80, 24)
	host.Paint(buf)

	th := theme.DefaultDark()
	selBg := rendering.ThemeColorToCell(th.Colors.Selection)
	// The selected match is a.go line 4 ("needle first"); a.go line 9
	// ("needle second") is inside the same context excerpt and must carry
	// the highlight bg on its matched cells.
	found := false
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x+5 < buf.Width(); x++ {
			if buf.Get(x, y).Char != 'n' || buf.Get(x+1, y).Char != 'e' {
				continue
			}
			if buf.Get(x+6, y).Char == ' ' && buf.Get(x+7, y).Char == 's' {
				if buf.Get(x, y).BG.Equal(selBg) {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("non-selected match on a previewed line should paint the selection-tint background")
	}
}

func TestQueryHistoryStoreDedupesAndCaps(t *testing.T) {
	var h queryHistoryStore
	h.Add("  ")
	if h.Len() != 0 {
		t.Fatal("blank queries must not be recorded")
	}
	h.Add("alpha")
	h.Add("beta")
	h.Add("alpha")
	if h.Len() != 2 || h.At(0) != "alpha" || h.At(1) != "beta" {
		t.Fatalf("dedupe/move-to-front broken: %v", h.entries)
	}
	for i := 0; i < queryHistoryLimit+5; i++ {
		h.Add("q" + itoa(i))
	}
	if h.Len() != queryHistoryLimit {
		t.Fatalf("history len=%d want %d", h.Len(), queryHistoryLimit)
	}
	if h.At(0) != "q"+itoa(queryHistoryLimit+4) {
		t.Fatalf("newest entry=%q", h.At(0))
	}
}

func TestQueryHistoryCyclesInQueryField(t *testing.T) {
	queryHistory = queryHistoryStore{}
	t.Cleanup(func() { queryHistory = queryHistoryStore{} })

	// Session 1: open a result (records "needle"), then close (records the
	// final query "needle beta").
	screen, host := searchHost(t)
	screen.SetQuery("needle")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{{
		Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "needle", NameOnly: true,
	}}, nil, time.Millisecond)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	screen.SetQuery("needle beta")
	screen.Close()
	if queryHistory.Len() != 2 {
		t.Fatalf("history entries=%d want 2 (%v)", queryHistory.Len(), queryHistory.entries)
	}

	// Session 2 (fresh screen): Ctrl+P walks newest→oldest, Ctrl+N returns
	// toward the draft.
	screen2, host2 := searchHost(t)
	host2.Handle(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := screen2.Query(); got != "needle beta" {
		t.Fatalf("first Ctrl+P query=%q want %q", got, "needle beta")
	}
	if !screen2.HistoryBrowsing() {
		t.Fatal("Ctrl+P should enter history browsing")
	}
	host2.Handle(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := screen2.Query(); got != "needle" {
		t.Fatalf("second Ctrl+P query=%q want %q", got, "needle")
	}
	host2.Handle(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := screen2.Query(); got != "needle" {
		t.Fatalf("Ctrl+P past oldest should clamp, query=%q", got)
	}
	host2.Handle(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if got := screen2.Query(); got != "needle beta" {
		t.Fatalf("Ctrl+N query=%q want %q", got, "needle beta")
	}
	host2.Handle(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if got := screen2.Query(); got != "" {
		t.Fatalf("Ctrl+N back to draft should restore empty draft, query=%q", got)
	}
	if screen2.HistoryBrowsing() {
		t.Fatal("returning to the draft should leave history browsing")
	}
}

func TestQueryHistoryUpOnEmptyFieldAndTypingExits(t *testing.T) {
	queryHistory = queryHistoryStore{}
	t.Cleanup(func() { queryHistory = queryHistoryStore{} })
	queryHistory.Add("stored query")

	screen, host := searchHost(t)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := screen.Query(); got != "stored query" {
		t.Fatalf("Up on empty field should browse history, query=%q", got)
	}
	if !screen.HistoryBrowsing() {
		t.Fatal("Up should enter history browsing")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := screen.Query(); got != "" {
		t.Fatalf("Down while browsing should restore the draft, query=%q", got)
	}

	// Typing exits browse mode.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	host.Handle(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if screen.HistoryBrowsing() {
		t.Fatal("typing must exit history browsing")
	}
	if got := screen.Query(); got != "stored queryz" {
		t.Fatalf("typing should edit the recalled query, query=%q", got)
	}
}
