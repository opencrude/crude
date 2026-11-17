package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func paintArea(t *testing.T, ta *TextArea, w, h int) *surface.CellBuffer {
	t.Helper()
	ta.Layout(LayoutContext{}, Rect{W: w, H: h})
	buf := surface.NewCellBuffer(w, h)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	return buf
}

func newArea(value string) *TextArea {
	ta := NewTextArea("test/bidi", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue(value)
	return ta
}

func reversedCellX(buf *surface.CellBuffer, y int) int {
	for x, c := range buf.Row(y) {
		if c.Attr.Has(cell.AttrReverse) {
			return x
		}
	}
	return -1
}

func TestTextAreaRightAlignsAndJoinsPersianParagraph(t *testing.T) {
	ta := newArea("سلام")
	buf := paintArea(t, ta, 10, 2)
	// meem, alef, lam, seen: right-aligned, joined, right to left.
	if got := rowText(buf, 0); got != "      \ufee1\ufe8e\ufee0\ufeb3" {
		t.Fatalf("row = %q", got)
	}
	// SetValue leaves the caret at the end: the cell just left of the text.
	if x := reversedCellX(buf, 0); x != 5 {
		t.Fatalf("end-of-text caret at column %d, want 5", x)
	}
}

func TestTextAreaKeepsLeftToRightParagraphWithHebrewRun(t *testing.T) {
	ta := newArea("go אבג now")
	buf := paintArea(t, ta, 12, 1)
	if got := strings.TrimRight(rowText(buf, 0), " "); got != "go גבא now" {
		t.Fatalf("row = %q", got)
	}
}

func TestTextAreaClickMapsScreenColumnToTextOrder(t *testing.T) {
	ta := newArea("אבג")
	paintArea(t, ta, 6, 1)
	// Visual: "   גבא" — the cell at column 5 shows א, the first letter.
	if pos := ta.MouseToFlatPos(5, 0); pos != 0 {
		t.Fatalf("click on א gave offset %d, want 0", pos)
	}
	if pos := ta.MouseToFlatPos(3, 0); pos != 2 {
		t.Fatalf("click on ג gave offset %d, want 2", pos)
	}
	// Left of a right-to-left paragraph is past its end.
	if pos := ta.MouseToFlatPos(0, 0); pos != 3 {
		t.Fatalf("click left of the text gave offset %d, want 3", pos)
	}
}

func press(ta *TextArea, code rune, mod tea.KeyMod) {
	ta.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: code, Mod: mod}))
}

func TestTextAreaArrowsMoveOnScreenInRightToLeftText(t *testing.T) {
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	bidi.SetVisualCaret(true)
	ta := newArea("אבג")
	paintArea(t, ta, 6, 1)
	ta.SetCursorFlat(0) // on א, the rightmost letter
	press(ta, tea.KeyLeft, 0)
	if got := ta.CursorFlat(); got != 1 {
		t.Fatalf("Left from א moved to %d, want ב (1)", got)
	}
	press(ta, tea.KeyRight, 0)
	if got := ta.CursorFlat(); got != 0 {
		t.Fatalf("Right from ב moved to %d, want א (0)", got)
	}

	bidi.SetVisualCaret(false)
	press(ta, tea.KeyRight, 0)
	if got := ta.CursorFlat(); got != 1 {
		t.Fatalf("logical Right moved to %d, want 1", got)
	}
}

func TestTextAreaShiftArrowSelectsLogicalRange(t *testing.T) {
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	bidi.SetVisualCaret(true)
	ta := newArea("אבג")
	paintArea(t, ta, 6, 1)
	ta.SetCursorFlat(0)
	press(ta, tea.KeyLeft, tea.ModShift)
	press(ta, tea.KeyLeft, tea.ModShift)
	if got := ta.SelectedText(); got != "אב" {
		t.Fatalf("selected %q, want אב", got)
	}
	// The highlight covers the two rightmost letters on screen.
	buf := paintArea(t, ta, 6, 1)
	sel := NewTokenSet(theme.DefaultDark()).Color(ColorTextSelection)
	if !buf.Get(5, 0).BG.Equal(sel) || !buf.Get(4, 0).BG.Equal(sel) || buf.Get(3, 0).BG.Equal(sel) {
		t.Fatal("selection is not painted under א and ב")
	}
}

func TestTextAreaEmptyLineKeepsPreviousDirection(t *testing.T) {
	ta := newArea("سلام\n")
	buf := paintArea(t, ta, 8, 2)
	// The caret on the new empty line starts at the right edge.
	if x := reversedCellX(buf, 1); x != 7 {
		t.Fatalf("caret on the empty line at column %d, want 7", x)
	}
}

func TestTextAreaArrangementOffPaintsLogicalOrder(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	bidi.SetPresentation(bidi.Presentation{})
	ta := newArea("אבג")
	buf := paintArea(t, ta, 6, 1)
	if got := strings.TrimRight(rowText(buf, 0), " "); got != "אבג" {
		t.Fatalf("row = %q", got)
	}
}

func paintField(t *testing.T, tf *TextField, w int) *surface.CellBuffer {
	t.Helper()
	tf.Layout(LayoutContext{}, Rect{W: w, H: 1})
	buf := surface.NewCellBuffer(w, 1)
	tf.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	return buf
}

func TestTextFieldArrangesValueAndMapsPointer(t *testing.T) {
	tf := NewTextField("test/field", NewTokenSet(theme.DefaultDark()))
	tf.SetValue("שלום")
	buf := paintField(t, tf, 8)
	if got := rowText(buf, 0); got != "    םולש" {
		t.Fatalf("row = %q", got)
	}
	// The cell at column 7 shows ש, the first letter.
	if idx := tf.indexAtContentX(7); idx != 0 {
		t.Fatalf("pointer on ש gave index %d, want 0", idx)
	}
	if idx := tf.indexAtContentX(1); idx != 4 {
		t.Fatalf("pointer left of the text gave index %d, want the end", idx)
	}
}

func TestTextFieldArrowsMoveOnScreen(t *testing.T) {
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	bidi.SetVisualCaret(true)
	tf := NewTextField("test/field", NewTokenSet(theme.DefaultDark()))
	tf.SetValue("ab שלום")
	paintField(t, tf, 10)
	tf.edit.SetCursor(3, false) // on ש, the right end of the Hebrew run
	tf.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft}))
	if got := tf.edit.Cursor(); got != 4 {
		t.Fatalf("Left from ש moved to %d, want ל (4)", got)
	}
	tf.edit.SetCursor(6, false) // on ם, the left end of the run
	tf.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft}))
	if got := tf.edit.Cursor(); got != 2 {
		t.Fatalf("Left from ם moved to %d, want the space (2)", got)
	}
}

func TestTextFieldPasswordNeverArranges(t *testing.T) {
	tf := NewTextField("test/field", NewTokenSet(theme.DefaultDark()))
	tf.SetPassword(true)
	tf.SetValue("שלום")
	if tf.windowLine(tf.displayRunes()) != nil {
		t.Fatal("a masked value produced an arrangement")
	}
}
