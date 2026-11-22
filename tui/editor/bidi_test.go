package editor

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types/insight"
	"crude/types/setting"
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

func TestEditorByLineJoinsPersianAtCodeMargin(t *testing.T) {
	ed := loadEditorText(t, "notes.md", "سلام\n\nok\n")
	ed.SetTextDirection(bidi.Auto)
	ed.SetCursorPosition(0, 4)
	buf := paintEditor(ed, 30, 6)
	row := textRow(ed, buf, 0)
	if !strings.HasPrefix(row, "\ufee1\ufe8e\ufee0\ufeb3") {
		t.Fatalf("Persian line is not left-aligned and joined: %q", row)
	}
	// The caret at the end of the line sits on the first unused source-grid cell.
	if got, want := caretColumn(ed, buf, 0), 4; got != want {
		t.Fatalf("end-of-line caret at %d, want %d", got, want)
	}
	// A blank line also starts at the code margin.
	ed.SetCursorPosition(1, 0)
	buf = paintEditor(ed, 30, 6)
	if got := caretColumn(ed, buf, 1); got != 0 {
		t.Fatalf("caret on the empty line at %d, want the code margin", got)
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

// Empty viewport space is outside the line. Inside the line a drag is
// anchored to source characters, even though the glyphs run backwards.
func TestEditorDragAcrossLeftAlignedPersian(t *testing.T) {
	ed := loadEditorText(t, "notes.md", "سلام دنیا\n")
	paintEditor(ed, 40, 6)
	if pos, _ := ed.PositionAt(ed.GutterWidth()+15, 0); pos.Col != 9 {
		t.Fatalf("blank space hit %+v", pos)
	}
	right := ed.GutterWidth() + 8
	ed.HandleEvent(tea.MouseClickMsg{X: right, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseMotionMsg{X: right - 4, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseReleaseMsg{X: right - 4, Y: 0, Button: tea.MouseLeft})
	if got := ed.SelectedText(); got != "سلام" {
		t.Fatalf("drag selected %q", got)
	}
}

// Code in a right-to-left sentence keeps its own order (UTS #55): under By
// line a Persian Typst sentence is a right-to-left paragraph, and
// "#cite(<key>)" reads as written instead of losing its "#" to the far side
// or mirroring its brackets.
func TestEditorByLineKeepsMarkupCodeInOrder(t *testing.T) {
	ed := loadEditorText(t, "notes.typ", "#strong[\u05e9\u05dc\u05d5\u05dd]\n\u05e9\u05dc\u05d5\u05dd #cite(<key>). \u05e2\u05d5\u05dc\u05dd\n")
	ed.SetTextDirection(bidi.Auto)
	buf := paintEditor(ed, 40, 6)
	if got := strings.TrimRight(textRow(ed, buf, 1), " "); got != "\u05dd\u05dc\u05d5\u05e2 .#cite(<key>) \u05dd\u05d5\u05dc\u05e9" {
		t.Fatalf("sentence with a citation shows %q", got)
	}
	// The source delimiters stay on their written sides of the prose.
	if got := strings.TrimRight(textRow(ed, buf, 0), " "); got != "#strong[\u05dd\u05d5\u05dc\u05e9]" {
		t.Fatalf("markup line shows %q", got)
	}
}

// Two Hebrew strings in left-to-right code stay where they are written, and
// a comment reads in its own direction.
func TestEditorKeepsRightToLeftStringsAndCommentsInPlace(t *testing.T) {
	ed := loadEditorText(t, "main.go", "package main\n\nvar x = f(\"\u05d0\u05d1\", \"\u05d2\u05d3\") // \u05e9\u05dc\u05d5\u05dd world \u05e2\u05d5\u05dc\u05dd\n")
	buf := paintEditor(ed, 80, 6)
	want := "var x = f(\"\u05d1\u05d0\", \"\u05d3\u05d2\") // \u05dd\u05dc\u05d5\u05e2 world \u05dd\u05d5\u05dc\u05e9"
	if got := strings.TrimRight(textRow(ed, buf, 2), " "); got != want {
		t.Fatalf("code line shows\n%q\nwant\n%q", got, want)
	}
}

// Right-to-left prose with an English phrase reads in sentence order without
// any setting: By line is the default. Under Left to right the same line puts
// its right-to-left phrases in left-to-right order, so a reader of the
// sentence meets its parts backwards.
func TestEditorDefaultShowsRightToLeftProseInSentenceOrder(t *testing.T) {
	if got := (setting.Editor{}).TextDirectionOrDefault(); got != setting.EditorTextDirectionByLine {
		t.Fatalf("default line direction is %q", got)
	}
	ed := loadEditorText(t, "notes.typ", "\u05d0\u05d1 Active SLAM \u05d2\u05d3\n")
	buf := paintEditor(ed, 40, 4)
	if got := strings.TrimRight(textRow(ed, buf, 0), " "); got != "\u05d3\u05d2 Active SLAM \u05d1\u05d0" {
		t.Fatalf("default editor shows %q", got)
	}
	ed.SetTextDirection(bidi.LeftToRight)
	buf = paintEditor(ed, 40, 4)
	if got := strings.TrimRight(textRow(ed, buf, 0), " "); got != "\u05d1\u05d0 Active SLAM \u05d3\u05d2" {
		t.Fatalf("Left to right shows %q", got)
	}
}

// By line takes a line's direction from its prose only. A comment that
// starts with Hebrew, the empty line after it, and a closing bracket after a
// Hebrew paragraph keep the left-to-right order of code (UTS #55: content
// does not choose the atom order). Before, the empty line and the bracket
// took the direction of the Hebrew letters above them.
func TestEditorCodeLinesKeepCodeOrderByLine(t *testing.T) {
	ed := loadEditorText(t, "main.go", "package main\n\n// \u05e9\u05dc\u05d5\u05dd world\n\nfunc f() {}\n")
	ed.SetCursorPosition(3, 0)
	buf := paintEditor(ed, 40, 6)
	if got := textRow(ed, buf, 2); !strings.HasPrefix(got, "// world \u05dd\u05d5\u05dc\u05e9") {
		t.Fatalf("Hebrew comment line shows %q", got)
	}
	if got := caretColumn(ed, buf, 3); got != 0 {
		t.Fatalf("caret on the empty line after a Hebrew comment at column %d, want 0", got)
	}
	ed = loadEditorText(t, "notes.typ", "#figure([\n\u05e9\u05dc\u05d5\u05dd \u05e2\u05d5\u05dc\u05dd\n])\n")
	buf = paintEditor(ed, 40, 6)
	if got := textRow(ed, buf, 2); !strings.HasPrefix(got, "])") {
		t.Fatalf("closing brackets after a Hebrew line show %q", got)
	}
	if got := strings.TrimRight(textRow(ed, buf, 1), " "); !strings.HasSuffix(got, "\u05e9") {
		t.Fatalf("Hebrew line is not right to left: %q", got)
	}
}

// A viewport is a crop of document-space visual columns, not a line break.
// Wheel through both ends of the bidi runs. Every visible glyph must keep
// both its place in that row and its source position.
func TestEditorPanCropsOneVisualLine(t *testing.T) {
	const logical = "    אבגד ABCDEFGHIJKLMNOPQRSTUVWXYZ הוזח"
	const visual = "    חזוה ABCDEFGHIJKLMNOPQRSTUVWXYZ דגבא"
	ed := loadEditorText(t, "prose.txt", logical+"\nend\n")
	ed.indentGuides = false
	w := ed.GutterWidth() + 18
	for _, button := range []tea.MouseButton{tea.MouseNone, tea.MouseWheelRight, tea.MouseWheelRight, tea.MouseWheelRight, tea.MouseWheelLeft, tea.MouseWheelLeft, tea.MouseWheelLeft} {
		if button != tea.MouseNone {
			ed.HandleEvent(tea.MouseWheelMsg{Button: button, X: ed.GutterWidth() + 4, Y: 0})
		}
		buf := paintEditor(ed, w, 6)
		actual := textRow(ed, buf, 0)
		want := string([]rune(visual)[ed.scrollX : ed.scrollX+ed.textColumns()])
		if actual != want {
			t.Fatalf("pan %d: got %q; want %q", ed.scrollX, actual, want)
		}
		for i, r := range []rune(want) {
			if r == ' ' {
				continue
			}
			p, ok := ed.PositionAt(ed.GutterWidth()+i, 0)
			if !ok || p.Line != 0 || []rune(logical)[p.Col] != r {
				t.Fatalf("pan %d, cell %d (%c): hit %+v", ed.scrollX, i, r, p)
			}
			x, y, ok := ed.PositionScreenPosition(p, 0, 0)
			if !ok || x != ed.GutterWidth()+i || y != 0 {
				t.Fatalf("anchor %+v returned %d,%d (%v)", p, x, y, ok)
			}
		}
	}
}

func TestWheelBurstKeepsLongMixedLineStable(t *testing.T) {
	const leading = "    before "
	const words = "ABCDEFGHIJKLMNOPQRSTUVWXYZ "
	long := strings.Repeat(words, 350)
	ed := loadEditorText(t, "prose.txt", strings.Repeat("above\n", 180)+
		leading+"אבגד "+long+"שלום\n"+strings.Repeat("below\n", 240))
	ed.indentGuides = false
	w := ed.GutterWidth() + 45
	paintEditor(ed, w, 12)
	wheel := func(button tea.MouseButton, count int) {
		t.Helper()
		for range count {
			ed.HandleEvent(tea.MouseWheelMsg{Button: button, X: ed.GutterWidth() + 4, Y: 0})
		}
	}
	wheel(tea.MouseWheelDown, 60)
	if row := ed.scrollRow(); row != 180 {
		t.Fatalf("60 wheel ticks landed on row %d, want 180", row)
	}
	// This row has an LTR base and a bounded RTL run. The independent
	// visual reference must remain valid even far into the 9,000-column row.
	expected := []rune(leading + "דגבא " + long + "םולש")
	for _, offset := range []int{0, 9, 3000, 7200, 9, 0} {
		button, delta := tea.MouseWheelRight, offset-ed.scrollX
		if delta < 0 {
			button, delta = tea.MouseWheelLeft, -delta
		}
		wheel(button, delta/3)
		buf := paintEditor(ed, w, 12)
		want := string(expected[offset : offset+ed.textColumns()])
		if row, got := ed.scrollX, textRow(ed, buf, 0); row != offset || got != want {
			t.Fatalf("want crop at %d: %q; got %d: %q", offset, want, row, got)
		}
	}
	wheel(tea.MouseWheelUp, 60)
	if row := ed.scrollRow(); row != 0 {
		t.Fatalf("reverse burst landed on row %d, want 0", row)
	}
}

func TestEditorBidiBlockCaretTraversesViewportAndRowEdges(t *testing.T) {
	// Unique runes make both directions of the source map independently checkable.
	const src = "אבגד ABCDEFGHIJKLMNOPQRST הוזח"
	const shown = "חזוה ABCDEFGHIJKLMNOPQRST דגבא"
	ed := loadEditorText(t, "p.txt", "x\n"+src+"\ny")
	w := ed.GutterWidth() + 12
	paintEditor(ed, w, 6)
	ed.SetCursorPosition(0, 1)
	for i, r := range []rune(shown) {
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight})
		b := paintEditor(ed, w, 6)
		if ed.cursor.Line != 1 || ed.cursor.Col >= len([]rune(src)) || []rune(src)[ed.cursor.Col] != r {
			t.Fatalf("step %d: cursor %+v, want %c", i, ed.cursor, r)
		}
		if got := caretColumn(ed, b, 1); got < 0 || got+ed.scrollX != i {
			t.Fatalf("step %d: visual caret %d scroll %d", i, got, ed.scrollX)
		}
	}
	for i := 0; i < 2; i++ {
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if ed.cursor != (Position{2, 0}) {
		t.Fatalf("after end: %+v", ed.cursor)
	}
	for i := 0; i < len([]rune(shown))+2; i++ {
		ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	if ed.cursor != (Position{0, 1}) {
		t.Fatalf("return past start: %+v", ed.cursor)
	}
}

func TestEditorBidiSourceScaffoldingAndResize(t *testing.T) {
	const src = "  caption: [אב Active SLAM גד]\n  [אב], [גד]\n  אב #cite(<key>) גד"
	ed := loadEditorText(t, "notes.typ", src)
	ed.indentGuides = false
	for _, w := range []int{80, 35, 100} {
		b := paintEditor(ed, w, 6)
		for y, want := range []string{"  caption: [דג Active SLAM בא]", "  [בא], [דג]", "  דג #cite(<key>) בא"} {
			n := min(ed.textColumns(), len([]rune(want)))
			expected := string([]rune(want)[:n]) + strings.Repeat(" ", ed.textColumns()-n)
			if got := textRow(ed, b, y); got != expected {
				t.Fatalf("width %d row %d: %q", w, y, got)
			}
		}
	}
	if ed.Value() != src || ed.Modified() {
		t.Fatal("resize edited source")
	}
}

func TestEditorBidiWrappedRowsAndInlayHits(t *testing.T) {
	ed := newInlayTestEditor(t, "אבגדהוזחטיכלמנסעפצקרשת")
	ed.SetPresentation(wrapSettings(4))
	b := paintWrappedInlays(t, ed, ed.GutterWidth()+11, 8, false)
	for row, want := range []string{"טחזוהדגבא↩", "צפעסנמלכי↩", "תשרק"} {
		if got := strings.TrimRight(textRow(ed, b, row), " "); got != want {
			t.Fatalf("wrap row %d: %q, want %q", row, got, want)
		}
	}
	p, ok := ed.PositionAt(ed.GutterWidth(), 1)
	if !ok || p.Col != 17 {
		t.Fatalf("wrapped row hit %+v", p)
	}
	ed = newInlayTestEditor(t, "אבגדהוזח ABCDEFGHIJKLMNOP")
	installTestInlays(t, ed, insight.LSPInlayHint{SourceByte: 4, Position: insight.LSPInlayPosition{Character: 2}, Label: "HINT", Index: 0})
	w := ed.GutterWidth() + 26
	paintEditor(ed, w, 6)
	ed.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	b = paintEditor(ed, w, 6)
	// Find the visible label in the actual paint, then hit each of its cells.
	rowText := textRow(ed, b, 0)
	before, _, ok := strings.Cut(rowText, "HINT")
	if !ok {
		t.Fatalf("missing hint in %q", rowText)
	}
	for j := range 4 {
		x := ed.GutterWidth() + rendering.VisibleWidth(before) + j
		if id, ok := ed.InlayHintAt(x, 0); !ok || id != 0 {
			t.Fatalf("no hint at painted cell %d", x)
		}
		p, ok := ed.PositionAt(x, 0)
		if !ok || p.Col != 2 {
			t.Fatalf("inlay hit %+v", p)
		}
	}
}

// Clipping either column of a wide cluster may blank that fragment, but
// must not shift its neighbors or let an orphan continuation reach paint.
func TestEditorBidiPanAcrossWideGrapheme(t *testing.T) {
	const source = "אב ABC界DEF אבגד xyz"
	ed := loadEditorText(t, "p.txt", source)
	w := ed.GutterWidth() + 12
	full := paintEditor(ed, 80, 5)
	g := ed.GutterWidth()
	clusters := append([]cell.Cell(nil), full.Row(0)[g:g+ed.textColumns()]...)
	paintEditor(ed, w, 5) // resize completes its pending caret reveal before manual pan
	for offset := 0; offset <= ed.hScrollModel.MaxOffset(); offset++ {
		ed.scrollX = offset
		b := paintEditor(ed, w, 5)
		for x := 0; x < ed.textColumns(); x++ {
			s := clusters[offset+x]
			c := b.Get(g+x, 0)
			if x == 0 && s.Width == 0 || x+int(s.Width) > ed.textColumns() {
				if c.Width != 1 || c.Char != ' ' {
					t.Fatalf("offset %d column %d: partial wide cell: %+v", offset, x, c)
				}
				continue
			}
			if c.Width != s.Width || c.Content != s.Content || c.Char != s.Char {
				t.Fatalf("offset %d col %d: %q/%d, want %q/%d", offset, x, c.Content, c.Width, s.Content, s.Width)
			}
		}
	}
}

func TestEditorBidiKeepsTabStopsBetweenFields(t *testing.T) {
	ed := loadEditorText(t, "fields.txt", "אב\tגד\tZ\n")
	ed.indentGuides = false
	ed.renderWhitespace = "none"
	b := paintEditor(ed, 32, 6)
	if got := strings.TrimRight(textRow(ed, b, 0), " "); got != "בא  דג  Z" {
		t.Fatalf("tab-separated fields moved: %q", got)
	}
	for _, tc := range []struct{ cell, col int }{{0, 1}, {1, 0}, {2, 2}, {3, 2}, {4, 5}, {5, 4}, {6, 6}, {7, 6}, {8, 8}} {
		p, ok := ed.PositionAt(ed.GutterWidth()+tc.cell, 0)
		if !ok || p.Col != tc.col {
			t.Fatalf("cell %d: %+v", tc.cell, p)
		}
	}
}

func TestSemanticRecolorDoesNotReorderProse(t *testing.T) {
	const text = "אב Active SLAM גד"
	ed := newInlayTestEditor(t, text)
	expect := "דג Active SLAM בא"
	check := func() {
		t.Helper()
		if got := strings.TrimRight(textRow(ed, paintEditor(ed, 60, 6), 0), " "); got != expect {
			t.Fatalf("semantic ink changed reading order: %q", got)
		}
	}
	check()
	set := semanticFixtureSet(ed)
	set.Spans = []insight.LSPSemanticSpan{{Start: 0, End: len(text), Type: 0}}
	if err := ed.SetSemanticTokens(&set); err != nil {
		t.Fatal(err)
	}
	check()
	ed.ClearSemanticTokens()
	check()
}

// Measure a pan and its repaint in an already open mixed line. The viewport
// is fixed; increasing the document width must not allocate a new line map
// per frame. A cold edit or a soft-wrap resize is a different workload.
func BenchmarkEditorMixedDirectionPan(b *testing.B) {
	for _, n := range []int{64, 2048} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			ed := newInlayTestEditor(b, strings.Repeat("اب API #tag אבג ", n))
			s := surface.NewSurface("mixed-pan", 90, 8, cell.TransparentBlank())
			ctx := ui.PaintContext{Theme: ed.th}
			ed.Render(s, ctx)
			ed.scrollX = ed.hScrollModel.MaxOffset() / 2
			ed.Render(s, ctx)
			right := true
			b.ReportAllocs()
			for b.Loop() {
				button := tea.MouseWheelRight
				if !right {
					button = tea.MouseWheelLeft
				}
				ed.HandleEvent(tea.MouseWheelMsg{X: 60, Y: 0, Button: button})
				ed.Render(s, ctx)
				right = !right
			}
		})
	}
}

func TestEditorRectangleSelectsOnlyPaintedBidiCells(t *testing.T) {
	const line = "ab אבג end" // visual: ab גבא end. [1,5) is b, space, gimel, bet (not alef).
	ed := loadEditorText(t, "rectangle.txt", line+"\r\n"+line)
	paintEditor(ed, 42, 6)
	x := ed.GutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x + 1, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 5, Y: 1, Button: tea.MouseLeft, Mod: tea.ModAlt})
	ed.HandleEvent(tea.MouseReleaseMsg{X: x + 5, Y: 1, Button: tea.MouseLeft, Mod: tea.ModAlt})
	b := paintEditor(ed, 42, 6)
	_, bg := ed.tokens.TextSelectionColors(ed.cellColor(ed.th.Colors.Fg))
	for y := range 2 {
		for i := 0; i < 10; i++ {
			if b.Get(x+i, y).BG.Equal(bg) != (1 <= i && i < 5) {
				t.Fatalf("rectangle cell %d,%d: unexpected selection %q", i, y, ed.SelectedText())
			}
		}
	}
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDelete})
	if want := "aא end\r\naא end"; ed.Value() != want {
		t.Fatalf("Delete removed unselected text or left selected text: %q", ed.Value())
	}
	ed.Undo()
	if ed.Value() != line+"\r\n"+line {
		t.Fatal("rectangle Undo changed source")
	}
}

func TestEditorAddCaretBelowUsesVisualColumn(t *testing.T) {
	ed := loadEditorText(t, "p.txt", "abcd\nab אבג")
	paintEditor(ed, 40, 6)
	ed.SetCursorPosition(0, 3)
	ed.HandleEvent(ActionMsg{ID: "select.caretBelow"})
	ed.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := ed.Value(); got != "abcXd\nab אבXג" {
		t.Fatalf("added caret missed column 3 (gimel): %q", got)
	}
}
