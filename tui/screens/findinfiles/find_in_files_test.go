package findinfiles

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("needle")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "needle alpha", NameOnly: false},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, LineText: "needle beta", NameOnly: false},
	}, nil, 5*time.Millisecond)
	host.Layout()
	buf := surface.NewCellBuffer(80, 24)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("find in files paint produced no non-empty cells")
	}
	painted := bufferText(buf)
	for _, label := range []string{"needle", "a.go", "b.go", "matches"} {
		if !strings.Contains(painted, label) {
			t.Fatalf("missing FIF label %q:\n%s", label, painted)
		}
	}
	if !bufferHasNonDefaultFG(buf) {
		t.Fatal("find in files paint missing themed foreground")
	}

	// Byte-identical regression. Deterministic: the search root (TempDir) is
	// never rendered — only query, result paths, and status.
	uitest.CheckGolden(t, "findinfiles_dark_80x24", buf)
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func bufferHasNonDefaultFG(buf *surface.CellBuffer) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() {
				return true
			}
		}
	}
	return false
}

func TestFindInFilesFilterPaintsQueryAndStatusAcrossThemes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 14}, {W: 100, H: 24}} {
			screen := New(th)
			screen.Open(t.TempDir())
			component := ui.NewComponentWidget(th, screen, size.W, size.H)
			screen.SetFocusHandler(component.Focus)
			screen.FocusQuery()
			host := component.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			painted := bufferText(buf)
			if !strings.Contains(painted, "Search file names and contents") {
				t.Fatalf("theme=%v size=%#v missing placeholder:\n%s", th == theme.DefaultLight(), size, painted)
			}
			if !strings.Contains(painted, "Open") || !strings.Contains(painted, "Close") {
				t.Fatalf("theme=%v size=%#v missing action buttons:\n%s", th == theme.DefaultLight(), size, painted)
			}
		}
	}
}

func TestFindInFilesTypingSchedulesDebouncedSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("alpha")
	for _, r := range []string{"b", "e", "t", "a"} {
		host.Handle(tea.KeyPressMsg{Code: rune(r[0]), Text: r})
	}

	if got := screen.Query(); got != "alphabeta" {
		t.Fatalf("query=%q", got)
	}
	if screen.generation == 0 {
		t.Fatal("typing should bump generation")
	}
}

func TestFindInFilesNavigationClampsAndOpens(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	results := []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "alpha", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, LineText: "beta", NameOnly: true},
		{Path: "/root/c.go", RelativePath: "c.go", Line: 3, LineText: "gamma", NameOnly: true},
	}
	screen.ApplySearchResults(screen.generation, results, nil, 12*time.Millisecond)
	// Three files → header+match per file: 6 display rows, selection starts
	// on the first MATCH row (row 1), which is result 0.
	if screen.DisplayRowCount() != 6 {
		t.Fatalf("display rows=%d want 6", screen.DisplayRowCount())
	}
	if screen.Selected() != 0 || screen.SelectedRow() != 1 || screen.SelectedRowIsHeader() {
		t.Fatalf("initial selection=%d row=%d header=%v", screen.Selected(), screen.SelectedRow(), screen.SelectedRowIsHeader())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 1 || !screen.SelectedRowIsHeader() {
		t.Fatalf("down onto b.go header: selection=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 1 || screen.SelectedRowIsHeader() {
		t.Fatalf("down onto b.go match: selection=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}
	for i := 0; i < 3; i++ {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if screen.Selected() != 2 || screen.SelectedRow() != 5 {
		t.Fatalf("clamp at end: selection=%d row=%d", screen.Selected(), screen.SelectedRow())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if screen.Selected() != 2 || !screen.SelectedRowIsHeader() {
		t.Fatalf("up onto c.go header: selection=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	if screen.Selected() != 0 || screen.SelectedRow() != 0 {
		t.Fatalf("home=%d row=%d", screen.Selected(), screen.SelectedRow())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if screen.Selected() != 2 || screen.SelectedRow() != 5 {
		t.Fatalf("end=%d row=%d", screen.Selected(), screen.SelectedRow())
	}
}

func TestFindInFilesEscAndEnterEmitOwnerScreenedMessages(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{{
		Path: "/root/x.go", RelativePath: "x.go", Line: 5, LineText: "needle", NameOnly: true,
	}}, nil, 3*time.Millisecond)

	open := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if open.Cmd == nil {
		t.Fatal("Enter on result should emit a command")
	}
	if msg, ok := open.Cmd().(OpenMsg); !ok || msg.Match.Path != "/root/x.go" {
		t.Fatalf("open message=%#v", open)
	} else if msg.Screen != screen {
		t.Fatalf("open owner=%p, want %p", msg.Screen, screen)
	}

	close := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if close.Cmd == nil {
		t.Fatal("Esc should emit close command")
	}
	if msg, ok := close.Cmd().(CloseMsg); !ok || msg.Screen != screen {
		t.Fatalf("close message=%#v", close)
	}
}

func TestFindInFilesFirstQueryNavigationSynchronizesPreviewAndOpen(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "alpha", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 2, LineText: "beta", NameOnly: true},
	}, nil, time.Millisecond)

	// Rows: a.go header, a.go match, b.go header, b.go match. Stepping onto
	// b.go's header already selects (and previews) its first match.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 1 || !screen.SelectedRowIsHeader() {
		t.Fatalf("first down selected=%d header=%v, want result 1 on header", screen.Selected(), screen.SelectedRowIsHeader())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if screen.Selected() != 1 || screen.SelectedRowIsHeader() {
		t.Fatalf("second down selected=%d header=%v, want match row", screen.Selected(), screen.SelectedRowIsHeader())
	}

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("Enter should emit an open command")
	}
	msg, ok := result.Cmd().(OpenMsg)
	if !ok || msg.Match.Path != "/root/b.go" || msg.Screen != screen {
		t.Fatalf("open message=%#v", msg)
	}
}

func TestFindInFilesRegexShortcutReschedulesSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("needle")
	screen.queryField.field.SetDebounce(0)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})

	result := host.Handle(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !screen.Regex() {
		t.Fatal("Ctrl+R should enable regex")
	}
	if result.Cmd == nil {
		t.Fatal("Ctrl+R should reschedule search")
	}
	debounced, ok := result.Cmd().(ui.SearchDebouncedMsg)
	if !ok {
		t.Fatalf("Ctrl+R produced %T, want SearchDebouncedMsg", result.Cmd())
	}
	requestCmd := screen.HandleSearchDebounced(debounced)
	if requestCmd == nil {
		t.Fatal("accepted debounce should emit a search request")
	}
	request, ok := requestCmd().(SearchRequestMsg)
	if !ok || !request.Options.Regex || request.Screen != screen {
		t.Fatalf("search request=%#v", request)
	}
}

func TestFindInFilesCaseToggleReschedulesSearch(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("Needle")
	screen.queryField.field.SetDebounce(0)

	result := host.Handle(tea.MouseClickMsg{X: screen.caseToggle.Bounds().X, Y: screen.caseToggle.Bounds().Y, Button: tea.MouseLeft})
	if !screen.CaseSensitive() {
		t.Fatal("case-sensitive toggle did not change state")
	}
	if result.Cmd == nil {
		t.Fatal("case-sensitive toggle should reschedule search")
	}
	debounced, ok := result.Cmd().(ui.SearchDebouncedMsg)
	if !ok {
		t.Fatalf("toggle produced %T, want SearchDebouncedMsg", result.Cmd())
	}
	requestCmd := screen.HandleSearchDebounced(debounced)
	request, ok := requestCmd().(SearchRequestMsg)
	if !ok || !request.Options.CaseSensitive || request.Screen != screen {
		t.Fatalf("search request=%#v", request)
	}

	close := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if close.Cmd == nil {
		t.Fatal("Escape on an option toggle should close Find in Files")
	}
	if msg, ok := close.Cmd().(CloseMsg); !ok || msg.Screen != screen {
		t.Fatalf("close message=%#v", close)
	}
}

func TestFindInFilesDoubleClickActivatesAndSingleClickJustSelects(t *testing.T) {
	screen, host := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{{
		Path: "/root/z.go", RelativePath: "z.go", Line: 9, LineText: "zed", NameOnly: true,
	}}, nil, 1*time.Millisecond)

	// The list starts at Y=2: row 0 is the z.go group header, row 1 (Y=3)
	// is the match row.
	first := host.Handle(tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseLeft})
	if first.Cmd != nil {
		t.Fatalf("first click emitted command=%#v", first)
	}
	if screen.Selected() != 0 || screen.SelectedRowIsHeader() {
		t.Fatalf("first click selected=%d header=%v", screen.Selected(), screen.SelectedRowIsHeader())
	}
	second := host.Handle(tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseLeft})
	if second.Cmd == nil {
		t.Fatal("second click should emit a command")
	}
	if msg, ok := second.Cmd().(OpenMsg); !ok {
		t.Fatalf("double click message=%#v", msg)
	}
}

func TestFindInFilesOwnerScreeningRejectsStaleScreen(t *testing.T) {
	screen, host := searchHost(t)
	other := New(theme.DefaultDark())
	if screen.AcceptsSearch(other, 1) {
		t.Fatal("AcceptsSearch must reject a different screen")
	}
	screen.SetQuery("needle")
	if !screen.AcceptsSearch(screen, screen.generation) {
		t.Fatal("AcceptsSearch should accept matching screen+generation")
	}
	if screen.AcceptsSearch(screen, screen.generation+99) {
		t.Fatal("AcceptsSearch should reject stale generation")
	}
	screen.SetQuery("")
	if screen.AcceptsSearch(screen, screen.generation) {
		t.Fatal("AcceptsSearch should reject empty query")
	}
	_ = host
}

func TestFindInFilesStatusUpdatesFromResults(t *testing.T) {
	screen, _ := searchHost(t)
	screen.SetQuery("x")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.generation, []fileio.ProjectSearchMatch{
		{Path: "/root/a.go", RelativePath: "a.go", Line: 1, LineText: "a", NameOnly: true},
		{Path: "/root/a.go", RelativePath: "a.go", Line: 2, LineText: "b", NameOnly: true},
		{Path: "/root/b.go", RelativePath: "b.go", Line: 3, LineText: "c", NameOnly: true},
	}, nil, 7*time.Millisecond)
	if !strings.Contains(screen.status.Text(), "3 matches") {
		t.Fatalf("status=%q", screen.status.Text())
	}
	if !strings.Contains(screen.status.Text(), "2 files") {
		t.Fatalf("status=%q", screen.status.Text())
	}
}

func TestFindInFilesLayoutAllocatesListAndPreviewRectangles(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	component := ui.NewComponentWidget(th, screen, 80, 24)
	screen.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	if screen.list.Bounds().Empty() {
		t.Fatal("list should receive a non-empty rectangle")
	}
	if screen.preview.Bounds().Empty() {
		t.Fatal("preview should receive a non-empty rectangle when window is tall enough")
	}
	if screen.actions.Bounds().Empty() {
		t.Fatal("actions should receive a non-empty rectangle")
	}
	if screen.divider == nil || screen.divider.Bounds().Empty() {
		t.Fatal("results/preview split should mount a divider")
	}
	if screen.preview.Bounds().Y <= screen.list.Bounds().Y+screen.list.Bounds().H {
		t.Fatal("preview should start below the list and divider")
	}
}

func TestFindInFilesSplitDividerDragResizesResults(t *testing.T) {
	screen, host := searchHost(t)
	startH := screen.list.Bounds().H
	div := screen.divider.Bounds()
	if div.Empty() {
		t.Fatal("divider bounds empty")
	}
	clickY := div.Y
	res := host.Handle(tea.MouseClickMsg{X: 10, Y: clickY, Button: tea.MouseLeft})
	if !res.Handled || !host.HasPointerCapture() {
		t.Fatalf("click divider: handled=%v capture=%v", res.Handled, host.HasPointerCapture())
	}
	host.Handle(tea.MouseMotionMsg{X: 10, Y: clickY + 3, Button: tea.MouseLeft})
	host.Layout()
	if screen.list.Bounds().H != startH+3 {
		t.Fatalf("after +3 drag list H=%d want %d", screen.list.Bounds().H, startH+3)
	}
}

func searchHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(t.TempDir())
	component := ui.NewComponentWidget(th, screen, 80, 24)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	screen.SetFocusHandler(component.Focus)
	screen.FocusQuery()
	return screen, host
}

func TestFindInFilesTogglesSitOnTheRight(t *testing.T) {
	screen, _ := searchHost(t)
	if screen.caseToggle.Bounds().X <= screen.queryField.Bounds().X {
		t.Fatalf("case toggle X=%d should be right of query X=%d", screen.caseToggle.Bounds().X, screen.queryField.Bounds().X)
	}
	if screen.regexToggle.Bounds().X <= screen.caseToggle.Bounds().X {
		t.Fatalf("regex toggle should sit to the right of case")
	}
}

func TestReplaceInFilesLayoutShowsReplaceRowAndButtons(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.OpenReplace(t.TempDir())
	component := ui.NewComponentWidget(th, screen, 80, 24)
	screen.SetFocusHandler(component.Focus)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	if screen.Mode() != ModeReplace {
		t.Fatal("expected replace mode")
	}
	if screen.replaceField.Bounds().Empty() || screen.replaceActions.Bounds().Empty() {
		t.Fatal("replace row should be laid out")
	}
	if screen.replaceField.Bounds().Y != 1 {
		t.Fatalf("replace row Y=%d want 1", screen.replaceField.Bounds().Y)
	}
	if screen.list.Bounds().Y < 3 {
		t.Fatalf("list Y=%d should sit below replace+status", screen.list.Bounds().Y)
	}
	buf := surface.NewCellBuffer(80, 24)
	host.Paint(buf)
	painted := bufferText(buf)
	for _, label := range []string{"Replace with", "Replace", "All", "Open", "Close"} {
		if !strings.Contains(painted, label) {
			t.Fatalf("missing %q:\n%s", label, painted)
		}
	}
}

func TestReplaceInFilesEmitsReplaceMessages(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.OpenReplace(t.TempDir())
	component := ui.NewComponentWidget(th, screen, 80, 24)
	host := component.Host()
	host.Resize(80, 24)
	host.Layout()
	screen.SetFocusHandler(component.Focus)
	screen.SetQuery("alpha")
	screen.SetReplacement("omega")
	screen.ScheduleSearch()
	screen.ApplySearchResults(screen.Generation(), []fileio.ProjectSearchMatch{{
		Path: "/root/a.go", RelativePath: "a.go", Line: 1, Column: 0, EndColumn: 5,
		LineText: "alpha", TextMatches: []int{0, 1, 2, 3, 4},
	}}, nil, time.Millisecond)

	one := screen.replaceSelected()
	if one == nil {
		t.Fatal("replace one should emit")
	}
	msg, ok := one().(ReplaceMsg)
	if !ok || msg.All || msg.Replacement != "omega" || msg.Match.Path != "/root/a.go" {
		t.Fatalf("replace one=%#v", msg)
	}
	all := screen.replaceAll()
	if all == nil {
		t.Fatal("replace all should emit")
	}
	allMsg, ok := all().(ReplaceMsg)
	if !ok || !allMsg.All || allMsg.Query != "alpha" {
		t.Fatalf("replace all=%#v", allMsg)
	}
}

func bufferText(buf *surface.CellBuffer) string {
	var result strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result.WriteRune(r)
		}
		result.WriteByte('\n')
	}
	return filepath.ToSlash(result.String())
}
