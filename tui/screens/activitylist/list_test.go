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

func TestActiveResourceIsIndependentOfKeyboardCursorAndCommands(t *testing.T) {
	items := []ui.ListItem{
		{ID: "new", Icon: "+", Label: "New resource", Command: true},
		{ID: "current", Label: "Current resource", Subtitle: "/repo/current", Active: true},
		{ID: "other", Label: "Other resource", Subtitle: "/repo/other"},
	}
	s, host := newTestScreen(t, items)
	host.Focus(ListID)
	buf := paintFrame(host, testW, testH)
	if buf.Get(2, listTop).BG != s.tokens.TreeCursorBg() {
		t.Fatal("command does not use the normal focused sidebar background")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	host.Focus(FilterID)
	buf = paintFrame(host, testW, testH)
	for _, row := range []int{listTop + 1, listTop + 2} {
		if buf.Get(2, row).BG != s.tokens.Color(ui.ColorSelection) {
			t.Fatal("keyboard browsing displaced current-resource name/path fill")
		}
	}
	if buf.Get(2, listTop).BG != s.tokens.Color(ui.ColorSubPaneBg) {
		t.Fatal("command lost its persistent control fill when focus left the list")
	}
	for _, row := range []int{listTop + 3, listTop + 4} {
		if buf.Get(2, row).BG != s.tokens.Color(ui.ColorBg) {
			t.Fatal("unfocused cursor/command falsely paints as current")
		}
	}
	// Refresh and ordering changes preserve identity, not a selected row index.
	s.SetItems([]ui.ListItem{items[0], items[2], items[1]})
	buf = paintFrame(host, testW, testH)
	if selected, _ := s.SelectedItem(); selected.ID != "other" || buf.Get(2, listTop+3).BG != s.tokens.Color(ui.ColorSelection) {
		t.Fatal("refresh changed browsing/current-resource identity")
	}
	// Hover is transient and never overwrites the committed resource flag.
	host.Handle(tea.MouseMotionMsg{X: 2, Y: listTop})
	buf = paintFrame(host, testW, testH)
	if buf.Get(2, listTop).BG != s.tokens.TreeHoverBg() {
		t.Fatal("command hover does not match other sidebar rows")
	}
	host.Handle(tea.MouseMotionMsg{X: testW - 1, Y: listTop})
	buf = paintFrame(host, testW, testH)
	if buf.Get(2, listTop).BG != s.tokens.Color(ui.ColorSubPaneBg) {
		t.Fatal("command lost its persistent control fill after pointer exit")
	}
	result := host.Handle(tea.MouseClickMsg{X: 2, Y: listTop, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.ID != ActionActivate {
		t.Fatal("native command row did not activate on one click")
	}
}

func TestCommandBackgroundSurvivesBrowsingBlurRefreshAndThemeChange(t *testing.T) {
	items := []ui.ListItem{
		{ID: "new", Icon: "+", Label: "New Workspace…", Command: true},
		{ID: "current", Label: "ox", Subtitle: "/projects/ox", Active: true},
		{ID: "other", Label: "crude", Subtitle: "/projects/crude"},
	}
	s, host := newTestScreen(t, items)
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		host.SetTheme(th)
		host.Focus(ListID)
		host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
		assertIdleFill := func() {
			t.Helper()
			buf := paintFrame(host, testW, testH)
			for x := 0; x < s.list.Bounds().W; x++ {
				if buf.Get(x, listTop).BG != s.tokens.Color(ui.ColorSubPaneBg) || buf.Get(x, listTop).BG == s.tokens.Color(ui.ColorBg) {
					t.Fatal("New Workspace control background disappeared while browsing or unfocused")
				}
			}
			for _, row := range []int{listTop + 1, listTop + 2} {
				if buf.Get(2, row).BG != s.tokens.Color(ui.ColorSelection) {
					t.Fatal("stable command fill displaced current-workspace name/path fill")
				}
			}
		}
		// Browsing another resource, leaving focus and refreshing must all retain
		// a real control surface, not borrow the resource's selected state.
		assertIdleFill()
		host.Focus(FilterID)
		assertIdleFill()
		host.Handle(tea.MouseMotionMsg{X: 2, Y: listTop})
		paintFrame(host, testW, testH)
		host.Handle(tea.MouseMotionMsg{X: testW - 1, Y: listTop})
		assertIdleFill()
		s.SetItems(append([]ui.ListItem(nil), items...))
		assertIdleFill()
		s.RestoreViewState(ViewState{Selection: "current"})
		assertIdleFill()
		result := host.Handle(tea.MouseClickMsg{X: 2, Y: listTop, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: 2, Y: listTop, Button: tea.MouseLeft})
		if result.Action == nil || result.Action.Payload.(ui.ListItem).ID != "new" {
			t.Fatal("persistent control needs more than one click")
		}
	}
}

func TestSingleClickNavigationTargetsNameAndPathWithoutChangingPickerPolicy(t *testing.T) {
	items := []ui.ListItem{{ID: "new", Label: "New Workspace…", Command: true}, {ID: "other", Label: "crude", Subtitle: "/projects/crude"}}
	s, host := newTestScreen(t, items)
	s.SetActivateOnClick(true)
	for _, row := range []int{listTop + 1, listTop + 2} {
		result := host.Handle(tea.MouseClickMsg{X: 2, Y: row, Button: tea.MouseLeft})
		host.Handle(tea.MouseReleaseMsg{X: 2, Y: row, Button: tea.MouseLeft})
		if result.Action == nil || result.Action.Payload.(ui.ListItem).ID != "other" {
			t.Fatal("one navigation click did not request the exact workspace from both lines")
		}
	}
	s.SetActivateOnClick(false)
	result := host.Handle(tea.MouseClickMsg{X: 2, Y: listTop + 1, Button: tea.MouseLeft})
	if result.Action != nil {
		t.Fatal("workspace navigation activation leaked into picker-style collections")
	}
}

func TestLoadingUsesLauncherProgressRuleWithoutMovingItems(t *testing.T) {
	items := []ui.ListItem{{ID: "new", Label: "New Workspace…", Command: true}, {ID: "current", Label: "ox", Subtitle: "/projects/ox", Active: true}}
	s, host := newTestScreen(t, items)
	paint := func(frame int) *surface.CellBuffer {
		host.SetFrame(frame)
		return paintFrame(host, testW, testH)
	}
	before := paint(100)
	listBounds, _ := host.ScreenRect(ListID)
	progressBounds, ok := host.ScreenRect(ProgressID)
	if !ok || progressBounds.Y != 1 || progressBounds.H != 1 || progressBounds.W != s.list.Bounds().W {
		t.Fatal("standard launcher progress rule is not directly below search")
	}
	s.SetLoading(true)
	first := paint(100)
	second := paint(1500)
	animated := false
	for x := 0; x < progressBounds.W; x++ {
		a, b := first.Get(x, progressBounds.Y), second.Get(x, progressBounds.Y)
		if a.Char != b.Char || a.BG != b.BG {
			t.Fatal("loading animation changed rule shape or background")
		}
		animated = animated || a.FG != b.FG
	}
	if !animated {
		t.Fatal("standard progress rule did not animate on the Host clock")
	}
	for y := listBounds.Y; y < testH; y++ {
		if rowText(first, y) != rowText(before, y) || rowText(second, y) != rowText(before, y) {
			t.Fatal("loading inserted text or moved resource rows")
		}
	}
	s.SetLoading(false)
	settled, later := paint(1600), paint(4000)
	for x := 0; x < progressBounds.W; x++ {
		if settled.Get(x, progressBounds.Y) != later.Get(x, progressBounds.Y) {
			t.Fatal("settled progress rule kept animating")
		}
	}
	if got, _ := host.ScreenRect(ListID); got != listBounds {
		t.Fatal("refresh changed list geometry")
	}
	// First load has animation, not a false empty label or loading prose.
	s.SetItems(nil)
	s.SetLoading(true)
	loading := paint(5000)
	if text := rowText(loading, listTop); strings.Contains(text, "No items") || strings.Contains(text, "No matches") || strings.Contains(text, "Loading") {
		t.Fatal("pending first load added empty/loading text instead of just the progress animation")
	}
}

func TestResourceSubtitlesShareGeometryAndFollowTheme(t *testing.T) {
	items := []ui.ListItem{
		{ID: "new", Icon: "+", Label: "New resource", Command: true},
		{ID: "first", Label: "First", Subtitle: "/src/first", Active: true},
		{ID: "second", Label: "Second", Subtitle: "/src/second"},
	}
	s, host := newTestScreen(t, items)
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		s.SetTheme(th)
		host.Focus(FilterID)
		buf := paintFrame(host, testW, testH)
		for row, text := range []string{"New resource", "First", "/src/first", "Second", "/src/second"} {
			if !strings.Contains(rowText(buf, listTop+row), text) {
				t.Fatalf("name/path row %d missing %q: %q", row, text, rowText(buf, listTop+row))
			}
		}
		for _, row := range []int{listTop + 2, listTop + 4} {
			if buf.Get(1, row).FG != s.tokens.Color(ui.ColorMuted) {
				t.Fatal("path text does not follow the current theme's muted token")
			}
		}
	}
	for _, row := range []int{3, 4} { // Both lines are the same resource, no spacer.
		item, ok := s.ItemAtLocalY(listTop + row)
		if !ok || item.ID != "second" {
			t.Fatal("path row is not part of its resource hit target")
		}
	}
	host.Handle(tea.MouseMotionMsg{X: 2, Y: listTop + 4})
	buf := paintFrame(host, testW, testH)
	for _, row := range []int{3, 4} {
		if buf.Get(2, listTop+row).BG != s.tokens.TreeHoverBg() {
			t.Fatal("path hover does not cover the whole resource")
		}
	}
	// A path click selects exactly its resource; the second click activates it.
	first := host.Handle(tea.MouseClickMsg{X: 2, Y: listTop + 4, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 2, Y: listTop + 4, Button: tea.MouseLeft})
	if selected, _ := s.SelectedItem(); first.Action != nil || selected.ID != "second" {
		t.Fatal("path click did not select the exact resource")
	}
	second := host.Handle(tea.MouseClickMsg{X: 2, Y: listTop + 4, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 2, Y: listTop + 4, Button: tea.MouseLeft})
	if second.Action == nil || second.Action.Payload.(ui.ListItem).ID != "second" {
		t.Fatal("path double-click did not activate the exact resource")
	}
	// Search indexes the retained full path, not just the row's name or badge.
	s.RestoreViewState(ViewState{Filter: "/src/second"})
	if len(s.list.inner.Items()) != 1 || !strings.Contains(rowText(paintFrame(host, testW, testH), listTop+1), "/src/second") {
		t.Fatal("path search discarded the useful secondary line")
	}
}

func TestResourcePathScrollAndClippedViewportUseOneHeightIndex(t *testing.T) {
	items := []ui.ListItem{{ID: "new", Label: "New", Command: true}}
	for i := 0; i < 40; i++ {
		items = append(items, ui.ListItem{ID: fmt.Sprint(i), Label: fmt.Sprintf("Project %02d", i), Subtitle: fmt.Sprintf("/src/project%02d", i)})
	}
	s, host := newTestScreen(t, items)
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	buf := paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, testH-2), "Project 39") || !strings.Contains(rowText(buf, testH-1), "/src/project39") {
		t.Fatal("End did not reveal both lines of the last resource")
	}
	s.list.inner.Scroll().ScrollTo(2) // top row is the path of Project 00
	buf = paintFrame(host, testW, testH)
	if !strings.Contains(rowText(buf, listTop), "/src/project00") {
		t.Fatal("clipped path row snapped back to its offscreen selection")
	}
	if item, ok := s.ItemAtLocalY(listTop); !ok || item.ID != "0" {
		t.Fatal("clipped path hit test disagrees with paint")
	}
	host.Handle(tea.MouseWheelMsg{X: 2, Y: listTop, Button: tea.MouseWheelDown})
	buf = paintFrame(host, testW, testH)
	if s.list.inner.Scroll().Offset() != 5 || !strings.Contains(rowText(buf, listTop), "Project 02") {
		t.Fatal("wheel scrolling did not use terminal rows")
	}
}
