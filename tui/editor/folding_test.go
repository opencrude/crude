package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/types"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const foldFixture = "package p\nfunc outer() {\n    if value {\n        call()\n    }\n}\nlast\n"

func TestFoldNativeCommandsShareRowsAndRevealSearch(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture)
	e.SetCursorPosition(2, 4)
	e.HandleEvent(tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 7 {
		t.Fatal("Fold did not remove exactly its hidden source rows", e.ScrollModel().Content())
	}
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 5 || e.Value() != foldFixture || e.Modified() {
		t.Fatal("outer fold edited source or counted hidden rows")
	}
	e.HandleEvent(ActionMsg{ID: "view.unfold"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 7 {
		t.Fatal("unfold lost nested collapsed state")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	if e.CursorLine() != 4 {
		t.Fatal("vertical motion entered hidden source", e.CursorLine())
	}
	e.SetQuery("call")
	e.NextMatch()
	paintEditor(e, 80, 14)
	if e.CursorLine() != 3 || e.ScrollModel().Content() != 8 {
		t.Fatal("search landing stayed folded")
	}
	e.HandleEvent(ActionMsg{ID: "view.foldAll"})
	paintEditor(e, 80, 14)
	e.SetSelection(0, 0, 7, 0)
	if e.SelectedText() != foldFixture {
		t.Fatal("copy dropped folded source")
	}
}
func TestFoldGutterAndLinkedEditDoNotShareViewChoices(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture)
	peer := e.NewLinkedView()
	t.Cleanup(peer.Close)
	e.SetPresentation(types.EditorSettings{TabSize: 4, LineNumbers: false})
	paintEditor(e, 80, 14)
	g := e.gutterGeometry()
	e.HandleEvent(tea.MouseClickMsg{X: g.numberX - 1, Y: 1, Button: tea.MouseLeft})
	paintEditor(e, 80, 14)
	paintEditor(peer, 50, 14)
	if e.ScrollModel().Content() != 5 || peer.ScrollModel().Content() != 8 {
		t.Fatal("gutter choice missing or leaked to linked view")
	}
	peer.SetCursorPosition(0, 0)
	peer.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 6 {
		t.Fatal("disjoint edit lost folded source mapping")
	}
	peer.SetCursorPosition(4, 8)
	peer.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 9 {
		t.Fatal("peer edit kept touched source hidden")
	}
	e.HandleEvent(ActionMsg{ID: "view.foldAll"})
	e.SetCursorPosition(2, 0)
	e.HandleEvent(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	e.Undo()
	if !strings.Contains(e.Value(), "Xcall") {
		t.Fatal("folding interfered with canonical Undo")
	}
}

func TestFoldMemoryIsExactPrivateSourceAndKeepsNestedChoices(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture)
	e.HandleEvent(ActionMsg{ID: "view.foldAll"})
	raw, err := json.Marshal(e.ExportFolds())
	if err != nil {
		t.Fatal(err)
	}
	var memory FoldMemory
	if err = json.Unmarshal(raw, &memory); err != nil {
		t.Fatal(err)
	}
	restored := loadEditorText(t, "fold.go", foldFixture)
	restored.RestoreFolds(&memory)
	paintEditor(restored, 80, 14)
	if !reflect.DeepEqual(restored.ExportFolds(), e.ExportFolds()) || restored.ScrollModel().Content() != 5 {
		t.Fatal("restored folds lost source or nesting")
	}
	restored.HandleEvent(ActionMsg{ID: "view.unfoldAll"})
	if len(e.folds.collapsed) == 0 {
		t.Fatal("restoring aliased live fold choices")
	}
	changed := loadEditorText(t, "changed.go", "changed\n"+foldFixture)
	changed.RestoreFolds(&memory)
	if changed.ExportFolds() != nil {
		t.Fatal("saved fold ranges were used on different source")
	}
}
func TestFoldAndWrapUseOneScrollbarAndUnicodePointerProjection(t *testing.T) {
	source := strings.Replace(foldFixture, "        call()", "        call(\""+strings.Repeat("word 界 é ", 10)+"\")", 1)
	e := loadEditorText(t, "fold.go", source)
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, 40, 14)
	original := e.ScrollModel().Content()
	if original <= 8 {
		t.Fatal("long source did not wrap")
	}
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintWrapped(t, e, 40, 14)
	if e.ScrollModel().Content() != 5 {
		t.Fatal("fold left wrapped hidden rows in scrollbar", e.ScrollModel().Content())
	}
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true, WordWrap: true})
	paintWrapped(t, e, 50, 14)
	if e.ScrollModel().Content() != 5 {
		t.Fatal("tab/width change lost folding")
	}
	e.SetCursorPosition(3, 8)
	paintWrapped(t, e, 50, 14)
	x, y, ok := e.PositionScreenPosition(e.cursor, 0, 0)
	if !ok {
		t.Fatal("navigation did not reveal folded source")
	}
	e.HandleEvent(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != strings.Replace(source, "call(", "Xcall(", 1) {
		t.Fatal("pointer after fold/wrap edited wrong bytes")
	}
	e.Undo()
	if e.Value() != source {
		t.Fatal("fold/wrap changed Undo")
	}
}

func TestFoldRangesRejectCrossingAndKeepLiteralDelimitersSafe(t *testing.T) {
	ranges := normalizeFolds([]FoldRange{{1, 10}, {2, 4}, {3, 8}, {11, 14}, {11, 12}, {-1, 8}, {8, 8}})
	if !reflect.DeepEqual(ranges, []FoldRange{{1, 10}, {2, 4}, {11, 14}}) {
		t.Fatal("crossing/duplicate ranges survived", ranges)
	}
	e := loadEditorText(t, "literal.c", "/* multi\r\n   line */\r\ncall(\"{\", \"}\");\r\n{\r\n  body();\r\n}\r\n")
	paintEditor(e, 80, 14)
	if _, ok := e.foldAt(2); ok {
		t.Fatal("quoted braces became a fold")
	}
	if _, ok := e.foldAt(0); !ok {
		t.Fatal("multi-line comment unavailable")
	}
	e.SetCursorPosition(3, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 6 {
		t.Fatal("CRLF counted as source cells or fake lines", e.ScrollModel().Content())
	}
	e.SetCursorPosition(4, 2)
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	e.Undo()
	if !strings.Contains(e.Value(), "\r\n  body();\r\n") {
		t.Fatal("fold edit changed source terminators")
	}
}
func TestFoldAllKeepsOffscreenSyntaxProjectionBounded(t *testing.T) {
	source := "package p\nfunc large() {\n" + strings.Repeat("    call()\n", 20000) + "}\n"
	e := loadEditorText(t, "large.go", source)
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintEditor(e, 80, 12)
	if e.ScrollModel().Content() != 4 || len(e.chromaLineCache) > 12 {
		t.Fatal("folded source was eagerly projected", e.ScrollModel().Content(), len(e.chromaLineCache))
	}
}

func TestFoldStaysHiddenDuringColdWidthPreparation(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture)
	e.SetPresentation(wrapSettings(4))
	paintWrapped(t, e, 80, 12)
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	release := pauseSyntaxWorkers(t)
	e.SetPresentation(wrapSettings(8))
	painted := paintEditor(e, 80, 12)
	if e.ScrollModel().Content() != 5 || painted.Get(e.GutterWidth(), 3).Char != 'l' {
		release()
		t.Fatal("pending width preparation revealed hidden source")
	}
	first := &e.rows.lines[0]
	paintEditor(e, 80, 12)
	if first != &e.rows.lines[0] {
		release()
		t.Fatal("cold worker wait rebuilt identity geometry every paint")
	}
	cmd := e.RowsCommand()
	release()
	if cmd != nil {
		cmd().(RowsReadyMsg).Apply()
	}
	settleRows(t, e)
	paintEditor(e, 80, 12)
	if e.ScrollModel().Content() != 5 {
		t.Fatal("prepared width lost folded rows")
	}
}

// Toggling a fold must not move the rows the user is looking at: the
// clicked header keeps its screen row, the top of the view stays put, and an
// offscreen caret is not pulled into view (the old path scrolled back to it).
func TestFoldToggleKeepsTheViewportStill(t *testing.T) {
	var text strings.Builder
	text.WriteString("package p\n")
	for i := range 60 {
		text.WriteString("func f" + strings.Repeat("x", i%3) + "() {\n    a()\n    b()\n    c()\n}\n")
	}
	e := loadEditorText(t, "many.go", text.String())
	const w, h = 80, 20
	paintEditor(e, w, h)
	for range 10 {
		e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	buf := paintEditor(e, w, h)
	top := e.scrollRow()
	if top == 0 {
		t.Fatal("fixture did not scroll")
	}
	y := -1
	for row := 6; row < 12; row++ {
		if line := e.rowAt(top + row).line; (line-1)%5 == 0 {
			y = row
			break
		}
	}
	if y < 0 {
		t.Fatal("no fold header near the middle of the view")
	}
	header := e.rowAt(top + y).line
	before := plainRow(buf, y, w)
	above := plainRow(buf, y-1, w)
	g := e.gutterGeometry()
	for _, step := range []string{"fold", "unfold"} {
		e.HandleEvent(tea.MouseClickMsg{X: g.foldX, Y: y, Button: tea.MouseLeft})
		buf = paintEditor(e, w, h)
		if e.scrollRow() != top || e.rowAt(top+y).line != header {
			t.Fatalf("%s moved the view: top %d -> %d, row %d shows line %d, want %d", step, top, e.scrollRow(), y, e.rowAt(top+y).line, header)
		}
		if got := plainRow(buf, y, w); !strings.Contains(got, "func f") || plainRow(buf, y-1, w) != above {
			t.Fatalf("%s repainted the header elsewhere: %q / %q (was %q)", step, plainRow(buf, y-1, w), got, before)
		}
		if e.CursorLine() != 0 {
			t.Fatalf("%s moved the caret to line %d", step, e.CursorLine())
		}
	}
	if !strings.Contains(plainRow(buf, y+1, w), "a()") {
		t.Fatal("unfold did not restore the block under its header")
	}

	// A keyboard fold from inside a block keeps its visible header in place
	// and puts the caret on the folded block's first line.
	e.SetCursorPosition(header+2, 4)
	paintEditor(e, w, h)
	top = e.scrollRow()
	e.HandleEvent(tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
	if got := plainRow(paintEditor(e, w, h), y, w); !strings.Contains(got, "func f") {
		t.Fatalf("keyboard fold moved the header row: %q", got)
	}
	if len(e.folds.collapsed) != 1 || e.scrollRow() != top || e.rowAt(top+y).line != header || e.CursorLine() != e.folds.collapsed[0].Start {
		t.Fatalf("keyboard fold moved the view: top %d -> %d, caret line %d, header %d, row shows %d, folds %v", top, e.scrollRow(), e.CursorLine(), header, e.rowAt(top+y).line, e.folds.collapsed)
	}
}

// Each block shows one chevron, on its header. Native syntax nodes used to
// add a second chevron on the first body line (the block's statement list),
// and a keyboard fold chose it instead of the block. Indentation folding
// offers one range per indented block; a nested block keeps its chevron and
// the closing brace stays visible, as in VS Code.
func TestFoldShowsOneChevronPerBlock(t *testing.T) {
	src := "package p\n\nfunc many() {\n\ta := 1\n\tb := 2\n\tc := 3\n}\n\nfunc sole() {\n\tif ok {\n\t\tcall()\n\t\tcall()\n\t}\n}\n"
	e := loadEditorText(t, "blocks.go", src)
	buf := paintEditor(e, 80, 20)
	e.refreshFolds()
	for _, line := range []int{2, 8, 9} {
		if _, ok := e.foldAt(line); !ok {
			t.Fatalf("block header on line %d has no fold: %v", line+1, e.folds.ranges)
		}
	}
	if r, ok := e.foldAt(3); ok {
		t.Fatalf("first body line carries a second fold %v: %v", r, e.folds.ranges)
	}
	g := e.gutterGeometry()
	chevron := func(y int) bool { return strings.TrimSpace(plainRow(buf, y, g.numberX)) != "" }
	if !chevron(2) || chevron(3) {
		t.Fatalf("painted chevrons: header %q, first body line %q", plainRow(buf, 2, g.numberX), plainRow(buf, 3, g.numberX))
	}
	// The gutter paints the fold chevron tokens, not the tree triangles.
	if got := string(buf.Get(g.foldX, 2).Char); got != e.tokens.Glyphs().FoldExpanded || got == e.tokens.Glyphs().TreeExpanded {
		t.Fatalf("fold header glyph %q, want %q", got, e.tokens.Glyphs().FoldExpanded)
	}
	e.SetCursorPosition(4, 1)
	e.HandleEvent(tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
	paintEditor(e, 80, 20)
	if len(e.folds.collapsed) != 1 || e.folds.collapsed[0] != (FoldRange{2, 5}) || e.CursorLine() != 2 {
		t.Fatalf("keyboard fold inside a body folded %v, caret line %d", e.folds.collapsed, e.CursorLine())
	}
}
