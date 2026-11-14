package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/syntax"
	"crude/tui/theme"
	"crude/types"
)

func semanticFixtureSet(e *Editor) types.LSPSemanticSet {
	source, _ := e.CurrentInsightSource()
	return types.LSPSemanticSet{Boundary: types.InsightBoundary{Source: source}, ViewID: "one", Sequence: 1, Types: []string{"function", "unknown"}, Spans: []types.LSPSemanticSpan{{Start: 0, End: 4, Type: 0}, {Start: 5, End: 10, Type: 1}}}
}
func TestSemanticPaintUsesExactSourceAndThemeWithoutEditing(t *testing.T) {
	e := newInlayTestEditor(t, "call value\n")
	e.SetCursorPosition(0, 10)
	e.SetSelection(0, 5, 0, 10)
	set := semanticFixtureSet(e)
	revision := e.Revision()
	selected := e.SelectedText()
	if err := e.SetSemanticTokens(&set); err != nil {
		t.Fatal(err)
	}
	b := paintEditor(e, 80, 8)
	x := e.gutterWidth()
	if got := b.Get(x, 0).FG; !got.Equal(e.cellColor(e.th.Colors.Code.Function)) {
		t.Fatal("semantic function did not use theme", got)
	}
	if got := b.Get(x+5, 0).FG; got.Equal(e.cellColor(e.th.Colors.Code.Function)) {
		t.Fatal("unknown token did not fall back", got)
	}
	if e.Value() != "call value\n" || e.Revision() != revision || e.SelectedText() != selected || e.Modified() {
		t.Fatal("semantic paint changed source/history")
	}
	e.SetTheme(theme.DefaultLight())
	b = paintEditor(e, 80, 8)
	if got := b.Get(x, 0).FG; !got.Equal(e.cellColor(e.th.Colors.Code.Function)) {
		t.Fatal("theme did not reproject semantic ink", got)
	}
	// An unacknowledged edit adjusts provider colors instead of dropping them
	// (VS Code's SparseMultilineTokens): the inserted space is not part of the
	// token and "call" keeps its ink one cell later.
	e.ClearSelection()
	e.SetCursorPosition(0, 0)
	e.InsertRune(' ')
	e.SetCursorPosition(1, 0) // keep the caret cell out of the measured ink
	b = paintEditor(e, 80, 8)
	for col := 1; col <= 4; col++ {
		if got := b.Get(x+col, 0).FG; !got.Equal(e.cellColor(e.th.Colors.Code.Function)) {
			t.Fatalf("adjusted token lost its ink at column %d: %+v", col, b.Get(x+col, 0))
		}
	}
	if err := e.SetSemanticTokens(&set); err == nil {
		t.Fatal("stale semantic source accepted")
	}
}

// The syntax publication and semantic reply can arrive in either order. Unknown
// semantic types must keep lexical ink, and edits adjust semantic ink.
func TestSemanticColorsOverlayLexicalFallbackInEitherArrivalOrder(t *testing.T) {
	for _, first := range []string{"semantic", "syntax"} {
		t.Run(first, func(t *testing.T) {
			e := newInlayTestEditor(t, "call value\n")
			snapshot := e.DocumentSnapshot()
			snapshot.Path = filepath.Join(t.TempDir(), "source.go")
			if err := e.InstallDocument(snapshot); err != nil {
				t.Fatal(err)
			}
			set := semanticFixtureSet(e)
			syntaxView := syntaxPublication(snapshot, 1, syntax.Comment)
			install := func() {
				t.Helper()
				if err := e.SetSemanticTokens(&set); err != nil {
					t.Fatal(err)
				}
			}
			if first == "semantic" {
				install()
				paintEditor(e, 80, 8)
				e.InstallDocumentInsight(syntaxView)
			} else {
				e.InstallDocumentInsight(syntaxView)
				paintEditor(e, 80, 8)
				install()
			}
			b := paintEditor(e, 80, 8)
			x := e.GutterWidth()
			if !b.Get(x+1, 0).FG.Equal(e.cellColor(e.th.Colors.Code.Function)) || !b.Get(x+5, 0).FG.Equal(e.cellColor(e.th.Colors.Code.Comment)) {
				t.Fatalf("%s overlay: first=%+v wantFunction=%+v second=%+v wantComment=%+v canonical=%v", first, b.Get(x+1, 0), e.cellColor(e.th.Colors.Code.Function), b.Get(x+5, 0), e.cellColor(e.th.Colors.Code.Comment), e.canonicalInk != nil)
			}
			e.ClearSemanticTokens()
			b = paintEditor(e, 80, 8)
			if !b.Get(x+1, 0).FG.Equal(e.cellColor(e.th.Colors.Code.Comment)) {
				t.Fatal("clearing semantic tokens did not restore syntax")
			}
			install()
			paintEditor(e, 80, 8)
			// Typing inside the token keeps its ink while the edit is pending,
			// as VS Code adjusts tokens instead of repainting lexical colors.
			e.SetCursorPosition(0, 2)
			e.InsertRune('X')
			e.SetCursorPosition(1, 0) // keep the caret cell out of the measured ink
			b = paintEditor(e, 80, 8)
			for col := 0; col <= 4; col++ {
				if !b.Get(x+col, 0).FG.Equal(e.cellColor(e.th.Colors.Code.Function)) {
					t.Fatalf("pending edit dropped semantic ink at column %d", col)
				}
			}
		})
	}
}

// A connected 20,000-line source with one real semantic span per line. Time
// the key mutation and its first editor-grid paint separately; restoring the
// acknowledged source and simulating fresh provider evidence is outside each
// timer. This does not measure terminal transmission or provider decoding.
func BenchmarkSemanticEditingDense(b *testing.B) {
	const lines = 20000
	const line = "value = 1\n"
	for _, enabled := range []bool{false, true} {
		for _, stage := range []string{"mutation", "paint", "stable_paint"} {
			b.Run(fmt.Sprintf("semantic%t/%s", enabled, stage), func(b *testing.B) {
				e := newInlayTestEditor(b, strings.Repeat(line, lines))
				defer closeSyntax(e)
				spans := make([]types.LSPSemanticSpan, lines)
				for i := range spans {
					spans[i] = types.LSPSemanticSpan{Start: i * len(line), End: i*len(line) + 5, Type: 0}
				}
				set := types.LSPSemanticSet{ViewID: "dense", Sequence: 1, Types: []string{"function"}, Spans: spans}
				key := tea.KeyPressMsg{Code: 'X', Text: "X"}
				back := tea.KeyPressMsg{Code: tea.KeyBackspace}
				accept := func(id string) {
					c, err := e.CaptureDocumentEdits(projectionOwner, id, "")
					if err != nil {
						b.Fatal(err)
					}
					if err := e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: c.BaseRevision, Revision: c.BaseRevision + 1, Accepted: true}); err != nil {
						b.Fatal(err)
					}
				}
				if stage == "stable_paint" {
					e.SetCursorPosition(0, 5)
					if enabled {
						set.Boundary.Source, _ = e.CurrentInsightSource()
						if err := e.SetSemanticTokens(&set); err != nil {
							b.Fatal(err)
						}
					}
					paintEditor(e, 120, 40)
					joinSyntax(b, e)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// Stable paint has no new provider publication or source
					// mutation. Prepare that immutable state once, not an
					// O(source) off-timer reinstallation before every frame.
					if stage == "stable_paint" {
						paintEditor(e, 120, 40)
						continue
					}
					b.StopTimer()
					e.SetCursorPosition(0, 5)
					if enabled {
						source, ready := e.CurrentInsightSource()
						if !ready {
							b.Fatal("source not acknowledged")
						}
						set.Boundary.Source = source
						if err := e.SetSemanticTokens(&set); err != nil {
							b.Fatal(err)
						}
					}
					paintEditor(e, 120, 40)
					joinSyntax(b, e)
					if stage == "paint" {
						e.HandleEvent(key)
						// The shell drains SyntaxCommand after input. HandleEvent
						// alone does not admit the new source job, so joining only
						// an old job let Render start source work inside this
						// paint-only sample.
						settleSyntax(b, e)
					}
					b.StartTimer()
					switch stage {
					case "mutation":
						e.HandleEvent(key)
					case "paint", "stable_paint":
						paintEditor(e, 120, 40)
					}
					b.StopTimer()
					if stage != "stable_paint" {
						accept("insert")
						e.HandleEvent(back)
						accept("delete")
					}
					b.StartTimer()
				}
			})
		}
	}
}
