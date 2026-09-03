package shellterm

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
)

func newTestScreen(t *testing.T) (*Screen, *[][]byte) {
	t.Helper()
	s := New(theme.DefaultDark(), "t1", "Terminal")
	if s.initErr != nil {
		t.Skipf("libghostty-vt: %v", s.initErr)
	}
	t.Cleanup(s.Close)
	var writes [][]byte
	s.SetWrite(func(b []byte) {
		writes = append(writes, append([]byte(nil), b...))
	})
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: 40, H: 11})
	return s, &writes
}

func joined(writes *[][]byte) string {
	return string(bytes.Join(*writes, nil))
}

func keyEvent(code rune, mod tea.KeyMod, text string) ui.Event {
	return ui.KeyEvent(tea.KeyPressMsg{Code: code, Mod: mod, Text: text})
}

func TestScreenShiftPageUpScrollsInsteadOfWriting(t *testing.T) {
	s, writes := newTestScreen(t)
	for i := 0; i < 40; i++ {
		s.Feed([]byte("line\r\n"))
	}
	res := s.Handle(ui.EventContext{}, keyEvent(tea.KeyPgUp, tea.ModShift, ""))
	if !res.Handled {
		t.Fatal("Shift+PgUp not handled")
	}
	if len(*writes) != 0 {
		t.Fatalf("Shift+PgUp wrote %q to the PTY; want scrollback scroll", joined(writes))
	}
	if s.vt.ViewportPinned() {
		t.Fatal("viewport still pinned after Shift+PgUp")
	}
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyEnd, tea.ModShift, ""))
	if !s.vt.ViewportPinned() {
		t.Fatal("Shift+End did not return to bottom")
	}
}

func TestScreenTypingSnapsToBottomAndClearsSelection(t *testing.T) {
	s, writes := newTestScreen(t)
	for i := 0; i < 40; i++ {
		s.Feed([]byte("line\r\n"))
	}
	s.vt.ScrollDelta(-5)
	s.vt.SelectionPress(0, 0, 1)
	s.vt.SelectionDrag(3, 0)
	if !s.vt.HasSelection() {
		t.Fatal("fixture selection missing")
	}
	s.Handle(ui.EventContext{}, keyEvent('a', 0, "a"))
	if s.vt.HasSelection() {
		t.Fatal("typing kept the selection")
	}
	if !s.vt.ViewportPinned() {
		t.Fatal("typing did not snap the viewport to the bottom")
	}
	if got := joined(writes); got != "a" {
		t.Fatalf("typed bytes=%q want a", got)
	}
}

func TestScreenAltScreenWheelSendsArrows(t *testing.T) {
	s, writes := newTestScreen(t)
	s.Feed([]byte("\x1b[?1049h"))
	res := s.Handle(ui.EventContext{LocalX: 3, LocalY: 3},
		ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 3, Y: 3}))
	if !res.Handled {
		t.Fatal("wheel not handled")
	}
	got := joined(writes)
	if got != strings.Repeat("\x1b[A", 3) {
		t.Fatalf("alt-screen wheel wrote %q; want three Up arrows", got)
	}
}

func TestScreenPrimaryScreenWheelScrollsViewport(t *testing.T) {
	s, writes := newTestScreen(t)
	for i := 0; i < 40; i++ {
		s.Feed([]byte("line\r\n"))
	}
	s.Handle(ui.EventContext{LocalX: 3, LocalY: 3},
		ui.MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 3, Y: 3}))
	if len(*writes) != 0 {
		t.Fatalf("primary-screen wheel wrote %q; want viewport scroll", joined(writes))
	}
	if s.vt.ViewportPinned() {
		t.Fatal("wheel did not scroll into history")
	}
}

func TestScreenScrollbackScrollbarTrackAndThumbInteraction(t *testing.T) {
	s, _ := newTestScreen(t)
	for i := 0; i < 60; i++ {
		s.Feed([]byte("line\r\n"))
	}
	s.vt.ScrollDelta(-10)
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
	before, _, _ := s.vt.Scrollbar()
	// The internal terminal scrollbar also accepts the two cells inward from
	// the painted right-edge stroke.
	host.Handle(tea.MouseClickMsg{X: bar.X - 2, Y: trackY, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: bar.X - 2, Y: trackY, Button: tea.MouseLeft})
	afterTrack, _, _ := s.vt.Scrollbar()
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
	afterDrag, _, _ := s.vt.Scrollbar()
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
	s.Feed([]byte("hello world"))

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
	s.Feed([]byte("hello world"))
	s.vt.SelectionPress(6, 0, 1)
	s.vt.SelectionDrag(10, 0)

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

func TestScreenShiftClickBypassesMouseTracking(t *testing.T) {
	s, writes := newTestScreen(t)
	s.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	s.Feed([]byte("target text"))
	if !s.vt.MouseTracking() {
		t.Fatal("mouse tracking not enabled by fixture")
	}
	s.Handle(ui.EventContext{LocalX: 0, LocalY: 0},
		ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: 0, Y: 0}))
	if len(*writes) != 0 {
		t.Fatalf("shift-click was encoded to the child: %q", joined(writes))
	}
	s.Handle(ui.EventContext{LocalX: 5, LocalY: 0},
		ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft, Mod: tea.ModShift, X: 5, Y: 0}))
	if !s.vt.HasSelection() {
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
	s.Feed([]byte("\x1b[?1004h"))
	s.SetTermFocused(true)
	s.SetTermFocused(true)
	s.SetTermFocused(false)
	if got := joined(writes); got != "\x1b[I\x1b[O" {
		t.Fatalf("focus reports=%q want CSI I then CSI O exactly once each", got)
	}
}

func TestScreenCursorTickReportsBlinkPhaseChanges(t *testing.T) {
	s, _ := newTestScreen(t)
	s.Feed([]byte("x\x1b[1;1H"))
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
	s.Feed([]byte("x\x1b[1;1H"))
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

func TestScreenFeedReportsClipboardAndBell(t *testing.T) {
	s, _ := newTestScreen(t)
	fx := s.Feed([]byte("ding\x07"))
	if !fx.Bell {
		t.Fatal("bell not reported")
	}
	fx = s.Feed([]byte("\x1b]52;c;aGVsbG8=\x07"))
	if !fx.HasClipboard || fx.Clipboard != "hello" {
		t.Fatalf("clipboard effect=%+v want hello", fx)
	}
	fx = s.Feed([]byte("\x1b]7;file:///tmp/dir\x07"))
	if !fx.PwdChanged || s.PWD() != "/tmp/dir" {
		t.Fatalf("pwd effect=%+v PWD=%q want /tmp/dir", fx, s.PWD())
	}
	fx = s.Feed([]byte("plain"))
	if fx.Bell || fx.HasClipboard || fx.PwdChanged || fx.TitleChanged {
		t.Fatalf("quiet feed reported effects: %+v", fx)
	}
}
