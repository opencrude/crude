package editor

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"regexp"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/syntax"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/types"
)

// flashFixture is a connected Go document whose canonical syntax comes from the
// real native parser and whose semantic colors mark every identifier, as an
// installed language server does.
type flashFixture struct {
	t        *testing.T
	e        *Editor
	language syntax.LanguageID
	version  uint64
	sequence uint64
	// semanticFirst delivers the provider's colors before the core's syntax.
	semanticFirst bool
}

func newFlashFixture(t *testing.T, source string, semanticFirst bool) *flashFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.go")
	language, ok := syntax.LanguageForPath(path)
	if !ok {
		t.Skip("native Go grammar unavailable")
	}
	snapshot := document.Snapshot{ID: "flash", Path: path, Revision: 1, AnalysisGeneration: "flash-workspace", AnalysisLifetime: "flash-open",
		Text: []byte(source), Saved: []byte(source), DiskExists: true, DiskHash: sha256.Sum256([]byte(source))}
	e := New(theme.DefaultDark())
	t.Cleanup(func() { closeSyntax(e) })
	if err := e.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	e.SetStatusChrome(false)
	f := &flashFixture{t: t, e: e, language: language, semanticFirst: semanticFirst}
	f.publish()
	return f
}

var flashIdentifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// publish delivers the core's syntax and the provider's semantic colors for the
// acknowledged revision, in the order the shell normally receives them.
// publish returns the frame painted between the two deliveries.
func (f *flashFixture) publish() *surface.CellBuffer {
	f.t.Helper()
	if f.semanticFirst {
		f.publishSemantic()
		between := paintEditor(f.e, 100, 20)
		f.publishSyntax()
		return between
	}
	f.publishSyntax()
	between := paintEditor(f.e, 100, 20)
	f.publishSemantic()
	return between
}

func (f *flashFixture) publishSyntax() {
	f.t.Helper()
	snapshot := f.e.DocumentSnapshot()
	session, err := syntax.New(f.language)
	if err != nil {
		f.t.Fatal(err)
	}
	result, err := session.Parse(context.Background(), snapshot.Revision, snapshot.Text)
	session.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	f.version++
	f.e.InstallDocumentInsight(types.DocumentInsight{DocumentID: snapshot.ID, Path: snapshot.Path, Revision: snapshot.Revision,
		Generation: "flash-core", Version: f.version, State: types.InsightCurrent, Syntax: &result})
}

func (f *flashFixture) publishSemantic() {
	f.t.Helper()
	snapshot := f.e.DocumentSnapshot()
	source, ready := f.e.CurrentInsightSource()
	if !ready {
		f.t.Fatal("acknowledged source is not ready for semantic colors")
	}
	f.sequence++
	set := types.LSPSemanticSet{Boundary: types.InsightBoundary{Source: source}, ViewID: "flash", Sequence: f.sequence, Types: []string{"variable"}}
	for _, m := range flashIdentifier.FindAllIndex(snapshot.Text, -1) {
		set.Spans = append(set.Spans, types.LSPSemanticSpan{Start: m[0], End: m[1], Type: 0})
	}
	if err := f.e.SetSemanticTokens(&set); err != nil {
		f.t.Fatal(err)
	}
}

func (f *flashFixture) acknowledge() {
	f.t.Helper()
	if f.e.DocumentState().PendingEdits == 0 {
		return // overtyping an auto-closed quote leaves the source unchanged
	}
	c, err := f.e.CaptureDocumentEdits(projectionOwner, "flash-edit", "flash-group")
	if err != nil || c.DocumentID == "" {
		f.t.Fatalf("capture: %+v %v", c, err)
	}
	if err := f.e.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: c.RequestID, Before: c.BaseRevision, Revision: c.BaseRevision + 1, Accepted: true}); err != nil {
		f.t.Fatal(err)
	}
}

// changedInk counts cells of unedited source lines whose character is
// unchanged but whose ink differs. Lines after the edit moved by delta rows;
// rows first..last of the new frame hold the edited text.
func changedInk(e *Editor, before, after *surface.CellBuffer, first, last, delta int) int {
	changed := 0
	w, h := before.Width(), before.Height()
	for y := 0; y < h; y++ {
		if y >= first && y <= last {
			continue
		}
		from := y
		if y > last {
			from = y - delta
		}
		if from < 0 || from >= h {
			continue
		}
		for x := e.gutterWidth(); x < w-2; x++ {
			a, b := before.Get(x, from), after.Get(x, y)
			if a.Char == b.Char && a.Char != ' ' && !a.FG.Equal(b.FG) {
				changed++
			}
		}
	}
	return changed
}

// The user typed a Go block and saw the whole visible buffer flash. Each frame
// between the keystroke and the settled publications must keep the ink of every
// unrelated line: pending local text, the receipt, the new syntax and the new
// semantic colors are separate deliveries in a connected shell.
func TestTypingABlockNeverFlashesUnrelatedSyntaxInk(t *testing.T) {
	for _, semanticFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "syntax-first", true: "semantic-first"}[semanticFirst], func(t *testing.T) {
			typeABlockWithoutFlashes(t, semanticFirst)
		})
	}
}

func typeABlockWithoutFlashes(t *testing.T, semanticFirst bool) {
	source := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\thome := lookup()\n\t\n\tfmt.Println(home, \"done\") // trailing comment\n}\n\nfunc lookup() string { return \"\" }\n"
	f := newFlashFixture(t, source, semanticFirst)
	f.e.SetCursorPosition(6, f.e.lineVisualWidth(6)) // after the indentation
	keys := []tea.KeyPressMsg{{Code: 'i', Text: "i"}, {Code: 'f', Text: "f"}, {Code: ' ', Text: " "}}
	for _, r := range `home == ""{` {
		keys = append(keys, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	keys = append(keys, tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: 'x', Text: "x"}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	for _, key := range keys {
		row, lines := f.e.cursor.Line, f.e.LineCount()
		settled := paintEditor(f.e, 100, 20)
		f.e.HandleEvent(key)
		delta := f.e.LineCount() - lines
		last := row + max(0, delta)
		frames := []*surface.CellBuffer{paintEditor(f.e, 100, 20)} // before any parse
		joinSyntax(t, f.e)
		frames = append(frames, paintEditor(f.e, 100, 20))
		f.acknowledge()
		frames = append(frames, paintEditor(f.e, 100, 20))
		frames = append(frames, f.publish())
		final := paintEditor(f.e, 100, 20)
		for i, frame := range frames {
			if n := changedInk(f.e, settled, frame, row, last, delta); n != 0 && changedInk(f.e, frame, final, row, last, 0) != 0 {
				t.Fatalf("key %q: frame %d recolored %d unrelated cells that neither the old nor the new publication painted", key.String(), i, n)
			}
		}
	}
	if got := f.e.LineText(6); got != "\tif home == \"\"{" {
		t.Fatalf("typed line %q", got)
	}
}
