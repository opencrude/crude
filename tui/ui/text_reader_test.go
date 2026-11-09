package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextReaderScrollAndResizeKeepTextReachable(t *testing.T) {
	reader := NewTextReader("reader", NewTokenSet(theme.DefaultDark()))
	text := reader.TextArea()
	message := strings.Repeat("A complete diagnostic line.\n", 30) + "Last line"
	text.SetValue(message)
	text.SetCursorFlat(0)
	host := NewHost(theme.DefaultDark(), reader)
	host.Resize(40, 8)
	host.Layout()
	host.Focus(text.ID())
	paint := func() *surface.CellBuffer {
		buf := surface.NewCellBuffer(40, 40)
		host.Paint(buf)
		return buf
	}
	if got := paint().Get(39, 0).Char; got != '┃' {
		t.Fatalf("overflow must paint a scrollbar thumb, got %q", got)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if text.ScrollOffset() == 0 {
		t.Fatal("PageDown did not advance the reader")
	}
	host.Handle(tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelUp})
	if text.ScrollOffset() != 4 {
		t.Fatalf("wheel did not continue from the keyboard reading position: %d", text.ScrollOffset())
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	// Drag the same scrollbar, then release at the bottom. It must expose the
	// actual final message line, not just move a disconnected thumb.
	host.Handle(tea.MouseClickMsg{X: 39, Y: 0, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 39, Y: 7, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: 39, Y: 7, Button: tea.MouseLeft})
	if text.ScrollOffset() != 23 || host.HasPointerCapture() {
		t.Fatalf("scrollbar did not reach the last page or release: offset=%d", text.ScrollOffset())
	}
	buf := paint()
	var line strings.Builder
	for x := 0; x < 39; x++ {
		line.WriteRune(buf.Get(x, 7).Char)
	}
	if !strings.Contains(line.String(), "Last line") {
		t.Fatal("last line is not painted after dragging")
	}
	host.Resize(40, 40)
	host.Layout()
	if reader.scrollbar.Bounds().W != 0 || text.Bounds().W != 40 || text.ScrollOffset() != 0 {
		t.Fatal("expansion did not remove unnecessary scrolling and restore text width")
	}
	host.Focus(text.ID())
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if text.SelectedText() != message {
		t.Fatal("reader selection did not preserve the complete message")
	}
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if text.Value() != message {
		t.Fatal("reader accepted an edit")
	}
}

func TestTextReaderSeverityInkSurvivesWrappingSelectionAndTheme(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	reader := NewTextReader("colored-reader", tokens)
	text := reader.TextArea()
	message := "✕ A long error message that wraps.\n\n⚠ Warning"
	text.SetValue(message)
	text.SetRuneColors(map[int]SemanticColor{
		0: ColorError, len([]rune("✕ A long error message that wraps.\n\n")): ColorWarning,
	})
	text.SetCursorFlat(text.Len())
	host := NewHost(theme.DefaultDark(), reader)
	paint := func() *surface.CellBuffer {
		host.Layout()
		buf := surface.NewCellBuffer(50, 12)
		host.Paint(buf)
		return buf
	}
	for _, width := range []int{20, 50} {
		host.Resize(width, 12)
		buf := paint()
		if c := buf.Get(0, 0); c.Char != '✕' || !c.FG.Equal(tokens.Color(ColorError)) {
			t.Fatalf("error glyph lost severity ink after reflow: %+v", c)
		}
	}
	host.Focus(text.ID())
	host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if text.SelectedText() != message {
		t.Fatal("colored reader changed the selected copy text")
	}
	tokens = NewTokenSet(theme.DefaultLight())
	reader.SetTokens(tokens)
	if c := paint().Get(0, 0); !c.FG.Equal(tokens.Color(ColorError)) {
		// Selection may adjust ink to keep contrast, but never hide the icon.
		fg, _ := tokens.TextSelectionColors(tokens.Color(ColorError))
		if !c.FG.Equal(fg) {
			t.Fatalf("selected glyph did not follow the new theme: %+v", c)
		}
	}
	text.SetValue("Replacement")
	if c := paint().Get(0, 0); c.FG.Equal(tokens.Color(ColorError)) {
		t.Fatal("replacement text retained a retired diagnostic color")
	}
}
