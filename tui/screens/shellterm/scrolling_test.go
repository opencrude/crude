package shellterm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/terminalvt"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestStreamingPrimaryScrollSelectionScrollbarAndEviction(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 24, Rows: 6, ScrollbackLines: 32})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for i := 0; i < 80; i++ {
		e.Feed([]byte(fmt.Sprintf("row-%04d\r\n", i)))
	}
	s := New(theme.DefaultDark(), "stream", "Stream")
	defer s.Close()
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 24, H: 6})
	var gen uint64 = 1
	read := func(c Command) workspace.TerminalProjection {
		t.Helper()
		g, err := e.Rows(terminalvt.RowRequest{Start: c.Start, Count: c.Count, ViewportStart: c.ViewportStart, HistoryEpoch: c.HistoryEpoch, HistoryBase: c.HistoryBase})
		if err != nil {
			t.Fatal(err)
		}
		return workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "stream", Alive: true}, Grid: g, Generation: gen}
	}
	s.SetProjection(read(Command{Start: -1}))
	settle := func() {
		t.Helper()
		for n := 0; n < 8; n++ {
			commands := s.TakePendingCommands()
			if len(commands) == 0 {
				return
			}
			for _, c := range commands {
				if c.Kind == CommandReadRows {
					s.SetRows(c.Sequence, read(c))
				}
			}
		}
		t.Fatal("row intents did not settle")
	}
	paint := func() string {
		buf := surface.NewCellBuffer(24, 6)
		s.Paint(buf, 0, 0, ui.PaintContext{})
		var out strings.Builder
		for y := 0; y < 6; y++ {
			for x := 0; x < 20; x++ {
				out.WriteRune(buf.Get(x, y).Char)
			}
			out.WriteByte('\n')
		}
		return out.String()
	}
	s.Handle(ui.EventContext{}, keyEvent(tea.KeyPgUp, tea.ModShift, ""))
	settle()
	if s.view.Pinned() {
		t.Fatal("viewer page up still follows tail")
	}
	// Viewer scrolling never gains input/resize authority.
	if s.Controlled() {
		t.Fatal("fixture unexpectedly controls input")
	}
	before := paint()
	s.view.SelectionPress(0, 0, 1)
	s.view.SelectionDrag(7, 0)
	wantCopy, ok := s.view.SelectionText()
	if !ok || !strings.Contains(wantCopy, "row-") {
		t.Fatal("missing history selection")
	}
	for i := 80; i < 84; i++ {
		e.Feed([]byte(fmt.Sprintf("row-%04d\r\n", i)))
		gen++
		s.SetProjection(read(s.ViewportRead()))
		if got := paint(); got != before {
			t.Fatalf("primary scrolled viewport changed before own eviction: before=%q after=%q", before, got)
		}
		if got, ok := s.view.SelectionText(); !ok || got != wantCopy {
			t.Fatalf("selection identity changed: %q %v", got, ok)
		}
		buf := surface.NewCellBuffer(24, 6)
		s.Paint(buf, 0, 0, ui.PaintContext{})
		for _, c := range buf.Cells() {
			if c.Attr&cell.AttrReverse != 0 {
				t.Fatal("live cursor leaked into history viewport")
			}
		}
	}
	// Real shared scrollbar thumb/track routing must keep usable rows before
	// each async completion and preserve capture through streamed metadata.
	s.view.ClearSelection()
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(24, 6)
	host.Layout()
	if !s.syncScrollbackScrollbar() {
		t.Fatal("scrolled history scrollbar absent")
	}
	bar := s.scrollbar.Bounds()
	top, _ := s.scroll.ThumbBounds(bar.H)
	host.Handle(tea.MouseClickMsg{X: bar.X, Y: top, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: bar.X, Y: 0, Button: tea.MouseLeft})
	if !strings.Contains(paint(), "row-") {
		t.Fatal("thumb drag blanked viewport while page pending")
	}
	settle()
	e.Feed([]byte("row-0084\r\n"))
	gen++
	s.SetProjection(read(s.ViewportRead()))
	if !host.HasPointerCapture() {
		t.Fatal("stream metadata lost thumb capture")
	}
	host.Handle(tea.MouseReleaseMsg{X: bar.X, Y: 0, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("thumb release retained capture")
	}
	// Own oldest row is now evicted: clamp to first retained row; do not keep
	// a selection over unrelated coordinates or publish an empty page.
	s.view.SelectionPress(0, 0, 2_000_000_000)
	s.view.SelectionDrag(7, 0)
	e.Feed([]byte(strings.Repeat("replacement\r\n", 100)))
	gen++
	s.SetProjection(read(s.ViewportRead()))
	if s.view.HasSelection() || !strings.Contains(paint(), "replacement") {
		t.Fatal("eviction kept stale selection or blank viewport")
	}
	// Controller typing is still an explicit End-follow intent after scrolling.
	s.SetControlled(true)
	s.TakePendingCommands()
	s.Handle(ui.EventContext{}, keyEvent('x', 0, "x"))
	settle()
	if !s.view.Pinned() || s.ViewportRead().Start != -1 {
		t.Fatal("typing did not restore follow")
	}
}

func TestCopyPageResetRaceRecoversDesiredViewportWithoutRetryOrCopy(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{Cols: 24, Rows: 6})
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Feed([]byte(strings.Repeat("old-history\r\n", 20)))
	s := New(theme.DefaultDark(), "copy-race", "Copy")
	defer s.Close()
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 24, H: 6})
	g, err := e.Rows(terminalvt.RowRequest{Start: -1})
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjection(workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "copy-race", Alive: true}, Grid: g, Generation: 1})
	s.SetCopy(func(string) tea.Cmd { t.Fatal("stale reset selection copied"); return nil })
	s.Handle(ui.EventContext{}, keyEvent('a', tea.ModCtrl|tea.ModShift, ""))
	s.Handle(ui.EventContext{}, keyEvent('c', tea.ModCtrl|tea.ModShift, ""))
	commands := s.TakePendingCommands()
	if len(commands) != 1 || commands[0].Count == 0 {
		t.Fatalf("copy intent=%+v", commands)
	}
	c := commands[0]
	e.Feed([]byte("\x1bc" + strings.Repeat("new-identity\r\n", 10)))
	g, err = e.Rows(terminalvt.RowRequest{Start: c.Start, Count: c.Count, ViewportStart: c.ViewportStart, HistoryEpoch: c.HistoryEpoch, HistoryBase: c.HistoryBase})
	if err != nil {
		t.Fatal(err)
	}
	if !s.SetRows(c.Sequence, workspace.TerminalProjection{Info: workspace.TerminalInfo{ID: "copy-race", Alive: true}, Grid: g, Generation: 2}) {
		t.Fatal("whole recovery viewport rejected")
	}
	if s.view.HasSelection() || s.TakePendingCopy() != nil || len(s.TakePendingCommands()) != 0 {
		t.Fatal("reset copy kept stale selection or queued recovery loop")
	}
	buf := surface.NewCellBuffer(24, 6)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(0, 0).Char != 'n' || !s.view.Pinned() {
		t.Fatal("reset recovery did not paint complete desired tail")
	}
}
