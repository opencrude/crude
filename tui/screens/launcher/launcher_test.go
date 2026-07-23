package launcher

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func testScreen(t *testing.T, width, height int, workspaces []workspace.WorkspaceInfo) (*Screen, *ui.Host, *surface.CellBuffer) {
	t.Helper()
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", nil, workspaces)
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

func TestScreenPaintPreservesWelcomeHierarchy(t *testing.T) {
	s, h, buf := testScreen(t, 110, 16, []workspace.WorkspaceInfo{
		{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 2},
		{ID: "beta", ProjectName: "beta", ProjectDir: "/projects/beta", SessionCount: 1},
	})
	h.Paint(buf)
	plain := bufferText(buf)
	for _, want := range []string{
		"New Session",
		"/projects/current",
		"New Workspace",
		"SHORTCUTS",
		"RECENT WORKSPACES",
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
	// Header, search, and result rows share one right-column origin and
	// terminate at the same edge when the scrollbar is unnecessary.
	if s.header.Bounds().X != s.search.Bounds().X || s.search.Bounds().X != s.list.Bounds().X {
		t.Fatalf("right-column origins drift: header=%+v search=%+v list=%+v", s.header.Bounds(), s.search.Bounds(), s.list.Bounds())
	}
	if s.header.Bounds().W != s.search.Bounds().W || s.search.Bounds().W != s.list.Bounds().W {
		t.Fatalf("right-column widths drift without scrollbar: header=%+v search=%+v list=%+v", s.header.Bounds(), s.search.Bounds(), s.list.Bounds())
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

	s.SetData("/projects/current", nil, []workspace.WorkspaceInfo{
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
	selectedBG := buf.Get(44, 7).BG
	if selectedBG.IsDefault() || selectedBG.Equal(cell.NoColor) {
		t.Fatalf("selected row has no explicit hover background: %#v", selectedBG)
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
	for name, r := range map[string]ui.Rect{
		"header": s.header.Bounds(),
		"search": s.search.Bounds(),
		"list":   s.list.Bounds(),
		"empty":  s.empty.Bounds(),
	} {
		if r.X != pane.Content.X || r.W != pane.Content.W {
			t.Fatalf("%s escaped the pane contract: got=%+v content=%+v", name, r, pane.Content)
		}
	}

	// Header and search begin on the same rail. Selecting the first workspace
	// puts the row marker on that rail, while its background reaches the pane's
	// final column without a compensating right gutter.
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	buf.Clear()
	h.Paint(buf)
	if got := buf.Get(s.header.Bounds().X, s.header.Bounds().Y).Char; got != 'R' {
		t.Fatalf("header starts at the wrong rail: %q", got)
	}
	if got := buf.Get(s.search.Bounds().X, s.search.Bounds().Y).Char; got != '>' {
		t.Fatalf("search starts at the wrong rail: %q", got)
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
	for name, r := range map[string]ui.Rect{
		"header": s.header.Bounds(),
		"search": s.search.Bounds(),
		"list":   s.list.Bounds(),
	} {
		if r.X != pane.Content.X || r.W != pane.Content.W {
			t.Fatalf("%s drifted from scrollbar-aware content rail: got=%+v content=%+v", name, r, pane.Content)
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
