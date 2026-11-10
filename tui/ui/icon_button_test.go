package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestIconButtonsShareHoverFocusTooltipAndNativeActivation(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	for _, glyph := range []string{"⧉", "⇱", "⌄", ""} {
		button := NewIconButton("action", glyph, "Panel action", "do-action", tokens)
		button.Layout(LayoutContext{}, Rect{W: 4, H: 1})
		buffer := surface.NewCellBuffer(4, 1)
		button.Paint(buffer, 0, 0, PaintContext{Hovered: true})
		if buffer.Get(0, 0).BG != tokens.Color(ColorHover) {
			t.Fatal("icon has no shared hover treatment", glyph)
		}
		button.Paint(buffer, 0, 0, PaintContext{})
		if buffer.Get(0, 0).BG != tokens.Color(ColorBg) {
			t.Fatal("hover remained after pointer leave", glyph)
		}
		if text, _, ok := button.Tooltip(); !ok || text != "Panel action" {
			t.Fatal("missing tooltip")
		}
		for _, event := range []Event{KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}), MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft})} {
			result := button.Handle(EventContext{}, event)
			if result.Action == nil || result.Action.ID != "do-action" {
				t.Fatal("native icon activation failed")
			}
		}
	}
}
