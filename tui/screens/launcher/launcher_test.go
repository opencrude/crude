package launcher

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
	"crude/workspace"
)

func testScreen(t *testing.T, width, height int, workspaces []workspace.WorkspaceInfo) (*Screen, *ui.Host, *surface.CellBuffer) {
	t.Helper()
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", workspaces)
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(width, height)
	h.Layout()
	s.SyncFocus()
	buf := surface.NewCellBuffer(width, height)
	h.Paint(buf)
	return s, h, buf
}

func bufferText(buf *surface.CellBuffer) string {
	rows := make([]string, buf.Height())
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			ch := buf.Get(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

func TestPaintGoldenCellBufferContract(t *testing.T) {
	s, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{
		{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 2},
	})
	h.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("launcher paint produced no non-empty cells")
	}
	plain := bufferText(buf)
	for _, label := range []string{"New Session", "RECENT", "alpha", "✨"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("missing label %q:\n%s", label, plain)
		}
	}

	// Brand uses theme accent on row 0.
	wantAccent := ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("launcher paint missing theme accent foreground")
	}

	// Byte-identical regression on the static (pre-animation) frame.
	// Deterministic: testScreen never calls SetAnimation, so spinFrame=0
	// renders the hero's static layout.
	uitest.CheckGolden(t, "launcher_dark_110x16", buf)

	// Selected workspace row uses theme hover background.
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	buf.Clear()
	h.Paint(buf)
	wantHover := ui.NewTokenSet(theme.DefaultDark()).Color(ui.ColorHover)
	lb := s.list.Bounds()
	if got := buf.Get(lb.X, lb.Y).BG; got.IsDefault() || !got.Equal(wantHover) {
		t.Fatalf("selected row BG=%#v want hover %#v", got, wantHover)
	}
}

func TestLauncherSplitDividerDragResizesColumns(t *testing.T) {
	s, h, _ := testScreen(t, 110, 16, nil)
	startW := s.newWs.Bounds().W
	div := s.divider.Bounds()
	if div.Empty() {
		t.Fatal("launcher divider bounds empty")
	}
	clickX := div.X + div.W/2
	clickY := div.Y + 1
	res := h.Handle(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft})
	if !res.Handled || !h.HasPointerCapture() {
		t.Fatalf("click divider: handled=%v capture=%v hit=%q", res.Handled, h.HasPointerCapture(), h.HitTest(clickX, clickY))
	}
	h.Handle(tea.MouseMotionMsg{X: clickX + 5, Y: clickY, Button: tea.MouseLeft})
	h.Layout()
	if s.newWs.Bounds().W != startW+5 {
		t.Fatalf("after +5 drag left W=%d want %d", s.newWs.Bounds().W, startW+5)
	}
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

func bufferHasFG(buf *surface.CellBuffer, want cell.Color) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() && fg.Equal(want) {
				return true
			}
		}
	}
	return false
}

func TestScreenPaintPreservesWelcomeHierarchy(t *testing.T) {
	s, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{
		{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 2},
		{ID: "beta", ProjectName: "beta", ProjectDir: "/projects/beta", SessionCount: 1},
	})
	h.Paint(buf)
	plain := bufferText(buf)
	for _, want := range []string{
		"New Session",
		filepath.FromSlash("/projects/current"),
		"New Workspace",
		"SHORTCUTS",
		"RECENT",
		"Search workspaces",
		"alpha",
		"beta",
		"2 sessions",
		"1 session",
		"│",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("launcher paint missing %q:\n%s", want, plain)
		}
	}
	for y := 0; y < buf.Height(); y++ {
		if got := len([]rune(string(bufferRow(buf, y)))); got != buf.Width() {
			t.Fatalf("row %d width=%d, want %d", y, got, buf.Width())
		}
	}
	rows := make([][]rune, buf.Height())
	for y := range rows {
		rows[y] = bufferRow(buf, y)
	}
	if rows[1][0] != '│' || rows[1][109] != '│' || rows[2][0] != '│' || rows[2][109] != '│' {
		t.Fatalf("hero side borders are not continuous: %q / %q", string(rows[1]), string(rows[2]))
	}
	if rows[6][0] != '│' || rows[6][39] != '│' || rows[7][0] != '│' || rows[7][39] != '│' {
		t.Fatalf("New Workspace side borders are not continuous: %q / %q", string(rows[6]), string(rows[7]))
	}
	// The pane has two rails: the header sits on the content rail, and
	// body rows (search, list) sit one column further in on the body rail.
	// All body rows must share the same origin and width.
	pane := s.workspacePane
	if got := s.header.Bounds(); got.X != pane.Content.X || got.W != pane.Content.W {
		t.Fatalf("header not on content rail: got=%+v content=%+v", got, pane.Content)
	}
	if s.search.Bounds().X != s.list.Bounds().X || s.search.Bounds().W != s.list.Bounds().W {
		t.Fatalf("body rail drift: search=%+v list=%+v", s.search.Bounds(), s.list.Bounds())
	}
	if s.search.Bounds().X != pane.Content.X+1 {
		t.Fatalf("body rail should start one column past content: search.X=%d content.X=%d", s.search.Bounds().X, pane.Content.X)
	}
}

func bufferRow(buf *surface.CellBuffer, y int) []rune {
	row := make([]rune, buf.Width())
	for x := range row {
		row[x] = buf.Get(x, y).Char
		if row[x] == 0 {
			row[x] = ' '
		}
	}
	return row
}

func TestScreenEmptyAndNoResultsStates(t *testing.T) {
	s, h, buf := testScreen(t, 80, 20, nil)
	h.Paint(buf)
	if got := bufferText(buf); !strings.Contains(got, "No recent workspaces") {
		t.Fatalf("empty state missing:\n%s", got)
	}

	s.SetData("/projects/current", []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
	})
	h.MarkLayout()
	h.Layout()
	s.SyncFocus()
	h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	h.Handle(tea.KeyPressMsg{Code: 'z', Text: "z"})
	h.Layout()
	buf.Clear()
	h.Paint(buf)
	if got := bufferText(buf); !strings.Contains(got, `No workspaces match "z"`) {
		t.Fatalf("no-results state missing:\n%s", got)
	}
}

func TestScreenNavigationAndFilterFocusAreStable(t *testing.T) {
	s, h, _ := testScreen(t, 100, 24, []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		{ProjectName: "beta", ProjectDir: "/projects/beta"},
	})
	if got := s.SelectedItem(); got == nil || got.Kind != ItemNewSession {
		t.Fatalf("initial selection=%#v", got)
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := s.SelectedItem(); got == nil || got.Kind != ItemOpenFolder {
		t.Fatalf("down selection=%#v", got)
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "alpha" {
		t.Fatalf("workspace selection=%#v", got)
	}

	h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !s.FilterActive() || h.Focused() != SearchID {
		t.Fatalf("filter focus=%q active=%v", h.Focused(), s.FilterActive())
	}
	h.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "beta" {
		t.Fatalf("filtered selection=%#v", got)
	}
	// Once filtering is active, focus may be moved by a mouse click, but
	// typing must return to the SearchField rather than becoming a dead key.
	h.Handle(tea.MouseClickMsg{X: 50, Y: 7, Button: tea.MouseLeft})
	h.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if got := s.Query(); got != "be" {
		t.Fatalf("filter query after list focus=%q", got)
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if s.FilterActive() || h.Focused() == SearchID {
		t.Fatalf("filter did not close: active=%v focus=%q", s.FilterActive(), h.Focused())
	}
}

func TestScreenActivationIsOwnerTagged(t *testing.T) {
	s, h, _ := testScreen(t, 100, 24, nil)
	result := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("Enter should activate New Session")
	}
	msg, ok := result.Cmd().(ActivateMsg)
	if !ok || msg.Screen != s || msg.Kind != ItemNewSession {
		t.Fatalf("activation=%#v", result.Cmd())
	}
}

func TestScreenRowsUseThemeAndSelectionState(t *testing.T) {
	s, h, buf := testScreen(t, 100, 24, []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
	})
	// Move from the hero to the workspace row.
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	buf.Clear()
	h.Paint(buf)
	// The selected row is the first workspace row in the body rail.
	lb := s.list.Bounds()
	selectedBG := buf.Get(lb.X, lb.Y).BG
	if selectedBG.IsDefault() || selectedBG.Equal(cell.NoColor) {
		t.Fatalf("selected row has no explicit hover background at (%d,%d): %#v", lb.X, lb.Y, selectedBG)
	}
	if got := s.SelectedItem(); got == nil || got.Workspace == nil {
		t.Fatal("workspace should be selected")
	}
}

func TestWorkspacePaneUsesOneColumnContentRailWithoutScrollbar(t *testing.T) {
	s, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{
		{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		{ProjectName: "beta", ProjectDir: "/projects/beta"},
	})
	pane := s.workspacePane
	if pane.Scrollbar.W != 0 {
		t.Fatalf("unexpected scrollbar geometry: %+v", pane.Scrollbar)
	}
	if pane.Content.X != pane.Bounds.X+paneContentInset {
		t.Fatalf("content must start one column inside pane: pane=%+v content=%+v", pane.Bounds, pane.Content)
	}
	if pane.Content.X+pane.Content.W != pane.Bounds.X+pane.Bounds.W {
		t.Fatalf("content should reach pane edge without a scrollbar: pane=%+v content=%+v", pane.Bounds, pane.Content)
	}
	// Header sits on the content rail.
	if got := s.header.Bounds(); got.X != pane.Content.X || got.W != pane.Content.W {
		t.Fatalf("header escaped the content rail: got=%+v content=%+v", got, pane.Content)
	}
	// Body rows (search, list, empty) sit one column further in on the body rail.
	bodyX := pane.Content.X + 1
	bodyW := pane.Content.W - 1
	for name, r := range map[string]ui.Rect{
		"search": s.search.Bounds(),
		"list":   s.list.Bounds(),
		"empty":  s.empty.Bounds(),
	} {
		if r.X != bodyX || r.W != bodyW {
			t.Fatalf("%s escaped the body rail: got=%+v body=(X=%d W=%d)", name, r, bodyX, bodyW)
		}
	}

	// Header and search begin on their respective rails. Selecting the first
	// workspace puts the row marker on the body rail, while its background
	// reaches the pane's final column without a compensating right gutter.
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	buf.Clear()
	h.Paint(buf)
	if got := buf.Get(s.header.Bounds().X, s.header.Bounds().Y).Char; got != 'R' {
		t.Fatalf("header starts at the wrong rail: %q", got)
	}
	if got := buf.Get(s.search.Bounds().X, s.search.Bounds().Y).Char; got != 'S' {
		t.Fatalf("search starts at the wrong rail: %q", got)
	}
	searchEnd := s.search.Bounds().X + s.search.Bounds().W - 1
	if got := buf.Get(searchEnd, s.search.Bounds().Y).Char; got != '⌕' {
		t.Fatalf("search magnifier should sit at the trailing edge: %q", got)
	}
	if got := buf.Get(s.list.Bounds().X, s.list.Bounds().Y).Char; got != '❯' {
		t.Fatalf("selected row starts at the wrong rail: %q", got)
	}
	rowEnd := s.list.Bounds().X + s.list.Bounds().W - 1
	if got := buf.Get(rowEnd, s.list.Bounds().Y).BG; !got.Equal(s.tokens.Color(ui.ColorHover)) {
		t.Fatalf("selected row does not reach the pane edge: got=%#v", got)
	}
}

func TestWorkspacePaneReservesOnlyFinalColumnForScrollbar(t *testing.T) {
	workspaces := make([]workspace.WorkspaceInfo, 30)
	for i := range workspaces {
		workspaces[i] = workspace.WorkspaceInfo{
			ProjectName: "workspace-" + string(rune('a'+i%26)),
			ProjectDir:  "/projects/workspace-" + string(rune('a'+i%26)),
		}
	}
	s, h, buf := testScreen(t, 110, 16, workspaces)
	pane := s.workspacePane
	if pane.Scrollbar.W != 1 {
		t.Fatalf("expected one-column scrollbar: %+v", pane.Scrollbar)
	}
	if pane.Scrollbar.X != pane.Bounds.X+pane.Bounds.W-1 {
		t.Fatalf("scrollbar must own pane's final column: pane=%+v scrollbar=%+v", pane.Bounds, pane.Scrollbar)
	}
	if pane.Content.X+pane.Content.W != pane.Scrollbar.X {
		t.Fatalf("content must terminate immediately before scrollbar: content=%+v scrollbar=%+v", pane.Content, pane.Scrollbar)
	}
	// Header sits on the content rail; body rows (search, list) sit on the
	// body rail, one column further in, terminating before the scrollbar.
	if got := s.header.Bounds(); got.X != pane.Content.X || got.W != pane.Content.W {
		t.Fatalf("header drifted from content rail: got=%+v content=%+v", got, pane.Content)
	}
	bodyX := pane.Content.X + 1
	bodyW := pane.Content.W - 1
	for name, r := range map[string]ui.Rect{
		"search": s.search.Bounds(),
		"list":   s.list.Bounds(),
	} {
		if r.X != bodyX || r.W != bodyW {
			t.Fatalf("%s drifted from scrollbar-aware body rail: got=%+v body=(X=%d W=%d)", name, r, bodyX, bodyW)
		}
	}
	if s.scrollbar.Bounds() != pane.scrollbarRect(s.list.Bounds().Y, s.list.Bounds().H) {
		t.Fatalf("scrollbar geometry was not derived from pane layout: got=%+v pane=%+v", s.scrollbar.Bounds(), pane)
	}

	searchBounds := s.search.Bounds()
	h.Handle(tea.MouseWheelMsg{X: searchBounds.X, Y: searchBounds.Y, Button: tea.MouseWheelDown})
	if s.ListScrollOffset() == 0 {
		t.Fatal("wheel on fixed search row should scroll only the workspace viewport")
	}
	if s.search.Bounds() != searchBounds {
		t.Fatalf("search moved while list scrolled: before=%+v after=%+v", searchBounds, s.search.Bounds())
	}
	buf.Clear()
	h.Paint(buf)
	if ch := buf.Get(pane.Scrollbar.X, s.list.Bounds().Y).Char; ch != '░' && ch != '█' {
		t.Fatalf("scrollbar did not paint in final pane column: %q", ch)
	}
}

func TestRebuildItemsEmitsWorkspacesOnly(t *testing.T) {
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", []workspace.WorkspaceInfo{
		{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 1},
	})

	var kinds []ItemKind
	for _, it := range s.items {
		kinds = append(kinds, it.Kind)
	}
	want := []ItemKind{ItemNewSession, ItemOpenFolder, ItemWorkspace}
	if len(kinds) != len(want) {
		t.Fatalf("items kinds=%v want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("items[%d]=%v want %v (all=%v)", i, kinds[i], want[i], kinds)
		}
	}
	s.selectFlat(2)
	if got := s.SelectedWorkspacePath(); got != "/projects/alpha" {
		t.Fatalf("SelectedWorkspacePath=%q", got)
	}
	cmd := s.activateSelected()
	if cmd == nil {
		t.Fatal("activateSelected returned nil")
	}
	msg := cmd()
	act, ok := msg.(ActivateMsg)
	if !ok || act.Kind != ItemWorkspace || act.WorkspacePath != "/projects/alpha" {
		t.Fatalf("ActivateMsg=%#v", msg)
	}
}

func TestPaintWorkspaceRow_EmptyNameUsesPathBasename(t *testing.T) {
	const hash = "84db4726b4a4528b"
	s, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{
		{ID: hash, ProjectName: "", ProjectDir: "/home/lenovo/ocd/readable-ws", SessionCount: 0},
	})
	_ = s
	_ = h
	plain := bufferText(buf)
	if !strings.Contains(plain, "readable-ws") {
		t.Fatalf("painted row must show path-derived name, got:\n%s", plain)
	}
	if strings.Contains(plain, hash) {
		t.Fatalf("painted row must not show raw workspace hash %q:\n%s", hash, plain)
	}
}
