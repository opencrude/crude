package editor

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
)

func TestGhostProjectionPaintsOnTheWrappedContinuation(t *testing.T) {
	const source = "abcdefghijklmno\nend"
	e := newInlayTestEditor(t, source)
	e.SetPresentation(wrapSettings(4))
	e.SetCursorPosition(0, 15)
	width := e.GutterWidth() + 11
	before := paintWrappedInlays(t, e, width, 10, false)
	requireGhostProjectionRows(t, e, before, "abcdefghi↩", "jklmno", "end")
	offerGhostProjection(t, e, Suggestion{Start: 15, End: 15, Text: "XYZ"})
	preview := paintWrappedInlays(t, e, width, 10, false)
	requireGhostProjectionRows(t, e, preview, "abcdefghi↩", "jklmnoXYZ", "end")
	if got := preview.Get(e.GutterWidth()+6, 1); got.FG != e.tokens.Color(ui.ColorMuted) {
		t.Errorf("wrapped insertion did not paint as faded text: %+v", got)
	}
	if e.Value() != source || e.Modified() {
		t.Fatal("painting a wrapped insertion changed source")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "abcdefghijklmnoXYZ\nend" || e.cursor != (Position{Line: 0, Col: 18}) {
		t.Fatalf("Tab left text=%q caret=%+v after wrapped preview", e.Value(), e.cursor)
	}
}

func TestGhostProjectionCannotAcceptAnEditHiddenInsideAFold(t *testing.T) {
	newFold := func() *Editor {
		e := loadEditorText(t, "fold.go", foldFixture)
		e.SetStatusChrome(false)
		e.SetCursorPosition(1, 0)
		e.HandleEvent(ActionMsg{ID: "view.fold"})
		b := paintWrappedInlays(t, e, 80, 14, false)
		if ghostProjectionHas(e, b, "call()") || !ghostProjectionHas(e, b, "… 3 lines") {
			t.Fatal("setup did not collapse the region containing call()")
		}
		return e
	}
	e, baseline := newFold(), newFold()
	start := strings.Index(foldFixture, "call")
	before := e.Value()
	// A producer may refuse the hidden range. If it offers a preview,
	// it must reveal that preview before Tab can edit the hidden source.
	_ = e.SetSuggestion("aeron", e.Revision(), Suggestion{Start: start, End: start + len("call"), Text: "corrected"})
	preview := paintWrappedInlays(t, e, 80, 14, false)
	if e.Value() != before || e.Modified() {
		t.Fatal("preview changed the folded source before Tab")
	}
	if ghostProjectionHas(e, preview, "corrected") {
		if ghostProjectionHas(e, preview, "… 3 lines") || !ghostProjectionHas(e, preview, "callcorrected()") {
			t.Fatal("preview painted over the still-collapsed fold instead of showing its source row")
		}
		e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
		if want := strings.Replace(foldFixture, "call", "corrected", 1); e.Value() != want {
			t.Fatalf("Tab on the revealed edit = %q, want %q", e.Value(), want)
		}
		return
	}
	baseline.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != baseline.Value() {
		t.Fatalf("Tab applied an edit that was never painted inside the fold: got %q; ordinary Tab=%q", e.Value(), baseline.Value())
	}
}

func TestGhostProjectionPreservesBidiOrderAndLogicalInsertion(t *testing.T) {
	const base, completion = "שלום", " עולם"
	e := newInlayTestEditor(t, base+"\n")
	e.SetTextDirection(bidi.Auto)
	e.SetCursorPosition(0, utf8.RuneCountInString(base))
	plain := renderTestInlays(e, 50, 8, false)
	if got := strings.TrimSpace(textRow(e, plain, 0)); got != "םולש" {
		t.Fatalf("setup did not paint a Hebrew line in visual order: %q", got)
	}

	offerGhostProjection(t, e, Suggestion{Start: len(base), End: len(base), Text: completion})
	preview := renderTestInlays(e, 50, 8, false)
	full := newInlayTestEditor(t, base+completion+"\n")
	full.SetTextDirection(bidi.Auto)
	ordinary := renderTestInlays(full, 50, 8, false)
	if got, want := textRow(e, preview, 0), textRow(full, ordinary, 0); got != want || strings.TrimSpace(got) != "םלוע םולש" {
		t.Errorf("right-to-left preview differs from painting the same logical text: got %q, want %q", got, want)
	}
	if e.Value() != base+"\n" || e.Modified() {
		t.Fatal("right-to-left preview changed source")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if want := base + completion + "\n"; e.Value() != want {
		t.Fatalf("Tab inserted visual rather than logical text: got %q want %q", e.Value(), want)
	}
}

func TestGhostProjectionClickElsewhereDismissesWithoutEditing(t *testing.T) {
	const source = "first\nsecond"
	e := newInlayTestEditor(t, source)
	e.SetCursorPosition(0, 5)
	offerGhostProjection(t, e, Suggestion{Start: 5, End: 5, Text: "Ghost"})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 40, 8, false), "firstGhost", "second")
	x := e.GutterWidth() + 2
	e.HandleEvent(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x, Y: 1, Button: tea.MouseLeft})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 40, 8, false), "first", "second")
	if _, shown := e.GhostSuggestion(); shown || e.Value() != source || e.Modified() || e.cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("click left suggestion=%t, modified=%t, text=%q, caret=%+v", shown, e.Modified(), e.Value(), e.cursor)
	}
}

func TestGhostProjectionScrollKeepsTheSameSourceAnchor(t *testing.T) {
	source := "top\n" + strings.Repeat("other rows\n", 30)
	e := newInlayTestEditor(t, source)
	e.SetCursorPosition(0, 3)
	offerGhostProjection(t, e, Suggestion{Start: 3, End: 3, Text: "Preview"})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 40, 7, false), "topPreview", "other rows")
	x := e.GutterWidth() + 1
	for range 4 {
		e.HandleEvent(tea.MouseWheelMsg{X: x, Y: 1, Button: tea.MouseWheelDown})
	}
	scrolled := renderTestInlays(e, 40, 7, false)
	requireGhostProjectionRows(t, e, scrolled, "other rows", "other rows")
	if ghostProjectionHas(e, scrolled, "Preview") || e.CursorLine() != 0 {
		t.Fatal("scroll painted the suggestion on a different line or moved the caret")
	}
	for range 4 {
		e.HandleEvent(tea.MouseWheelMsg{X: x, Y: 1, Button: tea.MouseWheelUp})
	}
	requireGhostProjectionRows(t, e, renderTestInlays(e, 40, 7, false), "topPreview", "other rows")
	if e.Value() != source || e.Modified() || e.cursor != (Position{Line: 0, Col: 3}) {
		t.Fatalf("scroll changed the source or caret: modified=%t caret=%+v", e.Modified(), e.cursor)
	}
}

func TestGhostProjectionOneUndoRestoresTextAndCaret(t *testing.T) {
	for _, tc := range []struct {
		name, source, shown, accepted string
		caret                         Position
		offer                         Suggestion
	}{
		{"multiline insertion", "call()\nnext()", "call(alpha,)", "call(alpha,\n    beta)\nnext()",
			Position{Line: 0, Col: 5}, Suggestion{Start: 5, End: 5, Text: "alpha,\n    beta"}},
		{"replacement", "call(old)\nnext()", "call(oldnew)", "call(new)\nnext()",
			Position{Line: 0, Col: 2}, Suggestion{Start: 5, End: 8, Text: "new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := loadEditorText(t, "edit.txt", tc.source)
			e.SetStatusChrome(false)
			e.SetCursorPosition(tc.caret.Line, tc.caret.Col)
			offerGhostProjection(t, e, tc.offer)
			requireGhostProjectionRows(t, e, renderTestInlays(e, 50, 8, false), tc.shown)
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
			if e.Value() != tc.accepted || !e.Modified() {
				t.Fatalf("Tab left %q, want %q", e.Value(), tc.accepted)
			}
			e.HandleEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			requireGhostProjectionRows(t, e, renderTestInlays(e, 50, 8, false), strings.Split(tc.source, "\n")...)
			if e.Value() != tc.source || e.cursor != tc.caret || e.Modified() {
				t.Fatalf("Ctrl+Z left text=%q, caret=%+v, modified=%t; want %q at %+v", e.Value(), e.cursor, e.Modified(), tc.source, tc.caret)
			}
		})
	}
}

func TestGhostProjectionDoesNotOfferASuggestionWithTwoCarets(t *testing.T) {
	const initial = "ab\ncd"
	setup := func() *Editor {
		e := newInlayTestEditor(t, initial)
		renderTestInlays(e, 40, 8, false)
		e.SetCursorPosition(0, 1)
		e.HandleEvent(ActionMsg{ID: "select.caretBelow"})
		if e.CaretCount() != 2 {
			t.Fatal("Add Caret Below did not create two carets")
		}
		return e
	}
	e, baseline := setup(), setup()
	_ = e.SetSuggestion("aeron", e.Revision(), Suggestion{Start: 1, End: 1, Text: "Ghost"})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 40, 8, false), "ab", "cd")
	if _, shown := e.GhostSuggestion(); shown {
		t.Fatal("a suggestion remained eligible with two carets")
	}
	baseline.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != baseline.Value() || !e.Modified() {
		t.Fatalf("Tab with two carets = %q, ordinary Tab=%q", e.Value(), baseline.Value())
	}
	for _, line := range strings.Split(e.Value(), "\n") {
		if !strings.ContainsAny(line, " \t") {
			t.Fatalf("ordinary Tab edited only one of the two carets: %q", e.Value())
		}
	}
}

func TestGhostProjectionMarksReplacedTextAndPaintsTheNewTextBesideIt(t *testing.T) {
	const initial = "call(old)\nnext()"
	e := newInlayTestEditor(t, initial)
	// Move the caret with the native keys so the test does not also
	// install a navigation landing highlight over the first old cell.
	renderTestInlays(e, 46, 8, false)
	for range 5 {
		e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	offerGhostProjection(t, e, Suggestion{Start: 5, End: 8, Text: "fresh"})
	b := renderTestInlays(e, 46, 8, false)
	requireGhostProjectionRows(t, e, b, "call(oldfresh)", "next()")
	x := e.GutterWidth()
	for i := 0; i < len("call(oldfresh)"); i++ {
		got := b.Get(x+i, 0)
		if got.Attr.Has(cell.AttrStrikethrough) != (i >= 5 && i < 8) {
			t.Errorf("only replaced source must be struck: column %d, cell %+v", i, got)
		}
		if i >= 8 && i < 13 && got.FG != e.tokens.Color(ui.ColorMuted) {
			t.Errorf("replacement column %d is not faded: %+v", i, got)
		}
	}
	if e.Value() != initial || e.Modified() {
		t.Fatal("replacement preview edited the file")
	}
}

func TestGhostProjectionMarkerPaintsAtTheNextPlaceAndTabJumps(t *testing.T) {
	const source = "first()\nnext()\n"
	e := newInlayTestEditor(t, source)
	e.SetCursorPosition(0, 2)
	at := strings.Index(source, "next")
	offerGhostProjection(t, e, Suggestion{Start: at, End: at, Marker: "Next edit", JumpTo: at + len("next(")})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 46, 8, false), "first()", "Next editnext()")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	requireGhostProjectionRows(t, e, renderTestInlays(e, 46, 8, false), "first()", "next()")
	if e.cursor != (Position{Line: 1, Col: 5}) || e.Modified() || e.Value() != source {
		t.Fatalf("next-place Tab left caret=%+v modified=%t text=%q", e.cursor, e.Modified(), e.Value())
	}
}

func offerGhostProjection(t *testing.T, e *Editor, offer Suggestion) {
	t.Helper()
	if err := e.SetSuggestion("aeron", e.Revision(), offer); err != nil {
		t.Fatal(err)
	}
}

func requireGhostProjectionRows(t *testing.T, e *Editor, b *surface.CellBuffer, rows ...string) {
	t.Helper()
	for y, want := range rows {
		if got := strings.TrimRight(textRow(e, b, y), " "); got != want {
			t.Errorf("painted row %d = %q, want %q", y, got, want)
		}
	}
}

func ghostProjectionHas(e *Editor, b *surface.CellBuffer, text string) bool {
	for y := 0; y < b.Height(); y++ {
		if strings.Contains(textRow(e, b, y), text) {
			return true
		}
	}
	return false
}
