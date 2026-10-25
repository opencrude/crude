package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

type testFrameBody struct {
	Base
}

func (b *testFrameBody) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (b *testFrameBody) Layout(_ LayoutContext, r Rect) {
	b.SetBounds(r)
}

func (b *testFrameBody) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	r := b.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			buf.Set(ox+r.X+x, oy+r.Y+y, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
		}
	}
}

func (b *testFrameBody) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

type testFrameEventTarget struct {
	last tea.Msg
}

func (t *testFrameEventTarget) HandleEvent(msg tea.Msg) tea.Cmd {
	t.last = msg
	return nil
}

func TestWindowFrameWidgetPaintsOnlyUnifiedChrome(t *testing.T) {
	th := theme.DefaultDark()
	body := &testFrameBody{Base: NewBase("body")}
	framed := NewWindowFrameWidget(
		th,
		"frame",
		FrameDialog,
		"Unified",
		body,
		nil,
		true,
		20,
		8,
	)
	surf := surface.NewSurface("window", 20, 8, cell.TransparentBlank())
	framed.Render(surf, &wm.RenderContext{Theme: th, Focused: true})

	buf := surf.Buffer()
	if got := buf.Get(0, 0).Char; got != '╭' {
		t.Fatalf("top-left chrome=%q, want rounded WindowFrame", got)
	}
	if got := buf.Get(19, 7).Char; got != '╯' {
		t.Fatalf("bottom-right chrome=%q, want rounded WindowFrame", got)
	}
	if got := buf.Get(18, 0).Char; got != '✕' {
		t.Fatalf("close glyph=%q, want WindowFrame close", got)
	}
	if got := buf.Get(3, 1).Char; got != 'X' {
		t.Fatalf("body cell=%q, want native body inside frame content", got)
	}
}

func TestWindowFrameWidgetHoverEdgeHighlightsBorder(t *testing.T) {
	th := theme.DefaultDark()
	body := &testFrameBody{Base: NewBase("body")}
	framed := NewWindowFrameWidget(
		th,
		"frame",
		FrameDialog,
		"Unified",
		body,
		nil,
		true,
		20,
		8,
	)
	tokens := NewTokenSet(th)
	accent := tokens.Color(ColorAccent)
	normal := tokens.Color(ColorPopupBorder)

	// No hover: the bottom-left border cell uses the normal border color.
	surf := surface.NewSurface("window", 20, 8, cell.TransparentBlank())
	framed.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	if got := surf.Buffer().Get(1, 7).FG; got == accent {
		t.Fatalf("idle bottom border cell FG=%v, must not be accent", got)
	}

	// Hover on the bottom edge: the bottom border paints with the accent.
	framed.SetHoverEdge(wm.EdgeBottom)
	surf = surface.NewSurface("window", 20, 8, cell.TransparentBlank())
	framed.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	buf := surf.Buffer()
	for x := 1; x < 19; x++ {
		if got := buf.Get(x, 7).FG; got != accent {
			t.Fatalf("bottom hover cell (%d,7) FG=%v, want accent %v", x, got, accent)
		}
	}
	// The left border above the bottom edge stays normal.
	if got := buf.Get(0, 3).FG; got != normal && got == accent {
		t.Fatalf("left border FG=%v, want non-accent", got)
	}
	// Clearing hover restores the normal border.
	framed.SetHoverEdge(wm.EdgeNone)
	surf = surface.NewSurface("window", 20, 8, cell.TransparentBlank())
	framed.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	if got := surf.Buffer().Get(1, 7).FG; got == accent {
		t.Fatalf("cleared hover bottom border FG=%v, must not be accent", got)
	}
}

// Regression: hovering a corner cell focuses TWO adjacent borders at once
// (FloatingWindow.HitTestEdge reports Edge*Corner). The corner-hover paint
// used to repaint the whole top/bottom row and left/right column with
// straight ─/│ glyphs, flattening the far corners of those runs into │ or ─.
// Corner cells must be recolored only; glyphs stay rounded, and only the
// corners adjacent to a hovered border lift to the accent.
func TestWindowFrameCornerHoverKeepsCurveGlyphs(t *testing.T) {
	th := theme.DefaultDark()
	body := &testFrameBody{Base: NewBase("body")}
	framed := NewWindowFrameWidget(
		th,
		"frame",
		FrameDialog,
		"", // no title/close chrome between the pointer and row-0 inspection
		body,
		nil,
		false,
		20,
		8,
	)
	tokens := NewTokenSet(th)
	accent := tokens.Color(ColorAccent)

	type corner struct {
		x, y int
		ch   rune
	}
	corners := []corner{{0, 0, '╭'}, {19, 0, '╮'}, {0, 7, '╰'}, {19, 7, '╯'}}
	edgeMids := []corner{{10, 0, '─'}, {19, 4, '│'}, {10, 7, '─'}, {0, 4, '│'}} // T R B L order

	cases := []struct {
		edge         wm.Edge
		glyphCorners []int // corner cells expected accent (adjacent to hovered borders)
		runMids      []int // edge-mid cells expected accent (the hovered runs)
	}{
		{wm.EdgeTopLeft, []int{0, 1, 2}, []int{0, 3}},
		{wm.EdgeTopRight, []int{0, 1, 3}, []int{0, 1}},
		{wm.EdgeBottomLeft, []int{0, 2, 3}, []int{2, 3}},
		{wm.EdgeBottomRight, []int{1, 2, 3}, []int{1, 2}},
	}

	for _, tc := range cases {
		framed.SetHoverEdge(tc.edge)
		surf := surface.NewSurface("window", 20, 8, cell.TransparentBlank())
		framed.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
		buf := surf.Buffer()

		// Every corner keeps its rounded glyph (never reshaped to ─ or │).
		for _, c := range corners {
			if got := buf.Get(c.x, c.y).Char; got != c.ch {
				t.Fatalf("%v corner (%d,%d) glyph=%q, want %q", tc.edge, c.x, c.y, got, c.ch)
			}
		}

		lifted := map[int]bool{}
		for _, i := range tc.glyphCorners {
			lifted[i] = true
			if got := buf.Get(corners[i].x, corners[i].y).FG; got != accent {
				t.Fatalf("%v adjacent corner (%d,%d) FG=%v, want accent", tc.edge, corners[i].x, corners[i].y, got)
			}
		}
		for i, c := range corners {
			if lifted[i] {
				continue
			}
			if got := buf.Get(c.x, c.y).FG; got == accent {
				t.Fatalf("%v opposite corner (%d,%d) FG=%v, must stay non-accent", tc.edge, c.x, c.y, got)
			}
		}

		hoverRuns := map[int]bool{}
		for _, i := range tc.runMids {
			hoverRuns[i] = true
			if got := buf.Get(edgeMids[i].x, edgeMids[i].y).FG; got != accent {
				t.Fatalf("%v run cell (%d,%d) FG=%v, want accent", tc.edge, edgeMids[i].x, edgeMids[i].y, got)
			}
		}
		for i, c := range edgeMids {
			if hoverRuns[i] {
				continue
			}
			if got := buf.Get(c.x, c.y).FG; got == accent {
				t.Fatalf("%v non-hovered run cell (%d,%d) FG=%v, must stay non-accent", tc.edge, c.x, c.y, got)
			}
		}
	}
}

func TestWindowFrameWidgetTranslatesLegacyPointerToContent(t *testing.T) {
	th := theme.DefaultDark()
	body := &testFrameBody{Base: NewBase("body")}
	target := &testFrameEventTarget{}
	framed := NewWindowFrameWidget(
		th,
		"frame",
		FrameDialog,
		"Dialog",
		body,
		target,
		true,
		20,
		8,
	)

	framed.HandleEvent(tea.MouseClickMsg{X: 5, Y: 3, Button: tea.MouseLeft})
	click, ok := target.last.(tea.MouseClickMsg)
	if !ok {
		t.Fatalf("event target received %T", target.last)
	}
	// FrameDialog content begins at (3,1): border + two-cell horizontal pad.
	if click.X != 2 || click.Y != 2 {
		t.Fatalf("translated click=(%d,%d), want (2,2)", click.X, click.Y)
	}
}

func TestWindowFrameRevealCollapsesContentHeight(t *testing.T) {
	body := &testFrameBody{Base: NewBase("body")}
	frame := NewWindowFrame("frame", FrameWindow, NewTokenSet(theme.DefaultDark()))
	frame.SetContent(body)
	frame.SetReveal(0)
	frame.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 10})

	// Full content height = H - top border - bottom border = 10-2 = 8.
	// At reveal 0 the body must collapse to zero height (chrome stays).
	if got := body.Bounds().H; got != 0 {
		t.Fatalf("reveal=0 body height=%d, want 0", got)
	}
	if got := frame.contentR.H; got != 8 {
		t.Fatalf("content rect H=%d, want 8 (chrome unaffected)", got)
	}
}

func TestWindowFrameWidgetRevealRelayoutsRetainedPaintAndHitGeometry(t *testing.T) {
	th := theme.DefaultDark()
	body := &testFrameBody{Base: NewBase("reveal-body")}
	widget := NewWindowFrameWidget(th, "frame", FrameWindow, "Reveal", body, nil, true, 20, 10)
	surf := surface.NewSurface("reveal", 20, 10, cell.TransparentBlank())
	for _, step := range []struct {
		progress float64
		height   int
	}{{0, 0}, {0.5, 4}, {1, 8}, {0, 0}, {1, 8}} {
		widget.SetReveal(step.progress)
		widget.Render(surf, &wm.RenderContext{Theme: th})
		if body.Bounds().H != step.height {
			t.Fatalf("reveal=%v retained height=%d want %d", step.progress, body.Bounds().H, step.height)
		}
		for y := 1; y < 9; y++ {
			painted := surf.Buffer().Get(5, y).Char == 'X'
			hit := widget.Host().HitTest(5, y) == body.ID()
			want := y <= step.height
			if painted != want || hit != want {
				t.Fatalf("reveal=%v row=%d painted=%v hit=%v want=%v", step.progress, y, painted, hit, want)
			}
		}
	}
}

func TestWindowFrameRevealScalesContentHeight(t *testing.T) {
	body := &testFrameBody{Base: NewBase("body")}
	frame := NewWindowFrame("frame", FrameWindow, NewTokenSet(theme.DefaultDark()))
	frame.SetContent(body)
	frame.SetReveal(0.5)
	frame.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 10})

	if got := body.Bounds().H; got != 4 {
		t.Fatalf("reveal=0.5 body height=%d, want 4 (8*0.5)", got)
	}
}

func TestWindowFrameRevealFullShowsAllContent(t *testing.T) {
	body := &testFrameBody{Base: NewBase("body")}
	frame := NewWindowFrame("frame", FrameWindow, NewTokenSet(theme.DefaultDark()))
	frame.SetContent(body)
	frame.SetReveal(1)
	frame.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 10})

	if got := body.Bounds().H; got != 8 {
		t.Fatalf("reveal=1 body height=%d, want 8 (full)", got)
	}
	if got := body.Bounds().Y; got != 1 {
		t.Fatalf("reveal=1 body Y=%d, want 1 (below title row)", got)
	}
}

func TestWindowFrameRevealClampsOutOfRange(t *testing.T) {
	frame := NewWindowFrame("frame", FrameWindow, NewTokenSet(theme.DefaultDark()))
	frame.SetReveal(-5)
	if got := frame.Reveal(); got != 0 {
		t.Fatalf("reveal(-5)=%v, want 0", got)
	}
	frame.SetReveal(7)
	if got := frame.Reveal(); got != 1 {
		t.Fatalf("reveal(7)=%v, want 1", got)
	}
}

func TestWindowFrameRevealHostHitTestMatchesGeometry(t *testing.T) {
	// The geometry that paints is the geometry that receives input: during
	// a half reveal, Host hit-testing must NOT target content rows below the
	// revealed area (they are not laid out).
	body := &testFrameBody{Base: NewBase("body")}
	frame := NewWindowFrame("frame", FrameWindow, NewTokenSet(theme.DefaultDark()))
	frame.SetContent(body)
	frame.SetReveal(0.5)
	host := NewHost(theme.DefaultDark(), frame)
	host.Resize(20, 10)
	host.Layout()

	// Revealed area: rows 1..4 (5 rows: title + 4 content). Row 5+ is the
	// hidden lower body; hit-testing must return the frame/root, not body.
	if id := host.HitTest(5, 2); id != "body" {
		t.Fatalf("hit in revealed area=%q, want body", id)
	}
	if id := host.HitTest(5, 8); id == "body" {
		t.Fatal("hit below revealed area must not target body")
	}
}

func TestWindowFrameTitleActionAfterLayoutEmitsAction(t *testing.T) {
	w := NewWindowFrameWidget(theme.DefaultDark(), "frame", FrameWindow, "Terminal",
		&testFrameBody{Base: NewBase("body")}, nil, true, 40, 12)
	// Install AFTER the constructor's initial layout: the hit region must be
	// valid immediately (regression: the glyph never painted and clicks fell
	// through because only Layout computed the rect).
	w.Frame().SetTitleAction("⇲", "Dock")
	r := w.Frame().TitleActionBounds()
	if r.Empty() {
		t.Fatal("title action bounds empty after SetTitleAction")
	}
	if want := (Rect{X: 40 - 6, Y: 0, W: 3, H: 1}); r != want {
		t.Fatalf("title action rect=%+v want %+v (left of close)", r, want)
	}
	res := w.Frame().Handle(EventContext{LocalX: r.X + 1, LocalY: 0},
		MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: r.X + 1, Y: 0}))
	if res.Action == nil || res.Action.ID != "window.titleaction" {
		t.Fatalf("title action click result=%+v want window.titleaction", res)
	}
	close := w.Frame().Handle(EventContext{LocalX: 40 - 2, LocalY: 0},
		MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: 40 - 2, Y: 0}))
	if close.Action == nil || close.Action.ID != "window.close" {
		t.Fatalf("close click result=%+v want window.close (unchanged)", close)
	}
}

var _ WindowEventTarget = (*testFrameEventTarget)(nil)
