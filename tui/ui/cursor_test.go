package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/wm"
)

func TestPaintBlockCursorPreservesWideGraphemeSpan(t *testing.T) {
	buf := surface.NewCellBuffer(4, 1)
	buf.SetSpan(0, 0, cell.NewWideCell('界', cell.White, cell.Black, 0))
	buf.Set(2, 0, cell.NewCell('X', cell.White, cell.Black, 0))

	PaintBlockCursor(buf, 0, 0, cell.White, cell.Black)
	primary := buf.Get(0, 0)
	continuation := buf.Get(1, 0)
	if primary.Char != '界' || primary.Width != 2 || !primary.Attr.Has(cell.AttrReverse) {
		t.Fatalf("wide cursor primary=%+v", primary)
	}
	if continuation.Width != 0 || !continuation.Attr.Has(cell.AttrReverse) {
		t.Fatalf("wide cursor continuation=%+v", continuation)
	}
	if next := buf.Get(2, 0); next.Char != 'X' || next.Attr.Has(cell.AttrReverse) {
		t.Fatalf("cursor escaped grapheme span: %+v", next)
	}
}

func TestCursorBlinkStateUsesSharedIdleFramePhase(t *testing.T) {
	var state CursorBlinkState
	state.ResetAt(0)
	if !state.Visible(799) {
		t.Fatal("cursor hid during activity hold")
	}
	if state.Visible(1200) {
		t.Fatal("cursor did not enter shared off phase")
	}
	if !state.Visible(1600) {
		t.Fatal("cursor did not return on shared on phase")
	}
	state.MarkActivity()
	if !state.Visible(1200) {
		t.Fatal("activity did not immediately reveal cursor")
	}
}

func TestBlockCursorUsesSofterSemanticCursorColor(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(2, 1)
	buf.Set(0, 0, cell.NewCell('x', tokens.Color(ColorFg), tokens.Color(ColorBg), 0))
	PaintBlockCursor(buf, 0, 0, tokens.Color(ColorCursor), tokens.Color(ColorBg))
	got := buf.Get(0, 0)
	if !got.FG.Equal(tokens.Color(ColorCursor)) || got.FG.Equal(tokens.Color(ColorFg)) || !got.Attr.Has(cell.AttrReverse) {
		t.Fatalf("cursor cell=%+v", got)
	}
}

func TestFloatingComponentReceivesSharedFrameClock(t *testing.T) {
	th := theme.DefaultDark()
	field := NewTextField("field", NewTokenSet(th))
	field.SetValue("x")
	field.Edit().SetCursor(0, false)
	field.cursor.ResetAt(0)
	widget := NewComponentWidget(th, field, 8, 1)
	widget.Focus("field")
	surf := surface.NewSurface("field", 8, 1, cell.TransparentBlank())
	widget.Render(surf, &wm.RenderContext{Theme: th, SpinFrame: 1200})
	if surf.Buffer().Get(0, 0).Attr.Has(cell.AttrReverse) {
		t.Fatal("floating field ignored shared off-phase frame")
	}
	widget.Render(surf, &wm.RenderContext{Theme: th, SpinFrame: 1600})
	if !surf.Buffer().Get(0, 0).Attr.Has(cell.AttrReverse) {
		t.Fatal("floating field ignored shared on-phase frame")
	}
}

func TestPaintBlockCursorAtEmptyCellDoesNotAdvanceContent(t *testing.T) {
	buf := surface.NewCellBuffer(4, 1)
	buf.Set(0, 0, cell.NewCell('a', cell.White, cell.Black, 0))
	buf.Set(1, 0, cell.NewCell('b', cell.White, cell.Black, 0))

	PaintBlockCursor(buf, 2, 0, cell.White, cell.Black)
	if c := buf.Get(2, 0); c.Char != ' ' || c.Width != 1 || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("empty cursor cell=%+v", c)
	}
	if buf.Get(0, 0).Char != 'a' || buf.Get(1, 0).Char != 'b' {
		t.Fatal("empty cursor shifted existing content")
	}
}
