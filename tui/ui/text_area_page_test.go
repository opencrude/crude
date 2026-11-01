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

func TestTextAreaWheelReadingSurvivesGrowingViewport(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("readOnly=%t", readOnly), func(t *testing.T) {
			area := NewTextArea("growing-reader", NewTokenSet(theme.DefaultDark()), true)
			area.SetReadOnly(readOnly)
			var text strings.Builder
			for i := range 30 {
				fmt.Fprintf(&text, "ROW_%02d\n", i)
			}
			area.SetValue(text.String())
			area.SetCursorFlat(0)
			host := NewHost(theme.DefaultDark(), area)
			host.Resize(30, 2)
			host.Layout()
			host.Handle(tea.MouseWheelMsg{X: 3, Y: 1, Button: tea.MouseWheelDown})
			if area.ScrollOffset() != 3 {
				t.Fatal("native wheel did not begin reading away from the caret")
			}
			// WindowFrame's reveal grows the body between input and later
			// frames. More space cannot hide a caret that was already visible,
			// and must not discard an explicit wheel reading position.
			for _, height := range []int{4, 6, 8} {
				host.Resize(30, height)
				host.Layout()
				buffer := surface.NewCellBuffer(30, height)
				host.Paint(buffer)
				var first strings.Builder
				for x := range 30 {
					first.WriteRune(buffer.Get(x, 0).Char)
				}
				if area.ScrollOffset() != 3 || !strings.Contains(first.String(), "ROW_03") {
					t.Fatalf("growing viewport discarded native wheel reading: height=%d offset=%d painted=%q",
						height, area.ScrollOffset(), first.String())
				}
			}
			if area.CursorFlat() != 0 || area.Value() != text.String() || area.HasActiveSelection() {
				t.Fatal("viewport growth changed text, caret or selection")
			}
			host.Focus(area.ID())
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			if area.ScrollOffset() != 1 || area.CursorVisualLine() != 1 {
				t.Fatal("an actual caret gesture did not resume native caret-following")
			}
		})
	}
}

func TestTextAreaFirstNarrowLayoutRevealsRetainedCaret(t *testing.T) {
	area := NewTextArea("narrow-reader", NewTokenSet(theme.DefaultDark()), true)
	area.SetValue("a\nb\nc")
	area.SetCursorFlat(5)
	host := NewHost(theme.DefaultDark(), area)
	host.Resize(1, 2)
	host.Layout()
	buffer := surface.NewCellBuffer(1, 2)
	host.Paint(buffer)
	if area.ScrollOffset() != 1 || area.CursorFlat() != 5 ||
		buffer.Get(0, 0).Char != 'b' || buffer.Get(0, 1).Char != 'c' {
		t.Fatal("first narrow viewport did not reveal the retained native caret")
	}
}
