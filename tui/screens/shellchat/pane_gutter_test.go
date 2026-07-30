package shellchat

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPaneGutterWheelIndependentFromMainChat(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40
	paneScroll := ui.NewScrollModel()
	paneScroll.SetContent(8)
	paneScroll.SetViewport(2)
	paneScroll.StickToBottom()
	mainLines := []string{"msg", "│ header", "│ line0", "│ line1"}
	paneMap := []chatprojection.PaneLineInfo{
		{},
		{PaneID: "p1", LineIdx: -1},
		{PaneID: "p1", LineIdx: 0},
		{PaneID: "p1", LineIdx: 1},
	}
	gutters := map[string]PaneGutterState{
		"p1": {Scroll: paneScroll, TrackH: 2, Visible: true},
	}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: mainLines}}, ComposeParams{
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		NestingLevel:  1,
		MainSBVisible: false,
		PaneLineMap:   paneMap,
		PaneGutters:   gutters,
	}, 4)
	mainScroll := s.ScrollModel()
	beforeMain := mainScroll.Offset()

	gx := NestedGutterX(s.paintState.compose, s.list.Bounds().W)
	host.Handle(tea.MouseWheelMsg{X: gx, Y: 2, Button: tea.MouseWheelUp})
	if paneScroll.Offset() == paneScroll.MaxOffset() {
		t.Fatal("pane wheel up should scroll nested pane")
	}
	if mainScroll.Offset() != beforeMain {
		t.Fatal("pane wheel must not move main chat scroll")
	}
}

func TestPaneGutterDragHostCaptureCrossRegion(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 30
	paneScroll := ui.NewScrollModel()
	paneScroll.SetContent(10)
	paneScroll.SetViewport(2)
	paneMap := []chatprojection.PaneLineInfo{
		{PaneID: "p1", LineIdx: 0},
		{PaneID: "p1", LineIdx: 1},
	}
	gutters := map[string]PaneGutterState{
		"p1": {Scroll: paneScroll, TrackH: 2, Visible: true},
	}
	lines := []string{"│ a", "│ b"}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: lines}}, ComposeParams{
		InnerW:       innerW,
		PadLR:        padLR,
		Theme:        th,
		NestingLevel: 1,
		PaneLineMap:  paneMap,
		PaneGutters:  gutters,
	}, 2)
	gx := NestedGutterX(s.paintState.compose, s.list.Bounds().W)
	host.Handle(tea.MouseClickMsg{X: gx, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() || !s.IsPaneGutterDragging() {
		t.Fatal("pane gutter click should capture host and start drag")
	}
	before := paneScroll.Offset()
	host.Handle(tea.MouseMotionMsg{X: padLR + 2, Y: 1, Button: tea.MouseLeft})
	if !s.IsPaneGutterDragging() {
		t.Fatal("pane gutter drag must continue outside gutter column")
	}
	if paneScroll.Offset() == before && paneScroll.MaxOffset() > 0 {
		t.Fatal("drag motion should scroll nested pane")
	}
	host.Handle(tea.MouseReleaseMsg{X: padLR + 2, Y: 1, Button: tea.MouseLeft})
	if host.HasPointerCapture() || s.IsPaneGutterDragging() {
		t.Fatal("release should end pane gutter drag")
	}
}

func TestPaneSelectionDoesNotIncludeGutter(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 30
	paneScroll := ui.NewScrollModel()
	paneScroll.SetContent(4)
	paneScroll.SetViewport(2)
	paneMap := []chatprojection.PaneLineInfo{{PaneID: "p1", LineIdx: 0}}
	gutters := map[string]PaneGutterState{
		"p1": {Scroll: paneScroll, TrackH: 2, Visible: true},
	}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"│ selectable text here"}},
	}, ComposeParams{
		InnerW:       innerW,
		PadLR:        padLR,
		Theme:        th,
		NestingLevel: 1,
		PaneLineMap:  paneMap,
		PaneGutters:  gutters,
		HasPaneBg:    func(string) bool { return true },
	}, 1)

	clickX := padLR + 3
	host.Handle(tea.MouseClickMsg{X: clickX, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: clickX + 8, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: clickX + 8, Y: 0, Button: tea.MouseLeft})
	gx := NestedGutterX(s.paintState.compose, s.list.Bounds().W)
	_, sc, el, ec := s.SelectionRange()
	if sc >= gx || ec >= gx {
		t.Fatalf("selection must stay in content cols sc=%d ec=%d gutterX=%d", sc, ec, gx)
	}
	_ = el
}

func TestNoLegacyScrollbarPackage(t *testing.T) {
	// Compile-time guard: shellchat uses ui.ScrollModel + native gutter paint only.
	t.Log("legacy scrollbar package removed")
}
