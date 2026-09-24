package activitylist

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	testW = 26
	testH = 12
)

func gitItems(n int) []ui.ListItem {
	items := make([]ui.ListItem, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, ui.ListItem{
			ID:     fmt.Sprintf("git:/repo/file%02d.go", i),
			Label:  fmt.Sprintf("file%02d.go", i),
			Detail: "M",
		})
	}
	return items
}

func newTestScreen(t *testing.T, items []ui.ListItem) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	s := New(th)
	s.SetHeading("Git")
	s.SetItems(items)
	host := ui.NewHost(th, s)
	host.Resize(testW, testH)
	host.Layout()
	return s, host
}

// paintFrame relayouts before painting, mirroring the root host's
// unconditional per-frame MarkLayout in renderShellToSurface.
func paintFrame(host *ui.Host, w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	host.MarkLayout()
	host.Paint(buf)
	return buf
}

func rowText(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestFocusedFilterArrowKeysNavigateFilteredRows(t *testing.T) {
	s, host := newTestScreen(t, gitItems(4))
	host.Focus(FilterID)
	for _, key := range []rune{'f', 'i', 'l', 'e'} {
		host.Handle(tea.KeyPressMsg{Code: key, Text: string(key)})
	}
	before := s.list.inner.SelectedIndex()
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if !result.Handled || s.list.inner.SelectedIndex() != before+1 {
		t.Fatalf("Down in filter did not navigate results: before=%d after=%d result=%#v", before, s.list.inner.SelectedIndex(), result)
	}
}

func TestWheelScrollsViewportAndReachesEnd(t *testing.T) {
	s, host := newTestScreen(t, gitItems(40))
	paintFrame(host, testW, testH)

	host.Handle(tea.MouseWheelMsg{X: 5, Y: 6, Button: tea.MouseWheelDown})
	if got := s.list.inner.Scroll().Offset(); got != 3 {
		t.Fatalf("offset after one wheel = %d want 3", got)
	}
	buf := paintFrame(host, testW, testH)
	if got := s.list.inner.Scroll().Offset(); got != 3 {
		t.Fatalf("offset after repaint = %d want 3 (per-frame relayout must not reset scroll)", got)
	}
	if !strings.Contains(rowText(buf, listTop), "file03.go") {
		t.Fatalf("first visible row = %q want file03.go", rowText(buf, listTop))
	}

	for i := 0; i < 40; i++ {
		host.Handle(tea.MouseWheelMsg{X: 5, Y: 6, Button: tea.MouseWheelDown})
	}
	maxOff := s.list.inner.Scroll().MaxOffset()
	if got := s.list.inner.Scroll().Offset(); got != maxOff {
		t.Fatalf("offset=%d want clamp at MaxOffset %d", got, maxOff)
	}
	buf = paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, testH-1), "file39.go") {
		t.Fatalf("bottom row = %q want last item visible", rowText(buf, testH-1))
	}

	host.Handle(tea.MouseWheelMsg{X: 5, Y: 6, Button: tea.MouseWheelUp})
	if got := s.list.inner.Scroll().Offset(); got != maxOff-3 {
		t.Fatalf("wheel up offset=%d want %d", got, maxOff-3)
	}
}

func TestWheelOverHeadingAndGapColumnScrolls(t *testing.T) {
	s, host := newTestScreen(t, gitItems(40))
	paintFrame(host, testW, testH)

	host.Handle(tea.MouseWheelMsg{X: 1, Y: 0, Button: tea.MouseWheelDown})
	if got := s.list.inner.Scroll().Offset(); got != 3 {
		t.Fatalf("wheel over icon column offset=%d want 3", got)
	}
	gapX := s.listWidth(ui.Rect{W: testW, H: testH})
	host.Handle(tea.MouseWheelMsg{X: gapX, Y: 5, Button: tea.MouseWheelDown})
	if got := s.list.inner.Scroll().Offset(); got != 6 {
		t.Fatalf("wheel over gap column offset=%d want 6", got)
	}
}

func TestPerFrameSetItemsPreservesScrollAndSelection(t *testing.T) {
	s, host := newTestScreen(t, gitItems(40))
	frame := func() {
		s.SetHeading("Git")
		s.SetItems(gitItems(40))
		paintFrame(host, testW, testH)
	}
	frame()

	first := host.Handle(tea.MouseClickMsg{X: 4, Y: listTop + 3, Button: tea.MouseLeft})
	if first.Action != nil {
		t.Fatalf("single click must not activate: %+v", first.Action)
	}
	host.Handle(tea.MouseReleaseMsg{X: 4, Y: listTop + 3, Button: tea.MouseLeft})
	if sel, ok := s.list.inner.Selected(); !ok || sel.ID != "git:/repo/file03.go" {
		t.Fatalf("selected after click = %+v", sel)
	}
	for i := 0; i < 4; i++ {
		host.Handle(tea.MouseWheelMsg{X: 5, Y: 6, Button: tea.MouseWheelDown})
	}
	if got := s.list.inner.Scroll().Offset(); got != 12 {
		t.Fatalf("offset after wheel=%d want 12", got)
	}

	frame()
	frame()
	if got := s.list.inner.Scroll().Offset(); got != 12 {
		t.Fatalf("offset after identical refresh frames=%d want 12", got)
	}
	if sel, ok := s.list.inner.Selected(); !ok || sel.ID != "git:/repo/file03.go" {
		t.Fatalf("selection lost across refresh: %+v", sel)
	}

	changed := append([]ui.ListItem{{ID: "git:/repo/aaa.go", Label: "aaa.go", Detail: "A"}}, gitItems(40)...)
	s.SetItems(changed)
	paintFrame(host, testW, testH)
	if got := s.list.inner.Scroll().Offset(); got != 12 {
		t.Fatalf("offset after data change=%d want 12", got)
	}
	if idx := s.list.inner.SelectedIndex(); idx != 4 {
		selected, _ := s.list.inner.Selected()
		t.Fatalf("selection should follow its ID to index 4, got %d (%s)", idx, selected.ID)
	}
}

func TestKeyboardNavigationFollowsSelectionPastViewport(t *testing.T) {
	s, host := newTestScreen(t, gitItems(40))
	paintFrame(host, testW, testH)
	host.Focus(ListID)

	for i := 0; i < 15; i++ {
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if idx := s.list.inner.SelectedIndex(); idx != 15 {
		t.Fatalf("selection=%d want 15", idx)
	}
	off := s.list.inner.Scroll().Offset()
	vh := s.list.inner.Scroll().Viewport()
	if 15 < off || 15 >= off+vh {
		t.Fatalf("selection 15 outside viewport [%d,%d)", off, off+vh)
	}
	buf := paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, testH-1), "file15.go") {
		t.Fatalf("bottom row=%q want file15.go", rowText(buf, testH-1))
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if idx := s.list.inner.SelectedIndex(); idx != 39 {
		t.Fatalf("End selection=%d want 39", idx)
	}
	if got := s.list.inner.Scroll().Offset(); got != s.list.inner.Scroll().MaxOffset() {
		t.Fatalf("End offset=%d want MaxOffset %d", got, s.list.inner.Scroll().MaxOffset())
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
	if idx := s.list.inner.SelectedIndex(); idx != 0 {
		t.Fatalf("Home selection=%d want 0", idx)
	}
	if got := s.list.inner.Scroll().Offset(); got != 0 {
		t.Fatalf("Home offset=%d want 0", got)
	}

	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if idx := s.list.inner.SelectedIndex(); idx != vh {
		t.Fatalf("PgDn selection=%d want %d", idx, vh)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if idx := s.list.inner.SelectedIndex(); idx != 0 {
		t.Fatalf("PgUp selection=%d want 0", idx)
	}
}

func TestSingleClickSelectsDoubleClickActivates(t *testing.T) {
	s, host := newTestScreen(t, gitItems(10))
	paintFrame(host, testW, testH)

	first := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if first.Action != nil {
		t.Fatalf("first click must select only, got action %+v", first.Action)
	}
	host.Handle(tea.MouseReleaseMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if sel, _ := s.list.inner.Selected(); sel.ID != "git:/repo/file02.go" {
		t.Fatalf("selected=%+v want file02", sel)
	}

	second := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if second.Action == nil || second.Action.ID != ActionActivate {
		t.Fatalf("double click action=%+v want %s", second.Action, ActionActivate)
	}
	if item, ok := second.Action.Payload.(ui.ListItem); !ok || item.ID != "git:/repo/file02.go" {
		t.Fatalf("payload=%+v", second.Action.Payload)
	}
	host.Handle(tea.MouseReleaseMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})

	third := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if third.Action != nil {
		t.Fatalf("click after activation must re-arm, got %+v", third.Action)
	}
	host.Handle(tea.MouseReleaseMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})

	s.list.lastClickAt = time.Now().Add(-time.Second)
	slow := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if slow.Action != nil {
		t.Fatalf("expired double-click window must not activate, got %+v", slow.Action)
	}
	host.Handle(tea.MouseReleaseMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})

	host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 4, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 3, Y: listTop + 4, Button: tea.MouseLeft})
	other := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop + 2, Button: tea.MouseLeft})
	if other.Action != nil {
		t.Fatalf("quick click on a different row must not activate, got %+v", other.Action)
	}
}

func TestEnterActivatesSelection(t *testing.T) {
	_, host := newTestScreen(t, gitItems(5))
	paintFrame(host, testW, testH)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Action == nil || res.Action.ID != ActionActivate {
		t.Fatalf("Enter action=%+v want %s", res.Action, ActionActivate)
	}
	if item, ok := res.Action.Payload.(ui.ListItem); !ok || item.ID != "git:/repo/file01.go" {
		t.Fatalf("payload=%+v", res.Action.Payload)
	}
}

func TestEmptyStateAndDisabledPlaceholder(t *testing.T) {
	s, host := newTestScreen(t, nil)
	buf := paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, listTop), emptyStateText) {
		t.Fatalf("empty list must show %q, row=%q", emptyStateText, rowText(buf, listTop))
	}

	s.SetItems([]ui.ListItem{{ID: "none", Label: "Working tree clean", Disabled: true}})
	buf = paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, listTop), "Working tree clean") {
		t.Fatalf("placeholder row=%q", rowText(buf, listTop))
	}
	if !buf.Get(1, listTop).FG.Equal(s.tokens.Color(ui.ColorMuted)) {
		t.Fatal("placeholder must paint muted")
	}
	res := host.Handle(tea.MouseClickMsg{X: 3, Y: listTop, Button: tea.MouseLeft})
	if res.Action != nil {
		t.Fatalf("placeholder must not activate: %+v", res.Action)
	}
	if idx := s.list.inner.SelectedIndex(); idx != -1 {
		t.Fatalf("placeholder must not be selectable, selection=%d", idx)
	}
}

func TestHoverHighlightsRowUnderPointerAndClears(t *testing.T) {
	s, host := newTestScreen(t, gitItems(10))
	paintFrame(host, testW, testH)

	host.Handle(tea.MouseMotionMsg{X: 4, Y: 5})
	buf := paintFrame(host, testW, testH)
	hover := s.tokens.TreeHoverBg()
	if !buf.Get(2, 5).BG.Equal(hover) {
		t.Fatal("row under pointer should paint hover bg")
	}
	if buf.Get(2, 3).BG.Equal(hover) {
		t.Fatal("rows away from the pointer must not paint hover")
	}

	host.Handle(tea.MouseMotionMsg{X: testW - 1, Y: 5})
	buf = paintFrame(host, testW, testH)
	if buf.Get(2, 5).BG.Equal(hover) {
		t.Fatal("hover must clear when the pointer leaves the list")
	}
}

func TestGitStatusDetailStaysVisibleOnLongPaths(t *testing.T) {
	long := ui.ListItem{
		ID:     "git:/repo/very/long/path.go",
		Label:  strings.Repeat("a", 60) + ".go",
		Detail: "M",
	}
	s, host := newTestScreen(t, []ui.ListItem{long})
	buf := paintFrame(host, testW, testH)
	listW := s.listWidth(ui.Rect{W: testW, H: testH})
	if got := buf.Get(listW-2, listTop).Char; got != 'M' {
		t.Fatalf("detail char=%q want M pinned right", got)
	}
	if !buf.Get(listW-2, listTop).FG.Equal(s.tokens.TreeGitModified()) {
		t.Fatal("git detail must use the tree git palette")
	}
	if !strings.Contains(rowText(buf, listTop), "…") {
		t.Fatalf("long label should be ellipsized: %q", rowText(buf, listTop))
	}
}

func TestLongListPaintsInsideListBounds(t *testing.T) {
	_, host := newTestScreen(t, gitItems(80))
	host.Resize(20, 6)
	host.Layout()
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})

	buf := surface.NewCellBuffer(20, 10)
	host.MarkLayout()
	host.Paint(buf)
	for y := 6; y < 10; y++ {
		if rowText(buf, y) != "" {
			t.Fatalf("row %d painted outside screen bounds: %q", y, rowText(buf, y))
		}
	}
	if !strings.Contains(rowText(buf, 5), "file79.go") {
		t.Fatalf("bottom viewport row=%q want file79.go", rowText(buf, 5))
	}
}

func TestHeaderPaintsIconThenSearch(t *testing.T) {
	_, host := newTestScreen(t, gitItems(3))
	buf := paintFrame(host, testW, testH)
	got := rowText(buf, 0)
	if strings.Contains(got, "GIT") {
		t.Fatalf("panel name must not paint, row=%q", got)
	}
	if !strings.Contains(got, "◈") {
		t.Fatalf("header row=%q want panel icon", got)
	}
	if !strings.Contains(strings.ToLower(got), "search") {
		t.Fatalf("header row=%q want search field immediately after the icon", got)
	}
	if strings.Contains(rowText(buf, 1), "Search") {
		t.Fatal("search must share the icon row, not sit on the next line")
	}
	if !strings.Contains(rowText(buf, listTop), "file00") {
		t.Fatalf("first body row=%q want file00", rowText(buf, listTop))
	}
}

func TestHeaderSearchAlignsWithListRightEdge(t *testing.T) {
	s, host := newTestScreen(t, gitItems(3))
	buf := paintFrame(host, testW, testH)
	listW := s.listWidth(ui.Rect{W: testW, H: testH})
	searchBG := s.tokens.Color(ui.ColorSubPaneBg)
	for x := listW; x < s.contentWidth(ui.Rect{W: testW, H: testH}); x++ {
		if buf.Get(x, 0).BG.Equal(searchBG) {
			t.Fatalf("search bg at col %d extends past list width %d", x, listW)
		}
	}
}
