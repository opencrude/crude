package editor

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
)

func occurrenceRange(line, start, end int) types.LSPNavigationRange {
	return types.LSPNavigationRange{Start: types.LSPNavigationPosition{Line: line, Character: start},
		End: types.LSPNavigationPosition{Line: line, Character: end}}
}

// Occurrences arrive in UTF-16 and paint at visual columns after a tab and a
// wide character. They are passive: no selection, no caret move, no edit.
func TestOccurrencesPaintAtVisualColumnsWhileTheCaretIsOnOne(t *testing.T) {
	e, s := projectionEditor(t, "value := 1\n\tx(界, value)\n")
	s.AnalysisGeneration, s.AnalysisLifetime = "analysis", "open"
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	source, _ := e.CurrentInsightSource()
	tab := e.TabWidth()
	at := tab + 6 // "\t" "x(" "界" ", " then value
	e.SetCursorPosition(1, at+2)
	line, col := e.LSPCursorPosition()
	result := &types.LSPHighlights{Boundary: types.InsightBoundary{Source: source}, Line: line, Col: col,
		Answer: types.LSPHighlightsFound, Highlights: []types.LSPHighlight{
			{Range: occurrenceRange(0, 0, 5), Kind: types.LSPHighlightWrite}, {Range: occurrenceRange(1, 6, 11), Kind: types.LSPHighlightRead}}}
	stale := *result
	stale.Col++
	if err := e.SetOccurrences(&stale); err == nil {
		t.Fatal("an answer for another caret was installed")
	}
	if err := e.SetOccurrences(result); err != nil {
		t.Fatal(err)
	}
	surf := surface.NewSurface("occurrences", 60, 6, cell.TransparentBlank())
	paint := func() (write, read, plain cell.Color) {
		e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
		x, y := e.CursorScreenPosition(0, 0)
		x -= 2 // the caret is two columns into the second value
		buf := surf.Buffer()
		return buf.Get(x-at, y-1).BG, buf.Get(x, y).BG, buf.Get(x-at+tab, y).BG
	}
	write, read, plain := paint()
	if write != e.tokens.OccurrenceFill(true) || read != e.tokens.OccurrenceFill(false) || plain == read {
		t.Fatal("occurrences were not painted at their exact visual columns", write, read, plain)
	}
	if e.HasSelection() || e.CursorLine() != 1 || e.CursorCol() != at+2 || e.Modified() {
		t.Fatal("occurrences changed the selection, caret or text")
	}
	e.SetCursorPosition(1, tab)
	if _, read, _ = paint(); read == e.tokens.OccurrenceFill(false) || e.OccurrencesCoverCaret() {
		t.Fatal("occurrences stayed after the caret left the symbol")
	}
	e.SetCursorPosition(0, 5) // the end of an occurrence still names it
	if !e.OccurrencesCoverCaret() {
		t.Fatal("the caret at the end of the symbol lost its occurrences")
	}
	e.InsertRune('s')
	if e.OccurrencesCoverCaret() {
		t.Fatal("occurrences survived an edit to the source they describe")
	}
}

// Protocol ranges can overlap or span lines. A later short range must not hide
// an earlier enclosing occurrence on the caret's line.
func TestOccurrencesHandleNestedMultilineRanges(t *testing.T) {
	e, s := projectionEditor(t, "aaa\nBBB\nccc\nDDD\n")
	s.AnalysisGeneration, s.AnalysisLifetime = "analysis", "open"
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	source, _ := e.CurrentInsightSource()
	e.SetCursorPosition(1, 1)
	ranges := []types.LSPHighlight{
		{Range: types.LSPNavigationRange{Start: types.LSPNavigationPosition{Line: 0, Character: 0}, End: types.LSPNavigationPosition{Line: 3, Character: 3}}, Kind: types.LSPHighlightRead},
		{Range: occurrenceRange(0, 1, 2), Kind: types.LSPHighlightRead},
		{Range: occurrenceRange(2, 0, 1), Kind: types.LSPHighlightRead},
	}
	if err := e.SetOccurrences(&types.LSPHighlights{Boundary: types.InsightBoundary{Source: source}, Line: 1, Col: 1, Highlights: ranges}); err != nil {
		t.Fatal(err)
	}
	if !e.OccurrencesCoverCaret() {
		t.Fatal("nested ranges hid the occurrence covering the caret")
	}
	surf := surface.NewSurface("nested", 40, 6, cell.TransparentBlank())
	e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
	x, y := e.CursorScreenPosition(0, 0)
	if got := surf.Buffer().Get(x-1, y).BG; got != e.tokens.OccurrenceFill(false) {
		t.Fatal("the enclosing occurrence was not painted", got)
	}
}

// Unsorted wire ranges still use the editor's existing tab, grapheme and UTF-16
// projection. Invalid surrogate boundaries refuse the whole replacement.
func TestOccurrencesPreserveGraphemeAndUTF16Boundaries(t *testing.T) {
	e, s := projectionEditor(t, "A\t🙂e\u0301Z\nA\t🙂e\u0301Z\n")
	s.AnalysisGeneration, s.AnalysisLifetime = "analysis", "open"
	if err := e.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	source, _ := e.CurrentInsightSource()
	e.SetCursorPosition(0, 4)
	line, col := e.LSPCursorPosition()
	result := &types.LSPHighlights{Boundary: types.InsightBoundary{Source: source}, Line: line, Col: col,
		Highlights: []types.LSPHighlight{{Range: occurrenceRange(1, 2, 4), Kind: types.LSPHighlightRead}, {Range: occurrenceRange(0, 2, 4), Kind: types.LSPHighlightRead}}}
	if err := e.SetOccurrences(result); err != nil {
		t.Fatal(err)
	}
	if !e.OccurrencesCoverCaret() {
		t.Fatal("the caret lost the highlighted wide grapheme")
	}
	spans := e.lineOccurrences(1, true)
	if len(spans) != 1 || spans[0].from != 4 || spans[0].to != 6 {
		t.Fatal("wide grapheme after a tab used the wrong columns", spans)
	}
	bad := *result
	bad.Highlights = append([]types.LSPHighlight(nil), result.Highlights...)
	bad.Highlights[0].Range.End.Character = 3 // inside the surrogate pair
	if err := e.SetOccurrences(&bad); err == nil {
		t.Fatal("a split surrogate was accepted")
	}
	if !e.OccurrencesCoverCaret() {
		t.Fatal("an invalid answer replaced the previous marks")
	}
	// End inside the combining cluster floors to the existing grapheme start.
	result.Highlights[0].Range = occurrenceRange(1, 4, 5)
	if err := e.SetOccurrences(result); err != nil {
		t.Fatal(err)
	}
	if spans := e.lineOccurrences(1, true); len(spans) != 0 {
		t.Fatal("a partial grapheme produced a false mark", spans)
	}
}
