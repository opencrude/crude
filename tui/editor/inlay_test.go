package editor

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func newInlayTestEditor(t testing.TB, text string) *Editor {
	t.Helper()
	e := New(theme.DefaultDark())
	t.Cleanup(e.Close)
	s := document.Snapshot{
		ID: "inlay-document", Path: "/virtual/inlay.txt", Revision: 41,
		AnalysisGeneration: "inlay-workspace", AnalysisLifetime: "inlay-open",
		Text: []byte(text), Saved: []byte(text), DiskExists: true,
		DiskHash: sha256.Sum256([]byte(text)),
	}
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	e.SetStatusChrome(false)
	return e
}

func inlayTestSet(e *Editor, hints ...types.LSPInlayHint) types.LSPInlaySet {
	s := e.DocumentSnapshot()
	return types.LSPInlaySet{
		ID: "inlay-set", Answer: types.LSPInlayHints,
		Boundary: types.InsightBoundary{Source: types.InsightSource{
			WorkspaceGeneration: s.AnalysisGeneration, DocumentID: s.ID, Path: s.Path,
			Lifetime: s.AnalysisLifetime, Revision: s.Revision, Kind: "canonical",
		}},
		Hints: hints,
	}
}

func installTestInlays(t testing.TB, e *Editor, hints ...types.LSPInlayHint) {
	t.Helper()
	if err := e.SetInlayHints(inlayTestSet(e, hints...)); err != nil {
		t.Fatal(err)
	}
}

func renderTestInlays(e *Editor, w, h int, focused bool) *surface.CellBuffer {
	s := surface.NewSurface("inlay-acceptance", w, h, cell.TransparentBlank())
	e.Render(s, ui.PaintContext{Theme: e.th, Focused: focused, Frame: 0})
	return s.Buffer()
}

// Expectations below are literal cell starts, complete glyphs and widths. They
// are deliberately not generated with the projection's conversion helpers.
func requireInlayGlyph(t testing.TB, b *surface.CellBuffer, x, y int, glyph string, width int) {
	t.Helper()
	c := b.Get(x, y)
	text := c.Content
	if text == "" {
		text = string(c.Char)
	}
	if text != glyph || int(c.Width) != width {
		t.Fatalf("cell (%d,%d) = %q width %d; want %q width %d", x, y, text, c.Width, glyph, width)
	}
	for step := 1; step < width; step++ {
		if continuation := b.Get(x+step, y); continuation.Width != 0 {
			t.Fatalf("cell (%d,%d) is not a wide-glyph continuation: %+v", x+step, y, continuation)
		}
	}
}

func TestEditorInlayLiteralUnicodeRenderCaretAndHit(t *testing.T) {
	// Source byte boundaries: a=0, tab=1, 中=2, 😀=5, é=9,
	// 👩‍💻=12, Z=23, CRLF=24, final empty-line EOF=26.
	const source = "a\t中😀e\u0301👩‍💻Z\r\n"
	e := newInlayTestEditor(t, source)
	renderTestInlays(e, 60, 8, false)
	e.SetSelection(0, 4, 0, 9)
	beforeCursor, beforeAnchor, beforeEnd := e.cursor, e.selAnchor, e.selEnd
	revision, generation := e.Revision(), e.DiskGeneration()
	installTestInlays(t, e,
		types.LSPInlayHint{Index: 0, SourceByte: 2, Position: types.LSPInlayPosition{Character: 2}, Label: ":T", PaddingLeft: true, PaddingRight: true},
		types.LSPInlayHint{Index: 1, SourceByte: 2, Position: types.LSPInlayPosition{Character: 2}, Label: "😀", PaddingRight: true},
		types.LSPInlayHint{Index: 2, SourceByte: 24, Position: types.LSPInlayPosition{Character: 13}, Label: "!"},
		types.LSPInlayHint{Index: 3, SourceByte: 26, Position: types.LSPInlayPosition{Line: 1}, Label: "EOF"},
	)
	if e.cursor != beforeCursor || e.selAnchor != beforeAnchor || e.selEnd != beforeEnd ||
		e.Revision() != revision || e.DiskGeneration() != generation || e.Modified() ||
		e.Value() != source || len(e.buf.changes) != 0 || e.DocumentState().PendingEdits != 0 {
		t.Fatal("showing labels changed source, selection, caret, history or savepoint")
	}
	b := renderTestInlays(e, 60, 8, false)
	x := e.gutterWidth()
	for _, c := range []struct {
		col, width int
		glyph      string
	}{
		{0, 1, "a"}, {1, 1, " "}, {2, 1, " "}, {3, 1, " "},
		{4, 1, " "}, {5, 1, ":"}, {6, 1, "T"}, {7, 1, " "},
		{8, 2, "😀"}, {10, 1, " "}, {11, 2, "中"}, {13, 2, "😀"},
		{15, 1, "e\u0301"}, {16, 2, "👩‍💻"}, {18, 1, "Z"}, {19, 1, "!"},
	} {
		requireInlayGlyph(t, b, x+c.col, 0, c.glyph, c.width)
	}
	for col, ch := range "EOF" {
		requireInlayGlyph(t, b, x+col, 1, string(ch), 1)
	}
	for _, span := range []struct{ line, first, end, index, anchor int }{
		{0, 4, 8, 0, 4}, {0, 8, 11, 1, 4}, {0, 19, 20, 2, 12}, {1, 0, 3, 3, 0},
	} {
		for col := span.first; col < span.end; col++ {
			if index, ok := e.InlayHintAt(x+col, span.line); !ok || index != span.index {
				t.Fatalf("label/padding cell (%d,%d) hit=(%d,%v), want original index %d", col, span.line, index, ok, span.index)
			}
			e.lastClickAt = time.Time{} // Separate independent native presses.
			e.HandleEvent(tea.MouseClickMsg{X: x + col, Y: span.line, Button: tea.MouseLeft})
			e.HandleEvent(tea.MouseReleaseMsg{X: x + col, Y: span.line, Button: tea.MouseLeft})
			if e.cursor != (Position{Line: span.line, Col: span.anchor}) || e.HasSelection() {
				t.Fatalf("label cell (%d,%d) caret=%v selection=%q, want exact anchor (%d,%d)", col, span.line, e.cursor, e.SelectedText(), span.line, span.anchor)
			}
		}
	}
	for _, pos := range [][2]int{{x - 1, 0}, {x, 0}, {x + 11, 0}, {x + 20, 0}, {x + 3, 1}, {59, 0}, {x, 8}} {
		if _, ok := e.InlayHintAt(pos[0], pos[1]); ok {
			t.Fatalf("non-label cell %v acquired an inlay identity", pos)
		}
	}
	e.ClearSelection()
	e.SetCursorPosition(0, 4)
	e.cursorBlink.ResetAt(0)
	b = renderTestInlays(e, 60, 8, true)
	if c := b.Get(x+11, 0); c.Content != "中" || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("source anchor caret must paint AFTER complete same-anchor group at col 11: %+v", c)
	}
	for col := 4; col < 11; col++ {
		if b.Get(x+col, 0).Attr.Has(cell.AttrReverse) {
			t.Fatalf("caret painted inside label at col %d", col)
		}
	}
	e.SetSelection(0, 4, 0, 9)
	cursor, anchor, end := e.cursor, e.selAnchor, e.selEnd
	e.ClearInlayHints()
	b = renderTestInlays(e, 60, 8, false)
	requireInlayGlyph(t, b, x+4, 0, "中", 2)
	if e.cursor != cursor || e.selAnchor != anchor || e.selEnd != end || e.SelectedText() != "中😀e\u0301" || e.Value() != source {
		t.Fatal("clearing labels changed source positions, selected bytes or source")
	}
}

func TestEditorInlaySelectionSearchDiagnosticsRemainSourceOnly(t *testing.T) {
	e := newInlayTestEditor(t, "foo bar")
	installTestInlays(t, e, types.LSPInlayHint{
		Index: 0, SourceByte: 0, Label: "foo", PaddingRight: true,
	})
	e.SetDiagnostics(testDiagnosticFacts(e, []LSPDiagnostic{{Line: 0, Col: 0, EndCol: 3, Severity: DiagError}}))
	if count := e.SetQuery("foo"); count != 1 {
		t.Fatalf("find must see one source occurrence, not the label: %d", count)
	}
	b := renderTestInlays(e, 40, 8, false)
	x, bg := e.gutterWidth(), e.cellColor(e.th.Colors.Bg)
	for col := 0; col < 4; col++ {
		c := b.Get(x+col, 0)
		if c.BG != bg || c.Attr.Has(cell.AttrUnderline) || c.Attr.Has(cell.AttrUndercurl) {
			t.Fatalf("label/padding inherited source search/diagnostic paint at col %d: %+v", col, c)
		}
	}
	for col := 4; col < 7; col++ {
		c := b.Get(x+col, 0)
		if c.BG != e.searchHighlightBg(true) || !c.Attr.Has(cell.AttrUndercurl) {
			t.Fatalf("source foo lacks search and diagnostic paint at col %d: %+v", col, c)
		}
	}
	e.SetSelection(0, 0, 0, 3)
	b = renderTestInlays(e, 40, 8, false)
	for col := 0; col < 7; col++ {
		selected := b.Get(x+col, 0).BG == e.tokens.Color(ui.ColorTextSelection)
		if selected != (col >= 4) {
			t.Fatalf("selection paint col %d = %v, want source cells 4..6 only", col, selected)
		}
	}
	if e.SelectedText() != "foo" {
		t.Fatalf("copy source=%q, want foo without display prefix", e.SelectedText())
	}
	if start, end, ok := e.SourceByteRange(); !ok || start != 0 || end != 3 {
		t.Fatalf("source byte selection=(%d,%d,%v), want (0,3,true)", start, end, ok)
	}
}

func TestEditorInlayHoverNeverBorrowsTheFollowingSourceDiagnostic(t *testing.T) {
	e := newInlayTestEditor(t, "foo")
	installTestInlays(t, e, types.LSPInlayHint{Index: 0, Label: "type", PaddingRight: true})
	e.SetDiagnostics(testDiagnosticFacts(e, []LSPDiagnostic{{Line: 0, Col: 0, EndCol: 3, Severity: DiagError, Message: "source-only issue"}}))
	renderTestInlays(e, 40, 8, false)
	x := e.gutterWidth()
	for col := 0; col < 5; col++ {
		e.HandleEvent(tea.MouseMotionMsg{X: x + col})
		if len(e.HoverState().Messages) != 0 {
			t.Fatalf("hint/padding cell %d borrowed an unrelated source diagnostic", col)
		}
	}
	e.HandleEvent(tea.MouseMotionMsg{X: x + 5})
	if !reflect.DeepEqual(e.HoverState().Messages, []string{"source-only issue"}) {
		t.Fatal("following source cell lost its actual diagnostic hover", e.HoverState())
	}
}

func TestEditorInlayNativeDragAndDoubleClickUseSourceWords(t *testing.T) {
	for _, tc := range []struct {
		name           string
		press, release int
		clicks         int
		want           string
	}{
		{"forward through label", 0, 12, 1, "alpha b"},
		{"backward from label is an anchor", 7, 2, 1, "pha "},
		{"double click source word", 11, 11, 2, "beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInlayTestEditor(t, "alpha beta")
			installTestInlays(t, e, types.LSPInlayHint{Index: 0, SourceByte: 6, Position: types.LSPInlayPosition{Character: 6}, Label: "hint "})
			renderTestInlays(e, 40, 8, false)
			x := e.gutterWidth()
			for i := 0; i < tc.clicks; i++ {
				e.HandleEvent(tea.MouseClickMsg{X: x + tc.press, Button: tea.MouseLeft})
				if i+1 < tc.clicks {
					e.HandleEvent(tea.MouseReleaseMsg{X: x + tc.press, Button: tea.MouseLeft})
				}
			}
			e.HandleEvent(tea.MouseMotionMsg{X: x + tc.release, Button: tea.MouseLeft})
			e.HandleEvent(tea.MouseReleaseMsg{X: x + tc.release, Button: tea.MouseLeft})
			if got := e.SelectedText(); got != tc.want {
				t.Fatalf("native gesture selected %q, want source-only %q", got, tc.want)
			}
		})
	}
}

func TestEditorInlayTypingAndCanonicalUndoRedoContainOnlySource(t *testing.T) {
	const source = "a\t中😀e\u0301👩‍💻Z\r\n"
	e := newInlayTestEditor(t, source)
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var intent DocumentUndoIntent
	e.SetOnDocumentUndo(func(i DocumentUndoIntent) { intent = i })
	installTestInlays(t, e, types.LSPInlayHint{Index: 0, SourceByte: 2, Position: types.LSPInlayPosition{Character: 2}, Label: ":type"})
	renderTestInlays(e, 50, 8, false)
	x := e.gutterWidth()
	e.HandleEvent(tea.MouseClickMsg{X: x + 6, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x + 6, Button: tea.MouseLeft})
	e.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if e.Value() != "a\tx中😀e\u0301👩‍💻Z\r\n" || !e.Modified() {
		t.Fatalf("typing at label anchor changed wrong bytes: %q", e.Value())
	}
	b := renderTestInlays(e, 50, 8, false)
	requireInlayGlyph(t, b, x+4, 0, "x", 1)
	if _, ok := e.InlayHintAt(x+6, 0); ok {
		t.Fatal("source mutation retained old inlay hit projection")
	}
	change, err := e.CaptureDocumentEdits(projectionOwner, "inlay-typed", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(change.Edits, []document.Edit{{Start: 2, End: 2, Text: "x"}}) {
		t.Fatalf("canonical edit includes labels or display positions: %+v", change.Edits)
	}
	if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: change.DocumentID, RequestID: change.RequestID, Before: 41, Revision: 42, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	for _, history := range []struct {
		key   rune
		redo  bool
		text  string
		edit  document.Edit
		dirty bool
	}{
		{'z', false, source, document.Edit{Start: 2, End: 3}, false},
		{'y', true, "a\tx中😀e\u0301👩‍💻Z\r\n", document.Edit{Start: 2, End: 2, Text: "x"}, true},
	} {
		e.HandleEvent(tea.KeyPressMsg{Code: history.key, Mod: tea.ModCtrl})
		if !e.DocumentState().UndoPending || intent.Redo != history.redo {
			t.Fatalf("native history key did not route exact canonical intent: %+v", intent)
		}
		s := e.DocumentSnapshot()
		before := s.Revision
		s.Revision++
		s.Text = []byte(history.text)
		if err := e.CompleteDocumentUndo(intent, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.ApplyDocumentDelta(document.Change{DocumentID: s.ID, BaseRevision: before, Edits: []document.Edit{history.edit}}, s); err != nil {
			t.Fatal(err)
		}
		e.CompleteDocumentHistorySync()
		if e.Value() != history.text || e.Modified() != history.dirty {
			t.Fatalf("history source=%q dirty=%v, want %q/%v", e.Value(), e.Modified(), history.text, history.dirty)
		}
	}
}

func TestEditorInlayRejectsInvalidAnchorsAtomically(t *testing.T) {
	const source = "a😀e\u0301👩‍💻Z\r\n"
	for _, tc := range []struct {
		name string
		hint types.LSPInlayHint
	}{
		{"split surrogate", types.LSPInlayHint{SourceByte: 1, Position: types.LSPInlayPosition{Character: 2}, Label: "bad"}},
		{"combining interior", types.LSPInlayHint{SourceByte: 6, Position: types.LSPInlayPosition{Character: 4}, Label: "bad"}},
		{"ZWJ interior", types.LSPInlayHint{SourceByte: 12, Position: types.LSPInlayPosition{Character: 7}, Label: "bad"}},
		{"byte UTF16 disagreement", types.LSPInlayHint{SourceByte: 5, Position: types.LSPInlayPosition{Character: 1}, Label: "bad"}},
		{"negative byte", types.LSPInlayHint{SourceByte: -1, Label: "bad"}},
		{"negative line", types.LSPInlayHint{Position: types.LSPInlayPosition{Line: -1}, Label: "bad"}},
		{"missing line", types.LSPInlayHint{Position: types.LSPInlayPosition{Line: 2}, Label: "bad"}},
		{"past EOL", types.LSPInlayHint{SourceByte: 20, Position: types.LSPInlayPosition{Character: 12}, Label: "bad"}},
		{"negative original index", types.LSPInlayHint{Index: -1, Label: "bad"}},
		{"invalid UTF8 label", types.LSPInlayHint{Label: "\xff"}},
		{"zero width label", types.LSPInlayHint{Label: "\u200b"}},
		{"oversize label", types.LSPInlayHint{Label: strings.Repeat("x", 4097)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInlayTestEditor(t, source)
			installTestInlays(t, e, types.LSPInlayHint{Label: "OK"})
			e.SetSelection(0, 1, 0, 3)
			cursor, anchor, end, revision := e.cursor, e.selAnchor, e.selEnd, e.Revision()
			if err := e.SetInlayHints(inlayTestSet(e, tc.hint)); err == nil {
				t.Fatal("invalid placement was accepted/clamped")
			}
			b := renderTestInlays(e, 40, 8, false)
			requireInlayGlyph(t, b, e.gutterWidth(), 0, "O", 1)
			requireInlayGlyph(t, b, e.gutterWidth()+1, 0, "K", 1)
			if e.Value() != source || e.Revision() != revision || e.cursor != cursor ||
				e.selAnchor != anchor || e.selEnd != end || e.Modified() || len(e.buf.changes) != 0 {
				t.Fatal("rejected replacement changed source, view, history or savepoint")
			}
		})
	}
	e := newInlayTestEditor(t, source)
	if err := e.SetInlayHints(inlayTestSet(e, types.LSPInlayHint{Label: "one"}, types.LSPInlayHint{Label: "duplicate"})); err == nil {
		t.Fatal("duplicate original indices accepted")
	}
}

func TestEditorInlayCanonicalBoundaryAndPendingEqualText(t *testing.T) {
	for _, boundary := range []string{"workspace", "document", "path", "lifetime", "revision", "disk"} {
		t.Run(boundary, func(t *testing.T) {
			e := newInlayTestEditor(t, "abc")
			set := inlayTestSet(e, types.LSPInlayHint{Label: "hint"})
			switch boundary {
			case "workspace":
				set.Boundary.Source.WorkspaceGeneration = "other"
			case "document":
				set.Boundary.Source.DocumentID = "other"
			case "path":
				set.Boundary.Source.Path = "/virtual/other"
			case "lifetime":
				set.Boundary.Source.Lifetime = "reopened"
			case "revision":
				set.Boundary.Source.Revision++
			case "disk":
				set.Boundary.Source.Kind = "disk"
			}
			if err := e.SetInlayHints(set); err == nil || e.Value() != "abc" {
				t.Fatal("nonmatching canonical source admitted or changed source")
			}
		})
	}
	for _, reason := range []string{"pending equal text", "equal-text revision", "equal-text lifetime"} {
		t.Run(reason, func(t *testing.T) {
			e := newInlayTestEditor(t, "abc")
			set := inlayTestSet(e, types.LSPInlayHint{Label: "hint"})
			if err := e.SetInlayHints(set); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "pending equal text":
				e.HandleEvent(tea.KeyPressMsg{Code: 'x', Text: "x"})
				e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
			case "equal-text revision":
				s := e.DocumentSnapshot()
				s.Revision++
				if err := e.ResetDocument(s); err != nil {
					t.Fatal(err)
				}
			case "equal-text lifetime":
				s := e.DocumentSnapshot()
				s.AnalysisLifetime = "reopened"
				if err := e.AcceptDocumentSnapshot(s); err != nil {
					t.Fatal(err)
				}
			}
			b := renderTestInlays(e, 40, 8, false)
			requireInlayGlyph(t, b, e.gutterWidth(), 0, "a", 1)
			if _, ok := e.InlayHintAt(e.gutterWidth(), 0); ok || e.Value() != "abc" {
				t.Fatal("old hints survived equal-text source boundary")
			}
			if err := e.SetInlayHints(set); err == nil {
				t.Fatal("retired hint set was readmitted")
			}
		})
	}
	preview := NewPreview(theme.DefaultDark(), "/virtual/inlay.txt", "abc", 1)
	t.Cleanup(preview.Close)
	canonical := newInlayTestEditor(t, "abc")
	if err := preview.SetInlayHints(inlayTestSet(canonical, types.LSPInlayHint{Label: "hint"})); err == nil {
		t.Fatal("standalone preview accepted canonical hints")
	}
}

func TestEditorInlayHorizontalExtentClippingAndVerticalGoal(t *testing.T) {
	e := newInlayTestEditor(t, "abZ\n0123456789\nab\tQ\n0123456789")
	renderTestInlays(e, 40, 8, false)
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 2, Position: types.LSPInlayPosition{Character: 2}, Label: strings.Repeat("h", 60)})
	renderTestInlays(e, 40, 8, false)
	if !e.hScrollbarVisible || e.hScrollModel.Content() != 63 {
		t.Fatalf("display extent=%d visible=%v, want 63/true", e.hScrollModel.Content(), e.hScrollbarVisible)
	}
	e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	if e.scrollX == 0 || e.scrollY != 0 {
		t.Fatalf("native horizontal wheel scroll=%d,%d", e.scrollX, e.scrollY)
	}
	e.scrollX, e.pendingReveal = 5, false
	b := renderTestInlays(e, 40, 8, false)
	x := e.gutterWidth()
	requireInlayGlyph(t, b, x, 0, "h", 1)
	if index, ok := e.InlayHintAt(x, 0); !ok || index != 0 {
		t.Fatal("horizontally clipped label lost original identity")
	}
	e.lastClickAt = time.Time{}
	e.HandleEvent(tea.MouseClickMsg{X: x, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x, Button: tea.MouseLeft})
	if e.cursor != (Position{Col: 2}) {
		t.Fatalf("scrolled label click caret=%v, want source col 2", e.cursor)
	}
	e.ClearInlayHints()
	renderTestInlays(e, 40, 8, false)
	if e.hScrollbarVisible || e.scrollX != 0 || e.hScrollModel.Content() != 10 {
		t.Fatalf("clear did not shrink extent: content=%d visible=%v scroll=%d", e.hScrollModel.Content(), e.hScrollbarVisible, e.scrollX)
	}

	// A desired display column inside a hint lands at the source anchor; one
	// inside a tab lands at its leading boundary, without losing the goal.
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 2, Position: types.LSPInlayPosition{Character: 2}, Label: "hint"})
	e.SetCursorPosition(1, 3)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyUp})
	if e.cursor != (Position{Col: 2}) || e.goalCol != 3 {
		t.Fatalf("vertical label landing=%v goal=%d, want source col 2, display goal 3", e.cursor, e.goalCol)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	if e.cursor != (Position{Line: 2, Col: 2}) || e.goalCol != 3 {
		t.Fatalf("vertical tab landing=%v goal=%d", e.cursor, e.goalCol)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	if e.cursor != (Position{Line: 3, Col: 3}) {
		t.Fatalf("desired display column was lost: %v", e.cursor)
	}
}

func TestEditorInlayControlsWideClipAndInvalidation(t *testing.T) {
	e := newInlayTestEditor(t, "aZ")
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 1, Position: types.LSPInlayPosition{Character: 1}, Label: "\x1b[31m\n\t\x7f\u2028"})
	b := renderTestInlays(e, 70, 8, false)
	x := e.gutterWidth()
	for col, ch := range `a\x1b[31m\n\t\x7f\u2028Z` {
		requireInlayGlyph(t, b, x+col, 0, string(ch), 1)
	}
	if e.Value() != "aZ" {
		t.Fatal("control escaping changed source")
	}
	e.ClearInlayHints()
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 1, Position: types.LSPInlayPosition{Character: 1}, Label: "👩‍💻" + strings.Repeat("h", 50)})
	renderTestInlays(e, 30, 8, false)
	e.scrollX, e.pendingReveal = 2, false
	b = renderTestInlays(e, 30, 8, false)
	requireInlayGlyph(t, b, x, 0, " ", 1) // No trailing half of the ZWJ glyph.
	requireInlayGlyph(t, b, x+1, 0, "h", 1)
	e.scrollX = 0
	// Put a whole wide label one cell before the right clip edge.
	e.ClearInlayHints()
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 1, Position: types.LSPInlayPosition{Character: 1}, Label: strings.Repeat("h", 30-x-3) + "中"})
	b = renderTestInlays(e, 30, 8, false)
	requireInlayGlyph(t, b, 28, 0, " ", 1)
	e.InvalidateWidthProfile()
	b = renderTestInlays(e, 30, 8, false)
	requireInlayGlyph(t, b, x+1, 0, "Z", 1)
	if _, ok := e.InlayHintAt(x+1, 0); ok {
		t.Fatal("width-profile invalidation retained old label geometry")
	}
	installTestInlays(t, e, types.LSPInlayHint{Label: "hint"})
	e.Close()
	if e.inlays != nil {
		t.Fatal("close retained inlay projection")
	}
}

func TestEditorInlayDenseWarmRenderAndLookupRetainProjection(t *testing.T) {
	e := newInlayTestEditor(t, strings.Repeat("short source\n", 20000))
	installTestInlays(t, e, types.LSPInlayHint{SourceByte: 6, Position: types.LSPInlayPosition{Character: 6}, Label: ":type "})
	b := renderTestInlays(e, 80, 16, false)
	requireInlayGlyph(t, b, e.gutterWidth()+6, 0, ":", 1)
	projection, line := e.inlays, e.inlays.lines[0]
	widthMeasures, revision := e.hWidthMeasures, e.Revision()
	sourceCache, widthCache := &e.buf.cachedBytes[0], &e.hLineWidths[0]
	exports := 0
	e.SetOnContentChanged(func(string, string) { exports++ })
	s := surface.NewSurface("warm-inlay", 80, 16, cell.TransparentBlank())
	for frame := 0; frame < 20; frame++ {
		e.Render(s, ui.PaintContext{Frame: frame})
		if index, ok := e.InlayHintAt(e.gutterWidth()+8, 0); !ok || index != 0 {
			t.Fatal("warm label lookup lost its identity")
		}
	}
	if e.inlays != projection || e.inlays.lines[0] != line || e.hWidthMeasures != widthMeasures ||
		e.Revision() != revision || &e.buf.cachedBytes[0] != sourceCache || &e.hLineWidths[0] != widthCache || exports != 0 {
		t.Fatalf("warm frames rebuilt projection/document width/cache or exported source: width measures %d -> %d, exports %d", widthMeasures, e.hWidthMeasures, exports)
	}
}

func BenchmarkEditorInlayWarmRender(b *testing.B) {
	for _, lines := range []int{20, 20000} {
		b.Run(fmt.Sprintf("lines-%d", lines), func(b *testing.B) {
			e := newInlayTestEditor(b, strings.Repeat("short source\n", lines))
			installTestInlays(b, e, types.LSPInlayHint{SourceByte: 6, Position: types.LSPInlayPosition{Character: 6}, Label: ":type "})
			s := surface.NewSurface("warm-inlay-bench", 80, 16, cell.TransparentBlank())
			e.Render(s, ui.PaintContext{})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e.Render(s, ui.PaintContext{})
				e.InlayHintAt(e.gutterWidth()+8, 0)
			}
		})
	}
}
