package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextAreaFirstPageMovesViewportWithItsNativeCursor(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("readOnly=%t", readOnly), func(t *testing.T) {
			area := NewTextArea("page-reader", NewTokenSet(theme.DefaultDark()), true)
			area.SetReadOnly(readOnly)
			var text strings.Builder
			for i := range 30 {
				fmt.Fprintf(&text, "ROW_%02d\n", i)
			}
			area.SetValue(text.String())
			area.SetCursorFlat(0)
			host := NewHost(theme.DefaultDark(), area)
			host.Resize(30, 5)
			host.Layout()
			host.Focus(area.ID())
			host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
			buffer := surface.NewCellBuffer(30, 5)
			host.Paint(buffer)
			var first strings.Builder
			for x := range 30 {
				first.WriteRune(buffer.Get(x, 0).Char)
			}
			if area.ScrollOffset() != 4 || area.CursorVisualLine() != 4 || !strings.Contains(first.String(), "ROW_04") {
				t.Fatalf("first PageDown did not page the native viewport: offset=%d cursorRow=%d first=%q",
					area.ScrollOffset(), area.CursorVisualLine(), first.String())
			}
			if area.Value() != text.String() || area.HasActiveSelection() {
				t.Fatal("native page navigation edited or selected text")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp})
			if area.ScrollOffset() != 0 || area.CursorVisualLine() != 0 {
				t.Fatal("native PageUp did not restore the original viewport/cursor")
			}
		})
	}
}
