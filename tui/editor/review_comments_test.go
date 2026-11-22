package editor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/text/anchor"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/workspace/document"
)

func reviewTestComment(id, source, quote, body string) ReviewComment {
	start := strings.Index(source, quote)
	return ReviewComment{ID: id, Anchor: anchor.Capture(source, start, start+len(quote)), Body: body}
}

func reviewTestLine(b *surface.CellBuffer, y int) string {
	return strings.TrimSpace(surface.ReadLogical(b.Row(y)))
}

func requireReviewLine(t *testing.T, b *surface.CellBuffer, y int, want string) {
	t.Helper()
	if got := reviewTestLine(b, y); got != want {
		t.Fatalf("row %d: %q, want %q", y, got, want)
	}
}

func editorShowsReviewText(b *surface.CellBuffer, text string) bool {
	for y := 0; y < b.Height(); y++ {
		if strings.Contains(reviewTestLine(b, y), text) {
			return true
		}
	}
	return false
}

func TestReviewCardsPlaceStackAndActivateWithoutMovingCaret(t *testing.T) {
	const source = "abcdefghijklmnop\nend"
	for _, wrap := range []bool{false, true} {
		t.Run(fmt.Sprint(wrap), func(t *testing.T) {
			e := newInlayTestEditor(t, source)
			e.SetPresentation(setting.Editor{TabSize: 4, LineNumbers: true, WordWrap: wrap})
			first := reviewTestComment("first", source, "bcd", "First")
			second := reviewTestComment("second", source, "nop", "Second")
			second.Muted = true
			e.SetReviewComments([]ReviewComment{second, first}) // not anchor order
			x := e.GutterWidth()
			width := x + 11
			// Before cold wrap metrics arrive the same index has one row per
			// source line, followed by that line's cards.
			b := renderTestInlays(e, width, 10, false)
			requireReviewLine(t, b, 1, "▎ First")
			requireReviewLine(t, b, 2, "▎ Second")
			cardY := 1
			if wrap {
				b = paintWrappedInlays(t, e, width, 10, false)
				cardY = 2
			}
			requireReviewLine(t, b, cardY, "▎ First")
			requireReviewLine(t, b, cardY+1, "▎ Second")
			if b.Get(x, cardY+2).Char != 'e' || e.ScrollModel().Content() != cardY+3 {
				t.Fatal("source, cards, and scroll model disagree")
			}
			for at := 0; at < x; at++ {
				if b.Get(at, cardY).Char != ' ' {
					t.Fatalf("card gutter contains ink at x=%d", at)
				}
			}
			if !b.Get(x, cardY).FG.Equal(e.tokens.Color(ui.ColorAccent)) ||
				!b.Get(x, cardY+1).FG.Equal(e.tokens.Color(ui.ColorMuted)) ||
				!b.Get(x+8, cardY).BG.Equal(e.tokens.Color(ui.ColorSubPaneBg)) {
				t.Fatal("card ink or background ignored the display state")
			}
			e.SetCursorPosition(0, 2)
			before := e.cursor
			cmd := e.HandleEvent(tea.MouseClickMsg{X: x + 2, Y: cardY + 1, Button: tea.MouseLeft})
			if cmd == nil {
				t.Fatal("click did not activate the painted card")
			}
			if msg, ok := cmd().(ReviewCommentActivatedMsg); !ok || msg.Editor != e || msg.ID != "second" {
				t.Fatalf("wrong activation: %+v", msg)
			}
			e.HandleEvent(tea.MouseReleaseMsg{X: x + 2, Y: cardY + 1, Button: tea.MouseLeft})
			if e.cursor != before || e.IsMouseDragging() || e.Value() != source {
				t.Fatal("activating a card changed source or capture")
			}
			if _, ok := e.PositionAt(x+2, cardY+1); ok {
				t.Fatal("card was reported to external pointer clients as source")
			}
			e.SetCursorPosition(0, 13)
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyDown})
			if e.CursorLine() != 1 {
				t.Fatalf("Down landed inside a card at %v", e.cursor)
			}
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyUp})
			if e.CursorLine() != 0 {
				t.Fatalf("Up landed inside a card at %v", e.cursor)
			}
			e.SetCursorPosition(0, 0)
			e.ScrollToCursor()
			e.HandleEvent(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
			e.HandleEvent(tea.MouseMotionMsg{X: x + 3, Y: cardY})
			e.HandleEvent(tea.MouseReleaseMsg{X: x + 3, Y: cardY, Button: tea.MouseLeft})
			if got := e.SelectedText(); got != "abc" {
				t.Fatalf("card-crossing drag selected %q, want the real source", got)
			}
			e.SetReviewComments(nil)
			b = renderTestInlays(e, width, 10, false)
			if e.ScrollModel().Content() != cardY+1 || editorShowsReviewText(b, "▎") {
				t.Fatal("clearing comments left card rows or paint")
			}
		})
	}
}

func TestReviewCardsFollowGhostRowsAndUseLastSourceLine(t *testing.T) {
	const source = "head\ncall()\nnext()"
	e := newInlayTestEditor(t, source)
	e.SetReviewComments([]ReviewComment{reviewTestComment("multi", source, "head\ncall()\n", "Review")})
	e.SetCursorPosition(1, 5)
	if err := e.SetSuggestion("errand", e.Revision(), Suggestion{Start: 10, End: 10, Text: "first\nsecond\nthird"}); err != nil {
		t.Fatal(err)
	}
	b := renderTestInlays(e, 80, 10, false)
	requireReviewLine(t, b, 2, "second")
	requireReviewLine(t, b, 3, "third")
	requireReviewLine(t, b, 4, "▎ Review")
	if e.ScrollModel().Content() != 6 || e.Value() != source {
		t.Fatal("ghost and comment rows changed source or scroll extent")
	}
}

func TestReviewCardSourceEditsFuzzyMissingUndoRedo(t *testing.T) {
	const source = "heading\nShip the draft to reviewers.\nafter"
	e := loadEditorText(t, "plan.md", source)
	e.SetStatusChrome(false)
	c := reviewTestComment("A", source, "Ship the draft to reviewers.", "Check recipients")
	c.Status = "Sent"
	e.SetReviewComments([]ReviewComment{c})
	b := renderTestInlays(e, 85, 12, false)
	requireReviewLine(t, b, 2, "▎ Check recipients  Sent")
	e.SetCursorPosition(0, 0)
	e.HandleEvent(tea.PasteMsg{Content: "Prelude\n"})
	b = renderTestInlays(e, 85, 12, false)
	requireReviewLine(t, b, 3, "▎ Check recipients  Sent")
	if start, end, found := e.ReviewCommentRange("A"); !found || e.Value()[start:end] != "Ship the draft to reviewers." {
		t.Fatalf("disjoint edit lost review span: %d:%d, %v", start, end, found)
	}
	e.SetSelection(2, 9, 2, 10)
	e.HandleEvent(tea.KeyPressMsg{Code: 'D', Text: "D"})
	b = renderTestInlays(e, 85, 12, false)
	requireReviewLine(t, b, 3, "▎ Check recipients  Sent · The commented text changed.")
	if start, end, found := e.ReviewCommentRange("A"); !found || e.Value()[start:end] != "Ship the Draft to reviewers." {
		t.Fatalf("fuzzy edit lost review span: %d:%d, %v", start, end, found)
	}
	e.SetSelection(2, 0, 2, 28)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
	b = renderTestInlays(e, 85, 12, false)
	if !editorShowsReviewText(b, "Sent · The commented text changed.") {
		t.Fatal("deletion hid a comment instead of showing its changed status")
	}
	if start, _, found := e.ReviewCommentRange("A"); found || start != len("heading\n") {
		t.Fatalf("missing text did not use the position hint: offset=%d found=%v", start, found)
	}
	if !e.RevealReviewComment("A") || e.CursorLine() != 1 {
		t.Fatal("the missing quote's card could not be found near its original position")
	}
	e.Undo()
	b = renderTestInlays(e, 85, 12, false)
	requireReviewLine(t, b, 3, "▎ Check recipients  Sent · The commented text changed.")
	if _, _, found := e.ReviewCommentRange("A"); !found {
		t.Fatal("Undo did not restore the fuzzy range")
	}
	e.Redo()
	if _, _, found := e.ReviewCommentRange("A"); found {
		t.Fatal("Redo kept the restored range after removing the quote again")
	}
}

func TestReviewRangeLayerBelowFindAndSelection(t *testing.T) {
	const source = "\t界abc xyz"
	e := newInlayTestEditor(t, source)
	e.SetReviewComments([]ReviewComment{reviewTestComment("one", source, "界abc", "Comment")})
	b := renderTestInlays(e, 60, 8, false)
	x := e.GutterWidth()
	rangeBG := e.cellColor(e.th.Colors.ReviewCommentRange)
	for col := 4; col < 9; col++ {
		if !b.Get(x+col, 0).BG.Equal(rangeBG) {
			t.Fatalf("comment highlight missed painted source column %d", col)
		}
	}
	if b.Get(x+3, 0).BG.Equal(rangeBG) || b.Get(x+9, 0).BG.Equal(rangeBG) {
		t.Fatal("review range highlighted outside its half-open span")
	}
	e.SetQuery("abc")
	e.SetSelection(0, 7, 0, 8)
	b = renderTestInlays(e, 60, 8, false)
	_, selectBG := e.tokens.TextSelectionColors(e.tokens.Color(ui.ColorFg))
	for col, want := range map[int]cell.Color{4: rangeBG, 6: e.searchHighlightBg(true), 7: selectBG, 8: e.searchHighlightBg(true)} {
		if !b.Get(x+col, 0).BG.Equal(want) {
			t.Fatalf("wrong highlight precedence at column %d", col)
		}
	}
}

func TestReviewCardsFoldPageAndReveal(t *testing.T) {
	const source = "first\nroot\n    target item\nother\nmore\nfinish\n"
	e := newInlayTestEditor(t, source)
	e.SetReviewComments([]ReviewComment{
		reviewTestComment("a", source, "first", "First"),
		reviewTestComment("b", source, "target", "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6"),
		reviewTestComment("c", source, "target", "Also here"),
	})
	e.SetCursorPosition(1, 0)
	e.HandleEvent(ActionMsg{ID: "view.fold"})
	b := renderTestInlays(e, 45, 7, false)
	if editorShowsReviewText(b, "Line 1") || e.ScrollModel().Content() != 7 {
		t.Fatal("folded line kept its cards")
	}
	for _, want := range []string{"b", "c", "a"} {
		if id, ok := e.NextReviewComment(true); !ok || id != want {
			t.Fatalf("next returned %q, want %q", id, want)
		}
		b = renderTestInlays(e, 45, 7, false)
		wantText := map[string]string{"a": "First", "b": "Line 1", "c": "Also here"}[want]
		if !editorShowsReviewText(b, wantText) {
			t.Fatalf("navigation did not reveal %q", wantText)
		}
		if id, ok := e.ReviewCommentAtCaret(); !ok || id != want {
			t.Fatalf("caret returned comment %q, want %q", id, want)
		}
	}
	if id, ok := e.NextReviewComment(false); !ok || id != "c" {
		t.Fatalf("previous did not wrap: %q", id)
	}
	e.SetCursorPosition(0, 0)
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if e.CursorLine() != 3 {
		t.Fatalf("PageDown landed at %v, want first source line below cards", e.cursor)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if e.CursorLine() != 2 {
		t.Fatalf("PageUp landed at %v, want source line above cards", e.cursor)
	}
}

func TestReviewRevealStaysOnCardAfterColdWrapCompletes(t *testing.T) {
	const source = "intro\nabcdefghijklmnopqrstuvwx\nlast"
	e := newInlayTestEditor(t, source)
	e.SetPresentation(wrapSettings(4))
	e.SetReviewComments([]ReviewComment{reviewTestComment("one", source, "abcdefghijklmnopqrstuvwx", "Find me")})
	if !e.RevealReviewComment("one") {
		t.Fatal("could not reveal the comment before the pane was sized")
	}
	w := e.GutterWidth() + 7
	b := renderTestInlays(e, w, 4, false)
	if !editorShowsReviewText(b, "▎ Find") {
		t.Fatal("reveal missed the card while source wrap was preparing")
	}
	settleRows(t, e)
	b = renderTestInlays(e, w, 4, false)
	if !editorShowsReviewText(b, "▎ Find") || !editorShowsReviewText(b, "▎ me") || e.CursorLine() != 1 || e.CursorCol() != 0 {
		t.Fatal("applying source wrap lost the requested card or moved the caret")
	}
}

func TestReviewCardNarrowWrapAndEightRowLimit(t *testing.T) {
	const source = "source\nlast"
	e := newInlayTestEditor(t, source)
	c := reviewTestComment("wide", source, "source", "A界 é\n"+strings.Repeat("line2\n", 10)+"do not show")
	e.SetReviewComments([]ReviewComment{c})
	x := e.GutterWidth()
	b := renderTestInlays(e, x+9, 12, false)
	requireInlayGlyph(t, b, x+3, 1, "界", 2)
	requireInlayGlyph(t, b, x+6, 1, "é", 1)
	requireReviewLine(t, b, 8, "▎ …")
	if e.ScrollModel().Content() != 10 || b.Get(x, 9).Char != 'l' || editorShowsReviewText(b, "do not show") {
		t.Fatal("long comment escaped its eight-row limit")
	}
	c.Status = "Sent"
	e.SetReviewComments([]ReviewComment{c})
	b = renderTestInlays(e, x+25, 12, false)
	requireReviewLine(t, b, 8, "▎ …  Sent")
	c.Body, c.Status = "longer", "Sent"
	e.SetReviewComments([]ReviewComment{c})
	b = renderTestInlays(e, x+9, 12, false)
	requireReviewLine(t, b, 2, "▎ Sent")
	if !b.Get(x+2, 2).FG.Equal(e.tokens.Color(ui.ColorMuted)) {
		t.Fatal("status used body ink")
	}
}

func TestReviewCardsFollowPublicationAndReset(t *testing.T) {
	const source = "start\nimportant plan line\nfinish"
	e, s := projectionEditor(t, source)
	e.SetStatusChrome(false)
	e.SetReviewComments([]ReviewComment{reviewTestComment("one", source, "important plan line", "Keep this")})
	requireReviewLine(t, renderTestInlays(e, 60, 10, false), 2, "▎ Keep this")
	state := s
	state.Revision, state.Text, state.Saved = 2, nil, nil
	peer := document.Change{DocumentID: s.ID, BaseRevision: 1, Owner: document.Owner{ClientID: "peer", Epoch: 1}, RequestID: "peer", Edits: []document.Edit{{Start: 0, End: 0, Text: "intro\n"}}}
	prepared, err := e.PrepareDocumentUpdate(projectionOwner, 1, []document.Change{peer}, state, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CommitDocumentUpdate(prepared); err != nil {
		t.Fatal(err)
	}
	e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp}) // peer edits retain the visible source anchor
	requireReviewLine(t, renderTestInlays(e, 60, 10, false), 3, "▎ Keep this")
	reset := s
	reset.Revision = 3
	reset.Text, reset.Saved = []byte("gone"), []byte("gone")
	state.Revision = 3
	prepared, err = e.PrepareDocumentUpdate(projectionOwner, 2, nil, state, &reset, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CommitDocumentUpdate(prepared); err != nil {
		t.Fatal(err)
	}
	e.HandleEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	requireReviewLine(t, renderTestInlays(e, 60, 10, false), 1, "▎ Keep this  The commented text changed.")
}

func TestReviewCommentsSkipDocumentsOverOneMiB(t *testing.T) {
	const text = "mark\n"
	line := strings.Repeat("x", 255) + "\n"
	source := text + strings.Repeat(line, 4096)
	e := newInlayTestEditor(t, source)
	e.SetReviewComments([]ReviewComment{reviewTestComment("large", source, "mark", "Never visible")})
	b := renderTestInlays(e, 55, 8, false)
	if _, _, found := e.ReviewCommentRange("large"); found || e.RevealReviewComment("large") ||
		e.ScrollModel().Content() != e.LineCount() || editorShowsReviewText(b, "▎") {
		t.Fatal("over-limit document exposed review cards")
	}
}

func TestReviewRangeDistinguishedOnBuiltInThemes(t *testing.T) {
	const source = "title text"
	for _, th := range theme.AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			e := newInlayTestEditor(t, source)
			e.SetTheme(th)
			e.SetReviewComments([]ReviewComment{reviewTestComment("a", source, "text", "Comment")})
			b := renderTestInlays(e, 45, 8, false)
			x := e.GutterWidth()
			ink, mark := b.Get(x+6, 0).FG, b.Get(x+6, 0).BG
			if mark.Equal(b.Get(x+5, 0).BG) || mark.Equal(b.Get(x, 1).BG) || cell.ContrastRatio(ink, mark) < 4.5 {
				t.Fatal("review range and surrounding source are not distinct and readable")
			}
		})
	}
}

func TestReviewCommentsReuseSourceMetricsOnRefreshAndResize(t *testing.T) {
	const source = "top\n\tanchor line\n" + "last\n"
	e := newInlayTestEditor(t, source)
	e.SetPresentation(wrapSettings(4))
	c := reviewTestComment("one", source, "anchor", "One")
	e.SetReviewComments([]ReviewComment{c})
	paintWrappedInlays(t, e, 80, 10, false)
	p, rows, measures := e.review.resolved, e.review.layout, e.rows.measures
	e.SetReviewComments([]ReviewComment{c})
	renderTestInlays(e, 80, 10, false)
	if p != e.review.resolved || rows != e.review.layout {
		t.Fatal("equal detached comments or paint rebuilt the review projection")
	}
	c.Body = "Another\nline"
	e.SetReviewComments([]ReviewComment{c})
	b := renderTestInlays(e, 60, 10, false)
	requireReviewLine(t, b, 3, "▎ line")
	if e.rows.measures != measures {
		t.Fatal("comment update remeasured unchanged source")
	}
}

func TestSelectionOffsetsUsePrimaryTabAndUTF8Source(t *testing.T) {
	const source = "a\t界é!\r\nnext"
	e := newInlayTestEditor(t, source)
	e.SetCursorPosition(0, 4)
	if a, b := e.SelectionOffsets(); a != 2 || b != 2 {
		t.Fatalf("after a tab, caret offsets are %d:%d, want 2:2", a, b)
	}
	if err := e.setSelections([]selectionSpan{
		{SourceSelection: SourceSelection{Anchor: 8, Cursor: 1}, primary: true},
		{SourceSelection: SourceSelection{Anchor: 12, Cursor: 14}},
	}); err != nil {
		t.Fatal(err)
	}
	a, b := e.SelectionOffsets()
	if a != 1 || b != 8 || e.Value()[a:b] != "\t界é" {
		t.Fatalf("primary byte range %d:%d did not capture tab and Unicode text", a, b)
	}
}

func BenchmarkReviewCommentsWarmPaint(b *testing.B) {
	for _, count := range []int{200, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			var src strings.Builder
			var comments []ReviewComment
			for i := 0; i < count; i++ {
				n := src.Len()
				line := fmt.Sprintf("Step %05d: use a canary to validate the change before deployment.\n", i)
				src.WriteString(line)
				if i%max(1, count/100) == 0 {
					comments = append(comments, ReviewComment{ID: fmt.Sprint(i), Anchor: conversation.TextAnchor{Start: n, End: n + 10, Exact: line[:10]}, Body: "Add a success check before declaring this complete."})
				}
			}
			e := newInlayTestEditor(b, src.String())
			e.SetPresentation(wrapSettings(4))
			e.SetReviewComments(comments)
			surf := surface.NewSurface("review-benchmark", 80, 24, cell.TransparentBlank())
			e.Render(surf, ui.PaintContext{})
			settleRows(b, e)
			e.SetCursorPosition(count/2, 0)
			e.ScrollToCursor()
			e.Render(surf, ui.PaintContext{})
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				e.Render(surf, ui.PaintContext{})
			}
		})
	}
}
