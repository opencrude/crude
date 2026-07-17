package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type framedLegacyTarget struct {
	last tea.Msg
}

func (t *framedLegacyTarget) Measure(availW, availH int) (int, int) {
	return availW, availH
}

func (t *framedLegacyTarget) Render(surf *surface.Surface, _ *widget.RenderContext) {
	for y := 0; y < surf.Height(); y++ {
		for x := 0; x < surf.Width(); x++ {
			surf.Buffer().Set(x, y, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
		}
	}
}

func (t *framedLegacyTarget) HandleEvent(msg tea.Msg) tea.Cmd {
	t.last = msg
	return nil
}

func TestWindowFrameWidgetPaintsOnlyUnifiedChrome(t *testing.T) {
	th := theme.DefaultDark()
	target := &framedLegacyTarget{}
	body := NewLegacyWidget("body", target)
	framed := NewWindowFrameWidget(
		th,
		"frame",
		FrameDialog,
		"Unified",
		body,
		target,
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
		t.Fatalf("body cell=%q, want legacy body inside frame content", got)
	}
}

func TestWindowFrameWidgetTranslatesLegacyPointerToContent(t *testing.T) {
	th := theme.DefaultDark()
	target := &framedLegacyTarget{}
	body := NewLegacyWidget("body", target)
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
		t.Fatalf("legacy target received %T", target.last)
	}
	// FrameDialog content begins at (3,1): border + two-cell horizontal pad.
	if click.X != 2 || click.Y != 2 {
		t.Fatalf("translated click=(%d,%d), want (2,2)", click.X, click.Y)
	}
}

var _ widget.Widget = (*framedLegacyTarget)(nil)
var _ WindowEventTarget = (*framedLegacyTarget)(nil)
