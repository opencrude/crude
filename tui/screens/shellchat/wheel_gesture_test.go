package shellchat

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
	"fmt"
	"testing"
	"time"
)

func gestureFixture(t *testing.T) (*Screen, *ui.Host, *ui.ScrollModel, *time.Time) {
	t.Helper()
	th := theme.DefaultDark()
	pane := ui.NewScrollModel()
	pane.SetContent(50)
	pane.SetViewport(4)
	pane.ScrollTo(20)
	lines := make([]string, 40)
	mapping := make([]chatprojection.PaneLineInfo, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("outer-%02d", i)
	}
	for i := 20; i < 24; i++ {
		mapping[i] = chatprojection.PaneLineInfo{PaneID: "pane", LineIdx: i - 20}
		lines[i] = fmt.Sprintf("inner-%02d", i-20)
	}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m", Lines: lines}}, ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th, NestingLevel: 1, MainSBVisible: true, PaneLineMap: mapping, PaneGutters: map[string]PaneGutterState{"pane": {Scroll: pane, TrackH: 4, Visible: true}}}, 8)
	s.ScrollModel().SetAutoScroll(false)
	s.ScrollModel().ScrollTo(24)
	now := time.Unix(1, 0)
	s.wheelNow = func() time.Time { return now }
	return s, host, pane, &now
}
func TestOuterWheelContinuesWhenPaneMovesUnderStationaryPointer(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	for i := 0; i < 7; i++ {
		*now = now.Add(40 * time.Millisecond)
		host.Handle(tea.MouseWheelMsg{X: 5, Y: 2, Button: tea.MouseWheelUp})
		host.MarkLayout()
		host.Layout()
	}
	if pane.Offset() != 20 {
		t.Fatalf("nested pane hijacked ongoing conversation wheel: offset=%d", pane.Offset())
	}
	if s.ScrollModel().Offset() >= 18 {
		t.Fatal("outer failed to travel through pane")
	}
}
func TestDeliberatePaneWheelAndBoundaryChainRemainEasy(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	before := s.ScrollModel().Offset()
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() != 19 || s.ScrollModel().Offset() != before {
		t.Fatal("fresh direct pane wheel not independent")
	}
	pane.ScrollTo(1)
	*now = now.Add(40 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() != 0 || s.ScrollModel().Offset() >= before {
		t.Fatal("pane boundary swallowed remainder")
	}
	outer := s.ScrollModel().Offset()
	*now = now.Add(40 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if s.ScrollModel().Offset() >= outer {
		t.Fatal("boundary transfer re-trapped outer gesture")
	}
	*now = now.Add(time.Second)
	pane.ScrollTo(20)
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() >= 20 {
		t.Fatal("pause could not restart pane scrolling")
	}
}
func TestDirectGuttersAndSourceReplacementResetGesture(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 2, Button: tea.MouseWheelUp})
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	gx := s.nestedGutterX()
	*now = now.Add(10 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: gx, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() >= 20 {
		t.Fatal("direct gutter did not target pane")
	}
	s.ResetWheelGesture()
	*now = now.Add(10 * time.Millisecond)
	before := s.ScrollModel().Offset()
	host.Handle(tea.MouseWheelMsg{X: s.scrollbar.Bounds().X, Y: 3, Button: tea.MouseWheelUp})
	if s.ScrollModel().Offset() >= before {
		t.Fatal("main gutter failed")
	}
}

func TestStationaryGutterAndSlowNotchesDoNotHijackOuter(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	gx := s.nestedGutterX()
	for i := 0; i < 8; i++ {
		*now = now.Add(180 * time.Millisecond)
		host.Handle(tea.MouseWheelMsg{X: gx, Y: 2, Button: tea.MouseWheelUp})
		host.MarkLayout()
		host.Layout()
	}
	if pane.Offset() != 20 {
		t.Fatal("pane moving beneath stationary gutter stole slow outer scroll")
	}
	if s.ScrollModel().Offset() >= 18 {
		t.Fatal("slow outer gesture did not cross pane")
	}
}

func TestRecursiveWheelConservesDeltaAndInvalidatesExactAncestors(t *testing.T) {
	s, host, parent, now := gestureFixture(t)
	child := ui.NewScrollModel()
	child.SetContent(30)
	child.SetViewport(3)
	child.ScrollTo(1)
	parent.ScrollTo(1)
	s.paintState.compose.PaneGutters["child"] = PaneGutterState{Scroll: child, TrackH: 3, Visible: true}
	for i := 20; i < 24; i++ {
		s.paintState.paneLineMap[i] = chatprojection.PaneLineInfo{PaneID: "child", LineIdx: i - 20, Ancestors: []chatprojection.PaneTarget{{PaneID: "pane", LineIdx: i - 20}}}
	}
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	var invalidated []string
	s.onPaneScrollChain = func(ids []string) { invalidated = append(invalidated, ids...) }
	before := s.ScrollModel().Offset()
	// Warm the same direction on child, then place both near boundary. The
	// accelerated second event has two rows: exactly1 child +1 parent.
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	child.ScrollTo(1)
	parent.ScrollTo(1)
	invalidated = nil
	*now = now.Add(20 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if child.Offset() != 0 || parent.Offset() != 0 || s.ScrollModel().Offset() != before {
		t.Fatalf("delta duplicated/lost: child%d parent%d outer%d", child.Offset(), parent.Offset(), s.ScrollModel().Offset())
	}
	if len(invalidated) == 0 || invalidated[0] != "pane" {
		t.Fatalf("missing exact ancestry invalidation: %v", invalidated)
	}
	*now = now.Add(20 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if s.ScrollModel().Offset() >= before {
		t.Fatal("recursive boundary did not transfer outer")
	}
	outer := s.ScrollModel().Offset()
	*now = now.Add(20 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelDown})
	if s.ScrollModel().Offset() != outer+1 || child.Offset() != 0 {
		t.Fatal("direction reversal stole ownership or accelerated correction")
	}
}

func TestPaneModelReplacementAndStreamingDoNotShareOldGesture(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() != 19 {
		t.Fatal("setup")
	}
	// Ordinary generation advance preserves exact model lifetime.
	s.SetMessages(s.rows, PaintParams{SourceID: s.rowsSourceID, Generation: 2, Compose: s.paintState.compose, PaneLineMap: s.paintState.paneLineMap})
	*now = now.Add(40 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() >= 19 {
		t.Fatal("stream update broke valid pane gesture")
	}
	replacement := ui.NewScrollModel()
	replacement.SetContent(50)
	replacement.SetViewport(4)
	replacement.ScrollTo(20)
	s.paintState.compose.PaneGutters["pane"] = PaneGutterState{Scroll: replacement, TrackH: 4, Visible: true}
	before := s.ScrollModel().Offset()
	*now = now.Add(40 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if replacement.Offset() != 20 || s.ScrollModel().Offset() >= before {
		t.Fatal("replacement source inherited stale gesture instead of ancestor handoff")
	}
}

func TestCollapsedPaneAndHeaderCannotRetainOldWheelTarget(t *testing.T) {
	s, host, pane, now := gestureFixture(t)
	s.ScrollModel().ScrollTo(18)
	host.MarkLayout()
	host.Layout()
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	old := pane.Offset()
	for i := range s.paintState.paneLineMap {
		s.paintState.paneLineMap[i] = chatprojection.PaneLineInfo{}
	}
	before := s.ScrollModel().Offset()
	*now = now.Add(20 * time.Millisecond)
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp})
	if pane.Offset() != old || s.ScrollModel().Offset() >= before {
		t.Fatal("collapsed pane retained invisible scroll ownership")
	}
	// Changing modifier is fresh intent, without requiring a long idle pause.
	s.ResetWheelGesture()
	host.ResetWheelGesture()
	s.ScrollModel().ScrollTo(18)
	pane.ScrollTo(20)
	s.paintState.paneLineMap[21] = chatprojection.PaneLineInfo{PaneID: "pane", LineIdx: -1}
	host.MarkLayout()
	host.Layout()
	before = s.ScrollModel().Offset()
	host.Handle(tea.MouseWheelMsg{X: 5, Y: 3, Button: tea.MouseWheelUp, Mod: tea.ModShift})
	if pane.Offset() != 20 || s.ScrollModel().Offset() >= before {
		t.Fatal("pane header acquired body scroll")
	}
}
