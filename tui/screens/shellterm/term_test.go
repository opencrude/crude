package shellterm

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestScreenSelectionThemeSwitchPreservesChildInk(t *testing.T) {
	s, _ := newTestScreen(t)
	fixture(s, "abc", 11, terminalvt.Modes{})
	p := s.projection
	for i := 0; i < 3; i++ {
		p.Grid.Cells[0][i].FG = terminalvt.Color{Explicit: true, R: 200, G: 10, B: 20}
		p.Grid.Cells[0][i].BG = terminalvt.Color{Explicit: true, R: 30, G: 40, B: 50}
		p.Grid.Cells[0][i].Style = terminalvt.Style{Faint: true, Inverse: true}
	}
	p.Grid.Cursor.Visible = false
	s.SetProjection(p)
	buf := surface.NewCellBuffer(40, 11)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	original := buf.Get(2, 0)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(40, 11)
	host.Layout()
	host.Handle(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultHighContrast(), theme.DefaultWarm(), theme.DefaultMidnight()} {
		host.SetTheme(th)
		host.Paint(buf)
		tokens := ui.NewTokenSet(th)
		c := buf.Get(0, 0)
		if c.FG != tokens.Color(ui.ColorTextSelectionFg) || c.BG != tokens.Color(ui.ColorTextSelection) || c.Attr&(cell.AttrReverse|cell.AttrDim) != 0 {
			t.Fatalf("theme selection=%+v", c)
		}
		if got := buf.Get(2, 0); got != original {
			t.Fatalf("theme changed explicit child ink: got=%+v want=%+v", got, original)
		}
		if got, _ := s.view.SelectionText(); got != "ab" {
			t.Fatalf("theme changed selection text=%q", got)
		}
	}
	s.view.ClearSelection()
	host.Paint(buf)
	if c := buf.Get(0, 0); c.FG != original.FG || c.BG != original.BG || c.Attr != original.Attr {
		t.Fatalf("clearing selection did not restore child ink: %+v", c)
	}
}

func fixture(s *Screen, text string, total int, modes terminalvt.Modes) {
	cells := make([][]terminalvt.Cell, total)
	for y := range cells {
		cells[y] = make([]terminalvt.Cell, 40)
		for x := range cells[y] {
			cells[y][x] = terminalvt.Cell{Text: " ", Width: 1}
		}
		for x, r := range text {
			if x < 40 {
				cells[y][x].Text = string(r)
			}
		}
	}
	s.SetProjection(workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "t1", Title: "Terminal", Alive: true}, Generation: s.view.Generation + 1, Grid: terminalvt.Projection{Cols: 40, GridRows: 11, Total: total, Cells: cells, Modes: modes, Cursor: terminalvt.Cursor{Visible: true, Blink: true}}})
}
func newTestScreen(t *testing.T) (*Screen, *[]Command) {
	t.Helper()
	s := New(theme.DefaultDark(), "t1", "Terminal")
	t.Cleanup(s.Close)
	var commands []Command
	s.SetCommand(func(c Command) tea.Cmd { commands = append(commands, c); return nil })
	fixture(s, "", 11, terminalvt.Modes{})
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: 11})
	s.SetControlled(true)
	s.TakePendingCommands()
	return s, &commands
}
func joined(commands *[]Command) string { return fmt.Sprint(*commands) }

func keyEvent(code rune, mod tea.KeyMod, text string) ui.Event {
	return ui.KeyEvent(tea.KeyPressMsg{Code: code, Mod: mod, Text: text})
}

func TestScreenShiftPageUpScrollsInsteadOfWriting(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "line", 51, terminalvt.Modes{})
	res := s.Handle(ui.EventContext{}, keyEvent(tea.KeyPgUp, tea.ModShift, ""))
	if !res.Handled {
		t.Fatal("Shift+PgUp not handled")
	}
	if len(*writes) != 0 {
		t.Fatalf("Shift+PgUp wrote %q to the PTY; want scrollback scroll", joined(writes))
	}
	if s.view.Pinned() {
		t.Fatal("viewport still pinned after Shift+PgUp")
	}
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyEnd, tea.ModShift, ""))
	if !s.view.Pinned() {
		t.Fatal("Shift+End did not return to bottom")
	}
}

func TestScreenTypingSnapsToBottomAndClearsSelection(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "line", 51, terminalvt.Modes{})
	s.view.ScrollDelta(-5)
	s.view.SelectionPress(0, 0, 1)
	s.view.SelectionDrag(3, 0)
	if !s.view.HasSelection() {
		t.Fatal("fixture selection missing")
	}
	s.Handle(ui.EventContext{}, keyEvent('a', 0, "a"))
	if s.view.HasSelection() {
		t.Fatal("typing kept the selection")
	}
	if !s.view.Pinned() {
		t.Fatal("typing did not snap the viewport to the bottom")
	}
	if got := joined(writes); len(*writes) != 1 || (*writes)[0].Key.Text != "a" {
		t.Fatalf("typed bytes=%q want a", got)
	}
}

func TestScreenAltScreenWheelSendsArrows(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "", 11, terminalvt.Modes{AltScreen: true})
	res := s.Handle(ui.EventContext{LocalX: 3, LocalY: 3},
		ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 3, Y: 3}))
	if !res.Handled {
		t.Fatal("wheel not handled")
	}
	got := joined(writes)
	if len(*writes) != 1 || (*writes)[0].Key.Code != terminalvt.KeyUp || (*writes)[0].Repeat != 3 {
		t.Fatalf("alt-screen wheel wrote %q; want three Up arrows", got)
	}
}

func TestScreenPrimaryScreenWheelScrollsViewport(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "line", 51, terminalvt.Modes{})
	s.Handle(ui.EventContext{LocalX: 3, LocalY: 3},
		ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 3, Y: 3}))
	if len(*writes) != 0 {
		t.Fatalf("primary-screen wheel wrote %q; want viewport scroll", joined(writes))
	}
	if s.view.Pinned() {
		t.Fatal("wheel did not scroll into history")
	}
}

func TestScreenScrollbackScrollbarTrackAndThumbInteraction(t *testing.T) {
	s, _ := newTestScreen(t)
	fixture(s, "line", 71, terminalvt.Modes{})
	s.view.ScrollDelta(-10)
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(40, 11)
	host.Layout()
	if !s.syncScrollbackScrollbar() {
		t.Fatal("scrollback scrollbar not visible")
	}
	bar := s.scrollbar.Bounds()
	top, thumbH := s.scroll.ThumbBounds(bar.H)
	trackY := 0
	if top == 0 {
		trackY = top + thumbH
	}
	if trackY >= bar.H {
		t.Fatal("harness: no track cell outside thumb")
	}
	before, _, _ := s.view.Scrollbar()
	// The internal terminal scrollbar also accepts the two cells inward from
	// the painted right-edge stroke.
	host.Handle(tea.MouseClickMsg{X: bar.X - 2, Y: trackY, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: bar.X - 2, Y: trackY, Button: tea.MouseLeft})
	afterTrack, _, _ := s.view.Scrollbar()
	if afterTrack == before || s.selecting {
		t.Fatalf("track click offset=%d->%d selecting=%v", before, afterTrack, s.selecting)
	}

	s.syncScrollbackScrollbar()
	top, _ = s.scroll.ThumbBounds(bar.H)
	host.Handle(tea.MouseClickMsg{X: bar.X, Y: top, Button: tea.MouseLeft})
	owner, _, captured := host.CapturedPointer()
	if !captured || owner != s.ID() || !s.scrollbar.IsDragging() {
		t.Fatalf("thumb capture owner=%q captured=%v dragging=%v", owner, captured, s.scrollbar.IsDragging())
	}
	dragY := bar.H - 1
	if top > bar.H/2 {
		dragY = 0
	}
	host.Handle(tea.MouseMotionMsg{X: 2, Y: dragY, Button: tea.MouseLeft})
	afterDrag, _, _ := s.view.Scrollbar()
	if afterDrag == afterTrack {
		t.Fatalf("thumb drag offset stayed at %d", afterDrag)
	}
	host.Handle(tea.MouseReleaseMsg{X: 2, Y: dragY, Button: tea.MouseLeft})
	if host.HasPointerCapture() || s.scrollbar.IsDragging() {
		t.Fatal("scrollback scrollbar release did not clear capture")
	}
}

func TestScreenSelectionCopiesOnRelease(t *testing.T) {
	s, _ := newTestScreen(t)
	var copied []string
	s.SetCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			copied = append(copied, text)
			return nil
		}
	})
	now := uint64(1_000_000_000)
	s.SetNow(func() uint64 { return now })
	fixture(s, "hello world", 11, terminalvt.Modes{})

	s.Handle(ui.EventContext{LocalX: 0, LocalY: 0},
		ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: 0}))
	s.Handle(ui.EventContext{LocalX: 4, LocalY: 0},
		ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft, X: 4, Y: 0}))
	res := s.Handle(ui.EventContext{LocalX: 4, LocalY: 0},
		ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 4, Y: 0}))

	if len(copied) != 0 {
		t.Fatalf("clipboard write ran synchronously: copied=%q", copied)
	}
	if res.Cmd == nil {
		t.Fatal("selection release returned no clipboard command")
	}
	_ = res.Cmd()
	if len(copied) != 1 || copied[0] != "hello" {
		t.Fatalf("copied=%q want [hello]", copied)
	}
}

func TestScreenCtrlShiftCCopiesWithoutWriting(t *testing.T) {
	s, writes := newTestScreen(t)
	var copied []string
	s.SetCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			copied = append(copied, text)
			return nil
		}
	})
	fixture(s, "hello world", 11, terminalvt.Modes{})
	s.view.SelectionPress(6, 0, 1)
	s.view.SelectionDrag(10, 0)

	res := s.Handle(ui.EventContext{}, keyEvent('c', tea.ModCtrl|tea.ModShift, ""))
	if !res.Handled {
		t.Fatal("Ctrl+Shift+C not handled")
	}
	if len(copied) != 0 {
		t.Fatalf("clipboard write ran synchronously: copied=%q", copied)
	}
	if res.Cmd == nil {
		t.Fatal("Ctrl+Shift+C returned no clipboard command")
	}
	_ = res.Cmd()
	if len(copied) != 1 || copied[0] != "world" {
		t.Fatalf("copied=%q want [world]", copied)
	}
	if len(*writes) != 0 {
		t.Fatalf("Ctrl+Shift+C leaked %q to the PTY", joined(writes))
	}
}

func TestScreenExplicitCopyWithoutSelectionNeverInterrupts(t *testing.T) {
	s, writes := newTestScreen(t)
	s.Handle(ui.EventContext{}, keyEvent('c', tea.ModCtrl|tea.ModShift, ""))
	if len(*writes) != 0 {
		t.Fatal("empty explicit copy leaked a control-C to child")
	}
}

func TestScreenControlKeysAreNotEditingShortcuts(t *testing.T) {
	for _, code := range []rune{'c', 'd', 'z', 's', 'q', '\\', 'l', 'u', 'w', 'a', 'e', 'k', 'r'} {
		s, writes := newTestScreen(t)
		fixture(s, "selection", 11, terminalvt.Modes{})
		s.view.SelectAll()
		s.Handle(ui.EventContext{}, keyEvent(code, tea.ModCtrl, ""))
		if len(*writes) != 1 || (*writes)[0].Kind != CommandKey || (*writes)[0].Key.Code != code || (*writes)[0].Key.Mod != terminalvt.ModCtrl {
			t.Fatalf("Ctrl+%c stolen by UI: %+v", code, *writes)
		}
	}
}

func TestScreenShiftClickBypassesMouseTracking(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "target text", 11, terminalvt.Modes{MouseTracking: true})
	if !s.view.Grid.Modes.MouseTracking {
		t.Fatal("mouse tracking not enabled by fixture")
	}
	s.Handle(ui.EventContext{LocalX: 0, LocalY: 0},
		ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: 0, Y: 0}))
	if len(*writes) != 0 {
		t.Fatalf("shift-click was encoded to the child: %q", joined(writes))
	}
	s.Handle(ui.EventContext{LocalX: 5, LocalY: 0},
		ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: 5, Y: 0}))
	if !s.view.HasSelection() {
		t.Fatal("shift-drag did not build a selection while tracking is on")
	}

	// Without shift the click is encoded for the child.
	s.Handle(ui.EventContext{LocalX: 1, LocalY: 0},
		ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: 1, Y: 0}))
	if len(*writes) == 0 {
		t.Fatal("unshifted click was not sent to the tracking child")
	}
}

func TestScreenFocusReportTransitions(t *testing.T) {
	s, writes := newTestScreen(t)
	fixture(s, "", 11, terminalvt.Modes{FocusReporting: true})
	s.SetTermFocused(true)
	s.SetTermFocused(true)
	s.SetTermFocused(false)
	*writes = s.TakePendingCommands()
	if got := joined(writes); len(*writes) != 2 || !(*writes)[0].Focused || (*writes)[1].Focused {
		t.Fatalf("focus reports=%q want CSI I then CSI O exactly once each", got)
	}
}

func TestScreenCursorTickReportsBlinkPhaseChanges(t *testing.T) {
	s, _ := newTestScreen(t)
	fixture(s, "x", 11, terminalvt.Modes{})
	s.SetTermFocused(true)
	s.resetCursorBlink(time.UnixMilli(0))

	// Reset puts the cursor in the visible activity phase, so the same instant
	// is already represented and should not invalidate.
	if s.TickCursor(time.UnixMilli(0)) {
		t.Fatal("unchanged cursor phase reported a visual change")
	}
	if !s.TickCursor(time.UnixMilli(3 * ui.CursorBlinkHalfPeriodMS)) {
		t.Fatal("cursor off phase did not report a visual change")
	}
	if !s.TickCursor(time.UnixMilli(4 * ui.CursorBlinkHalfPeriodMS)) {
		t.Fatal("cursor on phase did not report a visual change")
	}
	// Repeating the same phase must be a no-op so floating terminals do not
	// repaint on unrelated fast ticks.
	if s.TickCursor(time.UnixMilli(4*ui.CursorBlinkHalfPeriodMS + 100)) {
		t.Fatal("unchanged on phase reported a visual change")
	}
}

func TestScreenCursorActivityStopsBlinkUntilIdle(t *testing.T) {
	s, _ := newTestScreen(t)
	fixture(s, "x", 11, terminalvt.Modes{})
	s.SetTermFocused(true)
	start := time.UnixMilli(0)
	s.resetCursorBlink(start)

	if !s.TickCursor(start.Add(3 * ui.CursorBlinkHalfPeriodMS * time.Millisecond)) {
		t.Fatal("precondition: cursor did not enter its off phase")
	}
	activity := start.Add(1300 * time.Millisecond)
	if !s.resetCursorBlink(activity) {
		t.Fatal("activity did not make the hidden cursor immediately visible")
	}
	if s.TickCursor(activity.Add(ui.CursorActivityHoldMS*time.Millisecond - time.Millisecond)) {
		t.Fatal("cursor blinked before the post-activity idle delay elapsed")
	}
	if !s.TickCursor(activity.Add(ui.CursorActivityHoldMS * time.Millisecond)) {
		t.Fatal("cursor did not resume blinking after becoming idle")
	}
}

func TestScreenProjectionMetadataAndAttention(t *testing.T) {
	s, _ := newTestScreen(t)
	p := s.projection
	p.Info.CWD = "file:///tmp/dir"
	p.Info.Title = "child title"
	s.SetProjection(p)
	if s.PWD() != "/tmp/dir" || s.Title() != "child title" {
		t.Fatal("metadata lost")
	}
	if !s.SetAttention(true) || !s.HasAttention() || s.SetAttention(true) {
		t.Fatal("attention transitions")
	}
}

func TestObserverInputRequiresExplicitControlButCanSelectAndScroll(t *testing.T) {
	s, commands := newTestScreen(t)
	fixture(s, "hello", 51, terminalvt.Modes{MouseTracking: true})
	s.SetControlled(false)
	res := s.Handle(ui.EventContext{}, keyEvent('x', 0, "x"))
	if res.Action == nil || res.Action.ID != ActionTakeControl || len(*commands) != 0 {
		t.Fatal("observer input escaped lease gate")
	}
	s.Handle(ui.EventContext{LocalX: 0, LocalY: 0}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	s.Handle(ui.EventContext{LocalX: 4, LocalY: 0}, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
	if !s.view.HasSelection() {
		t.Fatal("observer cannot select tracked screen")
	}
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyPgUp, tea.ModShift, ""))
	if s.view.Pinned() || len(*commands) != 0 {
		t.Fatal("observer scroll sent input")
	}
}

func TestProjectionPageRequestsRejectStaleIntentAndMovePaint(t *testing.T) {
	s, _ := newTestScreen(t)
	p := s.projection
	p.Generation++
	p.Grid.Total = 51
	p.Grid.Start = 40
	for i := range p.Grid.Cells {
		p.Grid.Cells[i][0].Text = "z"
	}
	s.SetProjection(p)
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyPgUp, tea.ModShift, ""))
	pending := s.TakePendingCommands()
	if len(pending) != 1 || pending[0].Kind != CommandReadRows {
		t.Fatalf("page intent=%+v", pending)
	}
	first := pending[0]
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyHome, tea.ModShift, ""))
	next := s.TakePendingCommands()
	if len(next) != 1 {
		t.Fatalf("new intent=%+v", next)
	}
	page := p
	page.Grid.Start = 0
	page.Grid.Cells = make([][]terminalvt.Cell, 11)
	for i := range page.Grid.Cells {
		page.Grid.Cells[i] = []terminalvt.Cell{{Text: "a", Width: 1}}
	}
	if s.SetRows(first.Sequence, page) {
		t.Fatal("older request applied")
	}
	if !s.SetRows(next[0].Sequence, page) {
		t.Fatal("current page rejected")
	}
	buf := surface.NewCellBuffer(40, 11)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(0, 0).Content != "a" && buf.Get(0, 0).Char != 'a' {
		t.Fatalf("scroll did not move painted content %+v", buf.Get(0, 0))
	}
	page.Grid.Cells[0][0].Text = "b"
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(0, 0).Content == "b" {
		t.Fatal("snapshot aliased caller memory")
	}
	page.Generation--
	if s.SetRows(next[0].Sequence, page) {
		t.Fatal("old generation applied")
	}
}

func TestSelectionWordLineAndAll(t *testing.T) {
	s, _ := newTestScreen(t)
	fixture(s, "hello world", 11, terminalvt.Modes{})
	s.view.SelectionPress(7, 0, 1_000_000_000)
	s.view.SelectionPress(7, 0, 1_100_000_000)
	if text, _ := s.view.SelectionText(); text != "world" {
		t.Fatalf("word=%q", text)
	}
	s.view.SelectionPress(7, 0, 1_200_000_000)
	if text, _ := s.view.SelectionText(); text != "hello world" {
		t.Fatalf("line=%q", text)
	}
	s.Handle(ui.EventContext{}, keyEvent('a', tea.ModCtrl|tea.ModShift, ""))
	if text, _ := s.view.SelectionText(); strings.Count(text, "hello world") != 11 {
		t.Fatalf("all=%q", text)
	}
}

func TestPendingSelectAllCopyFetchesHistoryBeforeClipboard(t *testing.T) {
	s, _ := newTestScreen(t)
	p := s.projection
	p.Generation++
	p.Grid.Total = 22
	p.Grid.Start = 11
	s.SetProjection(p)
	var copied string
	s.SetCopy(func(text string) tea.Cmd { return func() tea.Msg { copied = text; return nil } })
	s.Handle(ui.EventContext{}, keyEvent('a', tea.ModCtrl|tea.ModShift, ""))
	res := s.Handle(ui.EventContext{}, keyEvent('c', tea.ModCtrl|tea.ModShift, ""))
	if res.Cmd != nil || copied != "" {
		t.Fatal("incomplete history copied")
	}
	commands := s.TakePendingCommands()
	if len(commands) != 1 || commands[0].Start != 0 || commands[0].Count != 22 {
		t.Fatalf("copy pages=%+v", commands)
	}
	page := p
	page.Grid.Start = 0
	page.Grid.Cells = make([][]terminalvt.Cell, 22)
	for i := range page.Grid.Cells {
		page.Grid.Cells[i] = []terminalvt.Cell{{Text: "line", Width: 1}}
	}
	if !s.SetRows(commands[0].Sequence, page) {
		t.Fatal("copy page rejected")
	}
	cmd := s.TakePendingCopy()
	if cmd == nil {
		t.Fatal("complete selection did not produce command")
	}
	cmd()
	if strings.Count(copied, "line") != 22 {
		t.Fatalf("partial copy=%q", copied)
	}
}

func TestTypedKeyPasteAndMouseStayUnencoded(t *testing.T) {
	s, commands := newTestScreen(t)
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyUp, tea.ModCtrl|tea.ModAlt, ""))
	s.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventCustom, Custom: tea.PasteMsg{Content: "\x1b[hello"}})
	fixture(s, "", 11, terminalvt.Modes{MouseTracking: true})
	s.Handle(ui.EventContext{LocalX: 2, LocalY: 3}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if len(*commands) != 3 || (*commands)[0].Key.Code != terminalvt.KeyUp || (*commands)[0].Key.Mod != terminalvt.ModCtrl|terminalvt.ModAlt || string((*commands)[1].Paste) != "\x1b[hello" || (*commands)[2].Mouse.X != 2 || (*commands)[2].Mouse.Y != 3 {
		t.Fatalf("commands=%+v", commands)
	}
}
