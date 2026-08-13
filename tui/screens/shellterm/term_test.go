package shellterm

import (
	"bytes"
	"strings"
	"testing"

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

func TestScreenSelectionCopiesOnRelease(t *testing.T) {
	s, _ := newTestScreen(t)
	var copied []string
	s.SetCopy(func(text string) { copied = append(copied, text) })
	now := uint64(1_000_000_000)
	s.SetNow(func() uint64 { return now })
	s.Feed([]byte("hello world"))

	s.Handle(ui.EventContext{LocalX: 0, LocalY: 0},
		ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: 0, Y: 0}))
	s.Handle(ui.EventContext{LocalX: 4, LocalY: 0},
		ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft, X: 4, Y: 0}))
	s.Handle(ui.EventContext{LocalX: 4, LocalY: 0},
		ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 4, Y: 0}))

	if len(copied) != 1 || copied[0] != "hello" {
		t.Fatalf("copied=%q want [hello]", copied)
	}
}

func TestScreenCtrlShiftCCopiesWithoutWriting(t *testing.T) {
	s, writes := newTestScreen(t)
	var copied []string
	s.SetCopy(func(text string) { copied = append(copied, text) })
	s.Feed([]byte("hello world"))
	s.vt.SelectionPress(6, 0, 1)
	s.vt.SelectionDrag(10, 0)

	res := s.Handle(ui.EventContext{}, keyEvent('c', tea.ModCtrl|tea.ModShift, ""))
	if !res.Handled {
		t.Fatal("Ctrl+Shift+C not handled")
	}
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
