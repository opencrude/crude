package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
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
	framed.Render(surf, &widget.RenderContext{Theme: th, Focused: true})

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
	// hidden lower body — hit-testing must return the frame/root, not body.
	if id := host.HitTest(5, 2); id != "body" {
		t.Fatalf("hit in revealed area=%q, want body", id)
	}
	if id := host.HitTest(5, 8); id == "body" {
		t.Fatal("hit below revealed area must not target body")
	}
}


var _ WindowEventTarget = (*testFrameEventTarget)(nil)
