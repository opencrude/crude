package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/surface"
)

// textRow returns the text area of one painted row: between the gutter and
// the scrollbar.
func textRow(ed *Editor, buf *surface.CellBuffer, y int) string {
	var out strings.Builder
	row := buf.Row(y)
	for x := ed.GutterWidth(); x < ed.GutterWidth()+ed.textColumns() && x < len(row); x++ {
		c := row[x]
		switch {
		case c.Width == 0:
		case c.Content != "":
			out.WriteString(c.Content)
		case c.Char == 0:
			out.WriteByte(' ')
		default:
			out.WriteRune(c.Char)
		}
	}
	return out.String()
}

func caretColumn(ed *Editor, buf *surface.CellBuffer, y int) int {
	for x := ed.GutterWidth(); x < ed.GutterWidth()+ed.textColumns(); x++ {
		if buf.Get(x, y).Attr.Has(cell.AttrReverse) {
			return x - ed.GutterWidth()
		}
	}
	return -1
}

func TestEditorArrangesHebrewRunInLeftToRightCode(t *testing.T) {
	ed := loadEditorText(t, "msg.txt", "say(\"שלום\")\n")
	buf := paintEditor(ed, 40, 6)
	// Code lines stay left-to-right; the Hebrew run reads right to left in
	// place and the brackets keep their glyphs at even levels.
	if got := strings.TrimRight(textRow(ed, buf, 0), " "); got != "say(\"םולש\")" {
		t.Fatalf("row = %q", got)
	}
}

func TestEditorByLineRightAlignsPersianLines(t *testing.T) {
	ed := loadEditorText(t, "notes.md", "سلام\n\nok\n")
	ed.SetTextDirection(bidi.Auto)
	ed.SetCursorPosition(0, 4)
	buf := paintEditor(ed, 30, 6)
	row := textRow(ed, buf, 0)
	if !strings.HasSuffix(row, "\ufee1\ufe8e\ufee0\ufeb3") {
		t.Fatalf("Persian line is not right-aligned and joined: %q", row)
	}
	// The caret at the end of the line sits just left of the text.
	if got, want := caretColumn(ed, buf, 0), ed.textColumns()-5; got != want {
		t.Fatalf("end-of-line caret at %d, want %d", got, want)
	}
	// The empty line below keeps the Persian direction; "ok" does not.
	ed.SetCursorPosition(1, 0)
	buf = paintEditor(ed, 30, 6)
	if got := caretColumn(ed, buf, 1); got != ed.textColumns()-1 {
		t.Fatalf("caret on the empty line at %d, want the right edge", got)
	}
	if got := textRow(ed, buf, 2); !strings.HasPrefix(got, "ok") {
		t.Fatalf("English line moved: %q", got)
	}
}

func TestEditorClickOnRightToLeftTextHitsTheCharacterShown(t *testing.T) {
	ed := loadEditorText(t, "msg.txt", "ab אבג\n")
	paintEditor(ed, 40, 6)
	// Visual "ab גבא": column 3 shows ג (source column 5).
	pos, ok := ed.PositionAt(ed.GutterWidth()+3, 0)
	if !ok || pos.Line != 0 || pos.Col != 5 {
		t.Fatalf("click on ג gave %+v, want column 5", pos)
	}
	pos, _ = ed.PositionAt(ed.GutterWidth()+5, 0)
	if pos.Col != 3 {
		t.Fatalf("click on א gave column %d, want 3", pos.Col)
	}
}

func TestEditorArrowsMoveOnScreen(t *testing.T) {
	defer bidi.SetVisualCaret(bidi.VisualCaret())
	bidi.SetVisualCaret(true)
	ed := loadEditorText(t, "msg.txt", "ab אבג\n")
	paintEditor(ed, 40, 6)
	ed.SetCursorPosition(0, 3) // on א, the right end of the run
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft})
	if ed.cursor.Col != 4 {
		t.Fatalf("Left from א moved to column %d, want ב (4)", ed.cursor.Col)
	}
	ed.SetCursorPosition(0, 5) // on ג, the left end of the run
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft})
	if ed.cursor.Col != 2 {
		t.Fatalf("Left from ג moved to column %d, want the space (2)", ed.cursor.Col)
	}
	// Shift+Left from א moves onto ב and selects the letter it crossed.
	ed.SetCursorPosition(0, 3)
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	if got := ed.SelectedText(); got != "א" {
		t.Fatalf("selected %q, want א", got)
	}
}

func TestEditorSelectionPaintsUnderItsCharacters(t *testing.T) {
	ed := loadEditorText(t, "msg.txt", "ab אבג\n")
	ed.SetSelection(0, 3, 0, 5) // א ב
	buf := paintEditor(ed, 40, 6)
	_, selBG := ed.tokens.TextSelectionColors(ed.cellColor(ed.th.Colors.Fg))
	sw := ed.GutterWidth()
	// Visual "ab גבא": ב at 4 and א at 5 are selected, ג at 3 is not.
	if !buf.Get(sw+4, 0).BG.Equal(selBG) || !buf.Get(sw+5, 0).BG.Equal(selBG) || buf.Get(sw+3, 0).BG.Equal(selBG) {
		t.Fatal("the selection did not follow its characters")
	}
}

// Up and Down keep the column the user sees. On a line whose right-to-left
// run is arranged, the character under that column is not the one at the
// same text column, with or without word wrap and horizontal scrolling.
func TestEditorUpDownKeepsTheScreenColumnAcrossRightToLeftRuns(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		ed := loadEditorText(t, "msg.txt", "abcdefgh\nab אבגד\nabcdefgh\n")
		ed.setWrapping(wrap)
		paintEditor(ed, 40, 8)
		// Visual line 2 is "ab דגבא": screen column 3 shows ד (text column 6).
		ed.SetCursorPosition(0, 3)
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
		if ed.cursor.Line != 1 || ed.cursor.Col != 6 {
			t.Fatalf("wrap=%v: Down from column 3 landed on %+v, want the ד shown at column 3 (text column 6)", wrap, ed.cursor)
		}
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
		if ed.cursor.Line != 2 || ed.cursor.Col != 3 {
			t.Fatalf("wrap=%v: second Down landed on %+v, want column 3", wrap, ed.cursor)
		}
	}
}

// With horizontal scrolling, Up and Down stay in the same text column on
// left-to-right lines, also when a collapsed fold makes the editor paint
// through its row projection.
func TestEditorUpDownKeepsColumnWhileScrolledHorizontally(t *testing.T) {
	long := "// " + strings.Repeat("abcdefghij", 12)
	for _, folded := range []bool{false, true} {
		ed := loadEditorText(t, "long.go", "package p\n\nfunc a() {\n\tx := 1\n\t_ = x\n}\n"+long+"\n"+long+"\n"+long+"\n")
		if folded {
			ed.SetCursorPosition(2, 4)
			ed.HandleEvent(tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
		}
		paintEditor(ed, 40, 12)
		if folded && !ed.usesRows() {
			t.Fatal("fold did not switch the editor to its row projection")
		}
		ed.SetCursorPosition(6, 100)
		ed.ensureCursorVisible()
		paintEditor(ed, 40, 12)
		if ed.scrollX == 0 {
			t.Fatal("editor did not scroll horizontally to the caret")
		}
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyUp})
		if ed.cursor.Line != 7 || ed.cursor.Col != 100 {
			t.Fatalf("folded=%v: caret at %+v after Down, Down, Up; want line 7 column 100", folded, ed.cursor)
		}
	}
}

// A pointer right of a right-to-left line is at the line's start, as a
// pointer left of a left-to-right line is: pressing there and dragging to
// the left selects from the first character.
func TestEditorPointerPastRightToLeftLineEdgeStartsTheLine(t *testing.T) {
	ed := loadEditorText(t, "notes.md", "سلام دنیا\n")
	ed.SetTextDirection(bidi.Auto)
	paintEditor(ed, 40, 6)
	right := ed.GutterWidth() + ed.textColumns() // first column past the text
	if pos, _ := ed.PositionAt(right, 0); pos.Col != 0 {
		t.Fatalf("pointer right of the line at column %d, want its start", pos.Col)
	}
	ed.HandleEvent(tea.MouseClickMsg{X: right, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseMotionMsg{X: right - 5, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseReleaseMsg{X: right - 5, Y: 0, Button: tea.MouseLeft})
	// right-5 shows the space after the first word; the drag ends at its
	// leading edge.
	if got := ed.SelectedText(); got != "سلام" {
		t.Fatalf("drag from past the right edge selected %q, want the first word", got)
	}
}
