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
	if e.ScrollModel().Content() != 6 {
		t.Fatal("Fold did not remove exactly its hidden source rows", e.ScrollModel().Content())
	}
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 4 || e.Value() != foldFixture || e.Modified() {
		t.Fatal("outer fold edited source or counted hidden rows")
	}
	e.HandleEvent(ActionMsg{ID: "view.unfold"})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 6 {
		t.Fatal("unfold lost nested collapsed state")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	if e.CursorLine() != 5 {
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
	if e.ScrollModel().Content() != 4 || peer.ScrollModel().Content() != 8 {
		t.Fatal("gutter choice missing or leaked to linked view")
	}
	peer.SetCursorPosition(0, 0)
	peer.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	paintEditor(e, 80, 14)
	if e.ScrollModel().Content() != 5 {
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
	if !reflect.DeepEqual(restored.ExportFolds(), e.ExportFolds()) || restored.ScrollModel().Content() != 4 {
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
	if e.ScrollModel().Content() != 4 {
		t.Fatal("fold left wrapped hidden rows in scrollbar", e.ScrollModel().Content())
	}
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true, WordWrap: true})
	paintWrapped(t, e, 50, 14)
	if e.ScrollModel().Content() != 4 {
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
	if e.ScrollModel().Content() != 5 {
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
	if e.ScrollModel().Content() != 3 || len(e.chromaLineCache) > 12 {
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
	if e.ScrollModel().Content() != 4 || painted.Get(e.GutterWidth(), 2).Char != 'l' {
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
	if e.ScrollModel().Content() != 4 {
		t.Fatal("prepared width lost folded rows")
	}
}
