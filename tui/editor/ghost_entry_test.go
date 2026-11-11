package editor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
)

// Enter through the rendered editor and real keys. The interface assertion lets
// this oracle run against the pre-feature owner instead of failing to compile.
type ghostEntryAPI interface {
	SetGhostText(uint64, int, string) error
	GhostText() (int, string, bool)
	DismissGhostText() bool
}

func ghostEntry(t testing.TB, e *Editor, anchor int, text string) ghostEntryAPI {
	t.Helper()
	api, ok := any(e).(ghostEntryAPI)
	if !ok {
		t.Fatal("native editor has no display-only inline prediction lifetime")
	}
	if err := api.SetGhostText(e.Revision(), anchor, text); err != nil {
		t.Fatal(err)
	}
	return api
}

func requireGhostRest(t testing.TB, api ghostEntryAPI, anchor int, rest string) {
	t.Helper()
	gotAnchor, got, ok := api.GhostText()
	if ok != (rest != "") || got != rest || ok && gotAnchor != anchor {
		t.Fatalf("remaining ghost = (%d,%q,%v), want (%d,%q,%v)", gotAnchor, got, ok, anchor, rest, rest != "")
	}
}

func TestEditorGhostNativeTabIsLiteralAndDoesNotCommitUntilAccepted(t *testing.T) {
	for _, width := range []int{160, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			e := newInlayTestEditor(t, "consume()")
			e.SetCursorPosition(0, 8)
			renderTestInlays(e, width, 24, false)
			revision, disk := e.Revision(), e.DiskGeneration()
			api := ghostEntry(t, e, 8, "value")
			b := renderTestInlays(e, width, 24, false)
			for offset, r := range "value)" {
				requireInlayGlyph(t, b, e.gutterWidth()+8+offset, 0, string(r), 1)
			}
			if e.Value() != "consume()" || e.Modified() || e.Revision() != revision ||
				e.DiskGeneration() != disk || e.DocumentState().PendingEdits != 0 {
				t.Fatal("showing inline prediction modified source, canonical edits or savepoint")
			}
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
			if e.Value() != "consume(value)" || e.cursor != (Position{Line: 0, Col: 13}) ||
				!e.Modified() || e.DiskGeneration() != disk || e.DocumentState().PendingEdits != 1 {
				t.Fatal("native Tab did not insert the exact prediction as one normal source edit", e.Value(), e.DocumentState())
			}
			requireGhostRest(t, api, 0, "")
			change, err := e.CaptureDocumentEdits(document.Owner{ClientID: "ghost-shell", Epoch: 1}, "accept", "")
			if err != nil || len(change.Edits) != 1 || change.Edits[0].Start != 8 || change.Edits[0].End != 8 || change.Edits[0].Text != "value" {
				t.Fatalf("canonical edit capture lost exact normal insertion: %+v, %v", change, err)
			}
		})
	}
}

func TestEditorGhostUnicodeInlayPaintHitAndBeforeAffinityShareOneProjection(t *testing.T) {
	e := newInlayTestEditor(t, "a\t中😀Z")
	e.SetCursorPosition(0, 6)
	installTestInlays(t, e, types.LSPInlayHint{
		Index: 0, SourceByte: 5, Position: types.LSPInlayPosition{Character: 3}, Label: ":T", PaddingRight: true,
	})
	api := ghostEntry(t, e, 5, "e\u0301👩‍💻")
	e.cursorBlink.ResetAt(0)
	b := renderTestInlays(e, 40, 8, true)
	x := e.gutterWidth()
	for _, c := range []struct {
		col, width int
		text       string
	}{{4, 2, "中"}, {6, 1, ":"}, {7, 1, "T"}, {8, 1, " "}, {9, 1, "e\u0301"}, {10, 2, "👩‍💻"}, {12, 2, "😀"}, {14, 1, "Z"}} {
		requireInlayGlyph(t, b, x+c.col, 0, c.text, c.width)
	}
	if !b.Get(x+9, 0).Attr.Has(cell.AttrReverse) || b.Get(x+12, 0).Attr.Has(cell.AttrReverse) {
		t.Fatal("insertion caret must stay after inlay metadata but BEFORE candidate text")
	}
	if b.Get(x+10, 0).FG != e.tokens.Color(ui.ColorMuted) {
		t.Fatal("ghost did not use the shared dimmed theme token")
	}
	for col := 9; col < 12; col++ {
		pos, ok := e.PositionAt(x+col, 0)
		if !ok || pos != (Position{Line: 0, Col: 6}) {
			t.Fatalf("ghost cell %d became source instead of its byte anchor: %v", col, pos)
		}
		if _, hint := e.InlayHintAt(x+col, 0); hint {
			t.Fatal("prediction acquired an unrelated provider inlay identity")
		}
	}
	requireGhostRest(t, api, 5, "e\u0301👩‍💻")
	if command := e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEsc}); command != nil {
		t.Fatal("prediction dismissal escaped as an editor-close command")
	}
	requireGhostRest(t, api, 0, "")
	if !e.IsOpen() || e.Value() != "a\t中😀Z" || e.cursor != (Position{Line: 0, Col: 6}) {
		t.Fatal("first prediction Escape closed editor or changed source/caret")
	}
	b = renderTestInlays(e, 40, 8, false)
	requireInlayGlyph(t, b, x+9, 0, "😀", 2)
}

func TestEditorGhostNativeWordAcceptAndTypedThroughUseCompletedByteEdits(t *testing.T) {
	e := newInlayTestEditor(t, "x=")
	e.SetCursorPosition(0, 2)
	api := ghostEntry(t, e, 2, "  alpha_beta + 中e\u0301👩‍💻")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	if e.Value() != "x=  alpha_beta" {
		t.Fatal("word acceptance did not consume leading spaces and one identifier", e.Value())
	}
	requireGhostRest(t, api, 14, " + 中e\u0301👩‍💻")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	if e.Value() != "x=  alpha_beta +" {
		t.Fatal("punctuation acceptance crossed its word boundary", e.Value())
	}
	requireGhostRest(t, api, 16, " 中e\u0301👩‍💻")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "x=  alpha_beta + 中e\u0301👩‍💻" {
		t.Fatal("full acceptance changed candidate Unicode bytes", e.Value())
	}
	requireGhostRest(t, api, 0, "")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "x=  alpha_beta + 中e\u0301👩‍💻  " {
		t.Fatal("Tab without a candidate stopped indenting", e.Value())
	}

	typed := newInlayTestEditor(t, "result = ")
	typed.SetCursorPosition(0, 9)
	rest := ghostEntry(t, typed, 9, "alpha + beta")
	typed.HandleEvent(tea.KeyPressMsg{Code: 'a', Text: "a"})
	requireGhostRest(t, rest, 10, "lpha + beta")
	typed.HandleEvent(tea.KeyPressMsg{Code: 'l', Text: "lpha"})
	if typed.Value() != "result = alpha" {
		t.Fatal("native multi-rune typing lost accepted source", typed.Value())
	}
	requireGhostRest(t, rest, 14, " + beta")
	typed.HandleEvent(tea.KeyPressMsg{Code: 'z', Text: "z"})
	requireGhostRest(t, rest, 0, "")
	if typed.Value() != "result = alphaz" {
		t.Fatal("mismatching input was swallowed by prediction")
	}
}

func TestEditorGhostAutoPairsPeerAndEqualBytesCannotMasqueradeAsTypedThrough(t *testing.T) {
	for _, text := range []string{"(arg)", "()"} {
		e := newInlayTestEditor(t, "")
		api := ghostEntry(t, e, 0, text)
		e.HandleEvent(tea.KeyPressMsg{Code: '(', Text: "("})
		if e.Value() != "()" || e.cursor.Col != 1 {
			t.Fatal("prediction changed native pair behavior", e.Value(), e.cursor)
		}
		requireGhostRest(t, api, 0, "")
	}
	for _, mutation := range []string{"peer", "equal-bytes", "navigation", "selection", "width"} {
		t.Run(mutation, func(t *testing.T) {
			e := newInlayTestEditor(t, "base")
			e.SetCursorPosition(0, 4)
			api := ghostEntry(t, e, 4, " suffix")
			switch mutation {
			case "peer":
				if err := e.buf.applyOffsets([]document.Edit{{Start: 0, End: 0, Text: "peer "}}); err != nil {
					t.Fatal(err)
				}
			case "equal-bytes":
				e.buf.ReplaceAll("base")
			case "navigation":
				e.SetCursorPosition(0, 3)
				e.SetCursorPosition(0, 4)
			case "selection":
				e.SetSelection(0, 1, 0, 4)
				e.ClearSelection()
			case "width":
				e.InvalidateWidthProfile()
			}
			requireGhostRest(t, api, 0, "")
		})
	}
}

func TestEditorGhostUnsafeOrStaleOutputIsRefusedWithoutChangingTheEditor(t *testing.T) {
	for _, output := range []string{"", "\xff", "a\nb", "\tindent", "\r", "\x1b[31mred", "\u0085", "\u2028", "\u202eabc", strings.Repeat("x", 4097)} {
		e := newInlayTestEditor(t, "base")
		e.SetCursorPosition(0, 4)
		api, ok := any(e).(ghostEntryAPI)
		if !ok {
			t.Fatal("native editor has no display-only inline prediction lifetime")
		}
		revision, disk := e.Revision(), e.DiskGeneration()
		if err := api.SetGhostText(revision, 4, output); err == nil {
			t.Fatalf("unsafe/unsupported output accepted: %q", output)
		}
		if e.Value() != "base" || e.Modified() || e.Revision() != revision || e.DiskGeneration() != disk {
			t.Fatal("refused ghost changed source or savepoint")
		}
	}
	e := newInlayTestEditor(t, "base")
	e.SetCursorPosition(0, 4)
	api := ghostEntry(t, e, 4, " value")
	if api.SetGhostText(e.Revision()+1, 4, "late") == nil || api.SetGhostText(e.Revision(), 3, "wrong-caret") == nil {
		t.Fatal("candidate ignored exact buffer revision or caret")
	}
	e.Close()
	if api.DismissGhostText() {
		t.Fatal("close retained an offered candidate")
	}
}

func TestEditorGhostSearchAndDiagnosticDecorationsRemainSourceOnly(t *testing.T) {
	e := newInlayTestEditor(t, "foo bar")
	e.SetDiagnostics(testDiagnosticFacts(e, []LSPDiagnostic{{Line: 0, Col: 0, EndCol: 3, Severity: DiagError, Message: "source only"}}))
	if count := e.SetQuery("foo"); count != 1 {
		t.Fatal("source search fixture", count)
	}
	ghostEntry(t, e, 0, "foo ")
	b := renderTestInlays(e, 40, 8, false)
	x := e.gutterWidth()
	for col := 0; col < 4; col++ {
		c := b.Get(x+col, 0)
		if c.BG != e.cellColor(e.th.Colors.Bg) || c.Attr.Has(cell.AttrUndercurl) || c.Attr.Has(cell.AttrUnderline) {
			t.Fatal("prediction inherited a source diagnostic or search highlight", col, c)
		}
		e.HandleEvent(tea.MouseMotionMsg{X: x + col, Y: 0})
		if len(e.HoverState().Messages) != 0 {
			t.Fatal("prediction cell borrowed the following source diagnostic")
		}
	}
	for col := 4; col < 7; col++ {
		if !b.Get(x+col, 0).Attr.Has(cell.AttrUndercurl) {
			t.Fatal("source lost its diagnostic after inline insertion", col)
		}
	}
}

func TestEditorGhostExactLocalPrefixSurvivesItsOwnAcknowledgement(t *testing.T) {
	e := newInlayTestEditor(t, "x=")
	e.SetCursorPosition(0, 2)
	api := ghostEntry(t, e, 2, "alpha")
	e.HandleEvent(tea.PasteMsg{Content: "al"})
	if e.Value() != "x=al" {
		t.Fatal("native paste lost literal source")
	}
	requireGhostRest(t, api, 4, "pha")
	change, err := e.CaptureDocumentEdits(document.Owner{ClientID: "ghost-shell", Epoch: 1}, "typed-prefix", "")
	if err != nil {
		t.Fatal(err)
	}
	err = e.AcceptDocumentReceipt(document.Receipt{
		DocumentID: change.DocumentID, RequestID: change.RequestID,
		Before: 41, Revision: 42, Accepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireGhostRest(t, api, 4, "pha")
	e.HandleEvent(tea.BlurMsg{})
	requireGhostRest(t, api, 0, "")
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "x=al    " {
		t.Fatal("a retired offer revived on focus/Tab")
	}
}

func TestEditorGhostReplacementDetachesOldBufferListenerImmediately(t *testing.T) {
	e := newInlayTestEditor(t, "base")
	e.SetCursorPosition(0, 4)
	ghostEntry(t, e, 4, "candidate")
	old := e.buf
	next := e.DocumentSnapshot()
	next.ID = "replacement-document"
	next.Text, next.Saved = []byte("replacement"), []byte("replacement")
	if err := e.InstallDocument(next); err != nil {
		t.Fatal(err)
	}
	if len(old.interactionViews) != 0 || e.ghost != nil || e.ghostEdit != nil {
		t.Fatal("document replacement left candidate ownership on the departed buffer")
	}
	if e.Value() != "replacement" || e.Modified() {
		t.Fatal("retiring candidate ownership changed the replacement source/savepoint")
	}
}

func TestEditorGhostActualAdditionalMutationCancelsEvenMatchingKeystrokeText(t *testing.T) {
	e := newInlayTestEditor(t, "x=")
	e.SetCursorPosition(0, 2)
	api := ghostEntry(t, e, 2, "alpha")
	e.beginUndoGroup()
	e.cursor = e.insertRecorded(e.cursor, "a", e.cursor)
	e.spliceRecorded(0, 1, "y", e.cursor)
	e.endUndoGroup()
	if e.Value() != "y=a" {
		t.Fatal("actual transaction fixture lost source edits", e.Value())
	}
	requireGhostRest(t, api, 0, "")
}

func TestEditorGhostNativePointerSelectsOnlySourceAndWideCellsClipAtomically(t *testing.T) {
	e := newInlayTestEditor(t, "alpha beta")
	e.SetCursorPosition(0, 6)
	api := ghostEntry(t, e, 6, "candidate ")
	renderTestInlays(e, 50, 8, false)
	x := e.gutterWidth()
	e.HandleEvent(tea.MouseClickMsg{X: x + 9, Y: 0, Button: tea.MouseLeft})
	requireGhostRest(t, api, 0, "")
	if e.cursor != (Position{Line: 0, Col: 6}) || e.HasSelection() {
		t.Fatal("clicking prediction cells did not land at their insertion anchor")
	}
	e.HandleEvent(tea.MouseMotionMsg{X: x + 1, Y: 0, Button: tea.MouseLeft})
	e.HandleEvent(tea.MouseReleaseMsg{X: x + 1, Y: 0, Button: tea.MouseLeft})
	if e.Value() != "alpha beta" || e.SelectedText() != "lpha " {
		t.Fatal("dragging from a prediction selected ghost or the following source glyph", e.SelectedText())
	}

	wide := newInlayTestEditor(t, "aZ")
	wide.SetCursorPosition(0, 1)
	wideAPI := ghostEntry(t, wide, 1, "中"+strings.Repeat("h", 50))
	renderTestInlays(wide, 30, 8, false)
	wide.scrollX, wide.pendingReveal = 2, false
	b := renderTestInlays(wide, 30, 8, false)
	requireInlayGlyph(t, b, wide.gutterWidth(), 0, " ", 1)
	requireInlayGlyph(t, b, wide.gutterWidth()+1, 0, "h", 1)
	if wide.hScrollModel.MaxOffset() <= 2 {
		t.Fatal("horizontal geometry ignored the actual prediction display width")
	}
	wideAPI.DismissGhostText()
	b = renderTestInlays(wide, 30, 8, false)
	requireInlayGlyph(t, b, wide.gutterWidth()+1, 0, "Z", 1)
}

func TestEditorGhostDenseWarmFramesDoNotRebuildOrExportSource(t *testing.T) {
	e := newInlayTestEditor(t, strings.Repeat("short source\n", 20000))
	e.SetCursorPosition(0, 6)
	ghostEntry(t, e, 6, "candidate ")
	renderTestInlays(e, 80, 16, false)
	offer, projection := e.ghost, e.ghost.line
	measures, revision := e.hWidthMeasures, e.Revision()
	sourceCache, widthCache := &e.buf.cachedBytes[0], &e.hLineWidths[0]
	exports := 0
	e.SetOnContentChanged(func(string, string) { exports++ })
	s := surface.NewSurface("warm-ghost", 80, 16, cell.TransparentBlank())
	for frame := 0; frame < 30; frame++ {
		e.Render(s, ui.PaintContext{Frame: frame})
		pos, ok := e.PositionAt(e.gutterWidth()+8, 0)
		if !ok || pos != (Position{Line: 0, Col: 6}) {
			t.Fatal("warm candidate hit lost its source anchor")
		}
	}
	if e.ghost != offer || e.ghost.line != projection || e.hWidthMeasures != measures ||
		e.Revision() != revision || &e.buf.cachedBytes[0] != sourceCache || &e.hLineWidths[0] != widthCache || exports != 0 {
		t.Fatal("warm candidate frames rebuilt source/geometry or exported unchanged content")
	}
}

func BenchmarkEditorGhostWarmRender(b *testing.B) {
	for _, lines := range []int{20, 20000} {
		for _, offered := range []bool{false, true} {
			b.Run(fmt.Sprintf("lines-%d/offered-%v", lines, offered), func(b *testing.B) {
				e := newInlayTestEditor(b, strings.Repeat("short source\n", lines))
				e.SetCursorPosition(0, 6)
				if offered {
					ghostEntry(b, e, 6, "candidate ")
				}
				s := surface.NewSurface("ghost-bench", 80, 16, cell.TransparentBlank())
				e.Render(s, ui.PaintContext{})
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					e.Render(s, ui.PaintContext{})
					e.PositionAt(e.gutterWidth()+8, 0)
				}
			})
		}
	}
}
