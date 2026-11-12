package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMultipleOccurrenceNativeInputAndOneUndo(t *testing.T) {
	e := loadEditorText(t, "occurrences.txt", "name + name\r\nname\r\n")
	e.SetCursorPosition(0, 2)
	e.HandleEvent(ActionMsg{ID: "select.nextOccurrence"}) // select the current word
	e.HandleEvent(ActionMsg{ID: "select.nextOccurrence"}) // extend without changing source
	e.HandleEvent(ActionMsg{ID: "select.nextOccurrence"})
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "X + X\r\nX\r\n" {
		t.Fatal("native typing did not replace all selected occurrences", e.Value())
	}
	e.Undo()
	if e.Value() != "name + name\r\nname\r\n" {
		t.Fatal("multiple typing did not Undo once", e.Value())
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if e.Value() != "Y + Y\r\nY\r\n" {
		t.Fatal("Undo did not restore all selections", e.Value())
	}
}

func TestMultipleCaretNativePasteCopiesLiteralBlockAndEscape(t *testing.T) {
	e := loadEditorText(t, "carets.txt", "ab\ncd\nef")
	e.SetCursorPosition(0, 1)
	e.HandleEvent(ActionMsg{ID: "select.caretBelow"})
	e.HandleEvent(ActionMsg{ID: "select.caretBelow"})
	e.HandleEvent(tea.PasteMsg{Content: "1\n2"})
	if e.Value() != "a1\n2b\nc1\n2d\ne1\n2f" {
		t.Fatal("paste distributed or omitted literal source", e.Value())
	}
	e.Undo()
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEscape})
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "ab\ncd\neXf" {
		t.Fatal("Escape did not keep only the primary caret", e.Value())
	}
}

func TestMultipleNativeDuplicateCaseAndDisjointPeers(t *testing.T) {
	e := loadEditorText(t, "text.txt", "word\r\nword\r\n")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyF3, Mod: tea.ModAlt})
	if e.CaretCount() != 2 || e.SelectedText() != "word\r\nword" {
		t.Fatal("all-occurrence native action did not select both source words")
	}
	before := e.SourceSelections()
	e.HandleEvent(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if e.Value() != "wordword\r\nwordword\r\n" || e.SelectedText() != "word\r\nword" {
		t.Fatal("Ctrl+D did not duplicate every selected range", e.Value())
	}
	e.Undo()
	if !reflect.DeepEqual(before, e.SourceSelections()) {
		t.Fatal("Undo lost multiple source ranges")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'u', Mod: tea.ModAlt})
	if e.Value() != "WORD\r\nWORD\r\n" {
		t.Fatal("case change applied only to a primary selection", e.Value())
	}
	e.Undo()
	other := e.NewLinkedView()
	defer other.Close()
	other.SetCursorPosition(0, 0)
	other.HandleEvent(tea.PasteMsg{Content: "prefix\r\n"})
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "prefix\r\nX\r\nX\r\n" {
		t.Fatal("peer insertion changed the selected source", e.Value(), e.SourceSelections())
	}
	if other.CaretCount() != 1 {
		t.Fatal("selection leaked to another view")
	}
}
func TestMultipleMotionKeepsEachGoalAcrossShortLinesAndWidths(t *testing.T) {
	e := loadEditorText(t, "motion.txt", "\tlonger\nx\n\tlonger\nx\n\tlonger\nx\n\tlonger")
	e.SetCursorPosition(0, 8)
	paintEditor(e, 60, 12)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 8, Y: 4, Button: tea.MouseLeft, Mod: tea.ModAlt})
	for range 2 {
		e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	want := "\tlonger\nx\n\tlongXer\nx\n\tlonger\nx\n\tlongXer"
	if e.Value() != want {
		t.Fatal("secondary vertical goal drifted on a short line", e.Value())
	}
	e.Undo()
	anchors := e.SourceSelections()
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	if !reflect.DeepEqual(anchors, e.SourceSelections()) {
		t.Fatal("geometry changed multi-caret source offsets")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	if e.SelectedText() != "g\ng" {
		t.Fatal("shift motion selected a different source unit", e.SelectedText())
	}
}
func TestMultipleRectangularSourcePaintAndCapture(t *testing.T) {
	e := loadEditorText(t, "rectangle.txt", "ab12ef\nab34ef\nab56ef\n")
	b := paintEditor(e, 48, 10)
	_ = b
	left := e.GutterWidth()
	e.HandleEvent(tea.MouseClickMsg{X: left + 2, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseMotionMsg{X: left + 4, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	if !e.IsMouseDragging() {
		t.Fatal("rectangular gesture lost capture before release")
	}
	e.HandleEvent(tea.MouseReleaseMsg{X: left + 4, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	if e.IsMouseDragging() || e.CaretCount() != 3 || e.SelectedText() != "12\n34\n56" {
		t.Fatal("rectangle did not select source columns", e.SelectedText(), e.CaretCount())
	}
	b = paintEditor(e, 48, 10)
	for row := 0; row < 3; row++ {
		if !b.Get(left+2, row).BG.Equal(b.Get(left+3, row).BG) || b.Get(left+2, row).BG.Equal(b.Get(left+1, row).BG) {
			t.Fatal("secondary selection not painted", row)
		}
	}
	e.HandleEvent(tea.PasteMsg{Content: "界"})
	if e.Value() != "ab界ef\nab界ef\nab界ef\n" {
		t.Fatal("rectangle paste edited other columns", e.Value())
	}
	e.Undo()
	e.HandleEvent(tea.MouseClickMsg{X: left + 2, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseReleaseMsg{X: left + 2, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	if e.CaretCount() != 3 || e.HasSelection() {
		t.Fatal("empty rectangle did not retain carets")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "abX12ef\nabX34ef\nabX56ef\n" {
		t.Fatal(e.Value())
	}
}
func TestMultipleRecoveryRejectsInvalidBoundaryAndKeepsPendingTransaction(t *testing.T) {
	e, s := projectionEditor(t, "界word\r\n界word\r\n")
	e.SetCursorPosition(0, 2)
	e.HandleEvent(ActionMsg{ID: "select.allOccurrences"})
	e.HandleEvent(tea.PasteMsg{Content: "Z"})
	raw, unresolved := e.ExportDocumentRecovery()
	if !unresolved {
		t.Fatal("pending vector not retained")
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var r DocumentRecovery
	if err := json.Unmarshal(bytes, &r); err != nil {
		t.Fatal(err)
	}
	restored, _ := projectionEditor(t, string(s.Text))
	if err := restored.RestoreDocumentRecovery(r); err != nil {
		t.Fatal(err)
	}
	if restored.Value() != e.Value() || !reflect.DeepEqual(restored.SourceSelections(), e.SourceSelections()) {
		t.Fatal("recovery lost selected source")
	}
	r.Secondary = []SourceSelection{{Anchor: 1, Cursor: 3}} // the original wide character no longer exists; validate the optimistic source below
	r.OptimisticText = []byte("é\r\né")
	r.Cursor, r.SelectionAnchor, r.SelectionEnd = 0, 0, 0
	r.Canonical.Text = r.OptimisticText
	r.Pending = nil
	r.PendingGroups = nil
	if err := r.Validate(); err == nil {
		t.Fatal("recovery accepted a split grapheme")
	}
}
func TestMultipleSourceReplacementClearsStaleCarets(t *testing.T) {
	e := loadEditorText(t, "replace.txt", "one\none")
	e.HandleEvent(ActionMsg{ID: "select.allOccurrences"})
	e.ReplaceDocument("new")
	if e.CaretCount() != 1 {
		t.Fatal("whole source replacement retained old multiple ranges")
	}
	e.HandleEvent(tea.PasteMsg{Content: "X"})
	if !strings.Contains(e.Value(), "X") {
		t.Fatal("typing failed after reset")
	}
}
func TestMultipleCanonicalPeerMergeNeverEditsTheSameOffsetTwice(t *testing.T) {
	e, s := projectionEditor(t, "a---a---a")
	e.SetCursorPosition(0, 0)
	e.HandleEvent(ActionMsg{ID: "select.allOccurrences"})
	change := document.Change{DocumentID: s.ID, BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 8}}}
	s.Text = []byte("a")
	s.Revision++
	if err := e.ApplyDocumentDelta(change, s); err != nil {
		t.Fatal(err)
	}
	e.HandleEvent(tea.PasteMsg{Content: "X"})
	if e.Value() != "X" || e.CaretCount() != 1 {
		t.Fatal("merged peer ranges inserted duplicate text", e.Value(), e.SourceSelections())
	}
}
func BenchmarkMultipleCaretWorkload(b *testing.B) {
	for _, count := range []int{1, 32, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			e := NewPreview(theme.DefaultDark(), "dense.txt", strings.Repeat("word alpha 界 é\n", 20000), 1)
			defer e.Close()
			e.readOnly = false
			var ranges []selectionSpan
			for line := 0; line < count; line++ {
				off := e.buf.positionToOffset(Position{Line: line, Col: 4})
				ranges = append(ranges, selectionSpan{SourceSelection: SourceSelection{off, off}, primary: line == count-1})
			}
			if err := e.setSelections(ranges); err != nil {
				b.Fatal(err)
			}
			e.ScrollToCursor()
			paintEditor(e, 120, 40)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
				paintEditor(e, 120, 40)
				e.Undo()
			}
		})
	}
}

func TestMultipleLineCommandsShareOneVectorAndUndo(t *testing.T) {
	for _, tc := range []struct {
		id   ui.ActionID
		want string
	}{
		{"edit.toggleComment", "// one\nkeep\n// two\nend"},
		{"edit.moveLineDown", "keep\none\nend\ntwo"},
		{"edit.deleteLine", "keep\nend"},
		{"edit.joinLines", "one keep\ntwo end"},
		{"edit.indent", "    one\nkeep\n    two\nend"},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			e := loadEditorText(t, "source.go", "one\nkeep\ntwo\nend")
			e.SetCursorPosition(0, 1)
			paintEditor(e, 60, 10)
			e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 1, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
			before := e.SourceSelections()
			e.HandleEvent(ActionMsg{ID: tc.id})
			if e.Value() != tc.want {
				t.Fatal("command omitted a caret's source", e.Value(), tc.want)
			}
			e.Undo()
			if e.Value() != "one\nkeep\ntwo\nend" || !reflect.DeepEqual(before, e.SourceSelections()) {
				t.Fatal("line transaction did not Undo once", e.Value(), e.SourceSelections())
			}
		})
	}
}
func TestMultipleIndentAndCommentOnlyTouchEachSourceLineOnce(t *testing.T) {
	e := loadEditorText(t, "source.go", "one word\ntwo word")
	e.SetCursorPosition(0, 1)
	paintEditor(e, 60, 10)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 5, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	if e.Value() != "// one word\ntwo word" || e.CaretCount() != 2 {
		t.Fatal("line was commented once per caret", e.Value())
	}
	e.Undo()
	e.HandleEvent(ActionMsg{ID: "edit.moveLineDown"})
	if e.Value() != "two word\none word" || e.CaretCount() != 2 {
		t.Fatal("same line was moved twice", e.Value())
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "two word\noXne wXord" {
		t.Fatal("line motion lost source anchors", e.Value())
	}
}

func TestMultipleSameLineDuplicatePreservesBothCarets(t *testing.T) {
	e := loadEditorText(t, "dup.txt", "abcd\nend")
	e.SetCursorPosition(0, 1)
	paintEditor(e, 40, 8)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 3, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if e.Value() != "abcd\nabcd\nend" || e.CaretCount() != 2 {
		t.Fatal("duplicating one source line repeated or lost carets", e.Value())
	}
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if e.Value() != "abcd\naXbcXd\nend" {
		t.Fatal(e.Value())
	}
}
func TestMultipleExpansionShrinksWholePrivateSet(t *testing.T) {
	e := loadEditorText(t, "exp.txt", "one + two\nend\nthree + four")
	e.SetCursorPosition(0, 1)
	paintEditor(e, 50, 10)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 1, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	before := e.SourceSelections()
	for range 2 {
		e.HandleEvent(tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	}
	if e.SelectedText() != "one + two\nthree + four" {
		t.Fatal(e.SelectedText())
	}
	for range 2 {
		e.HandleEvent(tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt})
	}
	if !reflect.DeepEqual(before, e.SourceSelections()) {
		t.Fatal("shrink lost a caret", e.SourceSelections())
	}
}
func TestMultipleExactVectorDoesNotRemeasureUnchangedFileWidth(t *testing.T) {
	e := loadEditorText(t, "width.txt", strings.Repeat("short line\n", 20000))
	paintEditor(e, 60, 8)
	e.SetCursorPosition(0, 1)
	e.HandleEvent(ActionMsg{ID: "select.caretBelow"})
	before := e.hWidthMeasures
	e.HandleEvent(tea.PasteMsg{Content: strings.Repeat("x", 100)})
	paintEditor(e, 60, 8)
	if n := e.hWidthMeasures - before; n > 2 || e.hScrollModel.MaxOffset() == 0 {
		t.Fatal("multi-edit rebuilt the document width or missed changed lines", n)
	}
	e.Undo()
	paintEditor(e, 60, 8)
	if n := e.hWidthMeasures - before; n > 4 || e.hScrollModel.MaxOffset() != 0 {
		t.Fatal("Undo rebuilt unchanged widths or retained a stale scrollbar", n)
	}
}

func TestMultipleSelectionLimitLeavesWholeViewUntouched(t *testing.T) {
	e := loadEditorText(t, "many.txt", strings.Repeat("word\n", MaxCarets+1))
	e.SetCursorPosition(0, 2)
	before := e.captureViewState()
	cmd := e.HandleEvent(ActionMsg{ID: "select.allOccurrences"})
	if cmd == nil || !strings.Contains(cmd().(SelectionNoticeMsg).Message, "too many matches") || e.captureViewState() != before {
		t.Fatal("limit partially changed selections")
	}
}
func TestMultipleRectanglesAndCaretsUseWrappedRows(t *testing.T) {
	e := loadEditorText(t, "wrap.txt", "abcdefghijklmnopqr\nabcdefghijklmnopqr\n")
	e.SetPresentation(wrapSettings(4))
	w := e.GutterWidth() + 11
	paintWrapped(t, e, w, 10)
	e.SetCursorPosition(0, 1)
	x := e.GutterWidth()
	e.HandleEvent(tea.MouseClickMsg{X: x + 1, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseReleaseMsg{X: x + 1, Y: 2, Button: tea.MouseLeft})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnd})
	e.cursorBlink.ResetAt(0)
	surf := surface.NewSurface("carets", w, 10, cell.TransparentBlank())
	e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true, Frame: 0})
	for _, row := range []int{0, 2} {
		if !surf.Buffer().Get(x+9, row).Attr.Has(cell.AttrReverse) {
			t.Fatal("caret jumped to the next row at the same source offset", row)
		}
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEscape})
	e.HandleEvent(tea.MouseClickMsg{X: x + 2, Y: 0, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseReleaseMsg{X: x + 3, Y: 2, Button: tea.MouseLeft})
	if got := e.SelectedText(); got != "c\nl\nc" {
		t.Fatal("rectangle did not follow visible wrap rows", got)
	}
	e.HandleEvent(tea.PasteMsg{Content: "X"})
	if e.Value() != "abXdefghijkXmnopqr\nabXdefghijklmnopqr\n" {
		t.Fatal("wrapped rectangle wrote wrong source", e.Value())
	}
	e.Undo()
	if e.SelectedText() != "c\nl\nc" {
		t.Fatal("Undo lost wrapped rectangle")
	}
}
func TestMultipleSelectionRetiresSnippetWithoutChangingSource(t *testing.T) {
	e := loadEditorText(t, "snippet.txt", "\nword")
	if err := e.ApplyCompletion(numericCompletion(t, "${1:word} / $1$0")); err != nil {
		t.Fatal(err)
	}
	source := e.Value()
	e.HandleEvent(ActionMsg{ID: "select.allOccurrences"})
	if e.SnippetActive() || e.Value() != source || e.CaretCount() != 3 {
		t.Fatal("new multi-selection retained a second Tab owner")
	}
	before := e.Value()
	if err := e.ApplyCompletion(numericCompletion(t, "oops")); err == nil || e.Value() != before {
		t.Fatal("completion partly applied to multiple carets")
	}
}

func BenchmarkMultipleCaretStages(b *testing.B) {
	for _, count := range []int{1, 32, 256} {
		for _, stage := range []string{"mutation", "paint", "undo"} {
			b.Run(fmt.Sprintf("%d/%s", count, stage), func(b *testing.B) {
				e := NewPreview(theme.DefaultDark(), "dense.txt", strings.Repeat("word alpha 界 é\n", 20000), 1)
				defer e.Close()
				e.readOnly = false
				var spans []selectionSpan
				for line := 0; line < count; line++ {
					off := e.buf.positionToOffset(Position{Line: line, Col: 4})
					spans = append(spans, selectionSpan{SourceSelection: SourceSelection{off, off}, primary: line == count-1})
				}
				if err := e.setSelections(spans); err != nil {
					b.Fatal(err)
				}
				e.ScrollToCursor()
				paintEditor(e, 120, 40)
				key := tea.KeyPressMsg{Code: 'X', Text: "X"}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					switch stage {
					case "mutation":
						e.HandleEvent(key)
						b.StopTimer()
						paintEditor(e, 120, 40)
						e.Undo()
						b.StartTimer()
					case "paint":
						b.StopTimer()
						e.HandleEvent(key)
						b.StartTimer()
						paintEditor(e, 120, 40)
						b.StopTimer()
						e.Undo()
						b.StartTimer()
					case "undo":
						b.StopTimer()
						e.HandleEvent(key)
						paintEditor(e, 120, 40)
						b.StartTimer()
						e.Undo()
					}
				}
			})
		}
	}
}

func TestMultipleFoldHidesOnlyCaretInkAndEditRevealsItsSource(t *testing.T) {
	e := loadEditorText(t, "fold.go", foldFixture)
	paintEditor(e, 60, 12)
	e.SetCursorPosition(3, 8)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 1, Y: 6, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseReleaseMsg{X: e.GutterWidth() + 1, Y: 6, Button: tea.MouseLeft})
	e.HandleEvent(ActionMsg{ID: "view.foldAll"})
	e.cursorBlink.ResetAt(0)
	surf := surface.NewSurface("fold", 60, 12, cell.TransparentBlank())
	e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true, Frame: 0})
	count := 0
	for y := 0; y < 12; y++ {
		for x := e.GutterWidth(); x < 59; x++ {
			if surf.Buffer().Get(x, y).Attr.Has(cell.AttrReverse) {
				count++
			}
		}
	}
	if count != 1 || e.CaretCount() != 2 {
		t.Fatal("hidden source painted a caret on a different visible line", count, e.CaretCount())
	}
	e.HandleEvent(tea.PasteMsg{Content: "X"})
	paintEditor(e, 60, 12)
	if e.Value() != strings.Replace(strings.Replace(foldFixture, "call()", "Xcall()", 1), "last", "lXast", 1) || len(e.folds.collapsed) != 0 {
		t.Fatal("edit did not reveal each caret's source", e.Value())
	}
	e.Undo()
	if e.Value() != foldFixture || e.CaretCount() != 2 {
		t.Fatal("folding lost multiple Undo state")
	}
}

func TestMultipleVerticalGoalsResetWithTabGeometry(t *testing.T) {
	source := strings.Repeat("\tabcdef\n", 6)
	e := loadEditorText(t, "goals.txt", source)
	paintEditor(e, 60, 12)
	e.SetCursorPosition(0, 6)
	e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 6, Y: 2, Button: tea.MouseLeft, Mod: tea.ModAlt})
	e.HandleEvent(tea.MouseReleaseMsg{X: e.GutterWidth() + 6, Y: 2, Button: tea.MouseLeft})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	e.SetPresentation(types.EditorSettings{TabSize: 8, LineNumbers: true})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	e.HandleEvent(tea.PasteMsg{Content: "X"})
	lines := strings.Split(source, "\n")
	lines[2], lines[4] = "\tabXcdef", "\tabXcdef"
	if e.Value() != strings.Join(lines, "\n") {
		t.Fatal("old secondary goal used old display tab stops", e.Value())
	}
}
