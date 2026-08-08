package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// TestTextFieldProgrammaticValueShowsHead: when a long value is replaced
// programmatically (e.g. provider switch syncs the base URL), the field must
// display the START of the value, not scroll to the tail at the cursor.
func TestTextFieldProgrammaticValueShowsHead(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	f := NewTextField("f", tokens)
	const url = "https://api.groq.com/openai/v1"

	f.SetValue(url)
	f.Layout(LayoutContext{Theme: theme.DefaultDark()}, Rect{W: 14, H: 1})
	buf := surface.NewCellBuffer(14, 1)
	f.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	got := textFieldRowText(buf)
	t.Logf("after SetValue, 14-wide paints: %q", got)
	if !strings.HasPrefix(got, "https://") {
		t.Fatalf("programmatic value must show the head, paints %q", got)
	}
}

func TestTextFieldUserTypingScrollsToCursor(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	f := NewTextField("f", tokens)
	const url = "https://api.groq.com/openai/v1"

	f.SetValue(url)
	f.Layout(LayoutContext{Theme: theme.DefaultDark()}, Rect{W: 14, H: 1})

	// User moves the cursor to the end and types: viewport must track the
	// cursor (tail visible), not stay pinned at the head.
	f.Handle(EventContext{Theme: theme.DefaultDark(), Focused: true, LocalX: 0, LocalY: 0}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
	f.Handle(EventContext{Theme: theme.DefaultDark(), Focused: true}, KeyEvent(tea.KeyPressMsg{Code: '1', Text: "1"}))
	f.Layout(LayoutContext{Theme: theme.DefaultDark()}, Rect{W: 14, H: 1})
	buf := surface.NewCellBuffer(14, 1)
	f.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	got := textFieldRowText(buf)
	t.Logf("after End + typing, paints: %q", got)
	if !strings.Contains(got, "v11") {
		t.Fatalf("user typing must show the tail near the cursor, paints %q", got)
	}
}

func textFieldRowText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, 0); c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimSpace(b.String())
}
