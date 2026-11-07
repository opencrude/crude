package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/theme"
)

// Typing in one view of a file shows in every other view in the same frame,
// and each view keeps its own caret on the same text it was on.
func TestLinkedViewsShowEveryEditAndKeepTheirOwnCarets(t *testing.T) {
	a, _ := projectionEditor(t, "alpha\nbeta\ngamma\n")
	b := a.NewLinkedView()
	t.Cleanup(b.Close)
	a.SetCursorPosition(0, 0)
	b.SetCursorPosition(2, 2) // "ga|mma"

	a.InsertRune('X')
	a.InsertRune('\n')
	if a.Value() != b.Value() || b.Value() != "X\nalpha\nbeta\ngamma\n" {
		t.Fatalf("views diverged: a=%q b=%q", a.Value(), b.Value())
	}
	if b.CursorLine() != 3 || b.CursorCol() != 2 {
		t.Fatalf("other view's caret did not follow its text: %d:%d", b.CursorLine(), b.CursorCol())
	}
	if a.CursorLine() != 1 || a.CursorCol() != 0 {
		t.Fatalf("typing view caret moved wrongly: %d:%d", a.CursorLine(), a.CursorCol())
	}

	// Deleting the text under the other view's caret collapses it to the edit.
	b.SetCursorPosition(1, 0)
	for range 6 { // "alpha\n"
		b.handleKey(tea.KeyPressMsg{Code: tea.KeyDelete})
	}
	if a.Value() != "X\nbeta\ngamma\n" || a.CursorLine() != 1 || a.CursorCol() != 0 {
		t.Fatalf("delete did not reach the first view: %q caret %d:%d", a.Value(), a.CursorLine(), a.CursorCol())
	}

	// Both views paint the same text.
	a.scrollY, b.scrollY = 0, 0
	a.viewW, a.viewH = 30, 6
	b.viewW, b.viewH = 30, 6
	if !strings.Contains(paintedText(a), "beta") || !strings.Contains(paintedText(b), "beta") || strings.Contains(paintedText(b), "alpha") {
		t.Fatalf("views paint different text:\n%s\n---\n%s", paintedText(a), paintedText(b))
	}
}

// All views share one document: one history, one dirty state and one set of
// pending canonical edits. Undo in any view undoes the last edit and restores
// the caret of the view that made it.
func TestLinkedViewsShareOneDocumentHistory(t *testing.T) {
	a, _ := projectionEditor(t, "one\ntwo\n")
	b := a.NewLinkedView()
	t.Cleanup(b.Close)
	b.SetCursorPosition(1, 3)
	b.InsertRune('!')
	if !a.Modified() || !b.Modified() || a.DocumentState().PendingEdits != 1 || b.DocumentState() != a.DocumentState() {
		t.Fatalf("views do not share the document: a=%+v b=%+v", a.DocumentState(), b.DocumentState())
	}
	c, err := b.CaptureDocumentEdits(projectionOwner, "r1", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Edits) != 1 || c.Edits[0].Text != "!" {
		t.Fatalf("capture %+v", c)
	}
	if err := a.AcceptDocumentReceipt(document.Receipt{DocumentID: c.DocumentID, RequestID: "r1", Before: c.BaseRevision, Revision: c.BaseRevision + 1, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	if b.DocumentState().PendingEdits != 0 {
		t.Fatalf("receipt accepted through one view did not settle the other: %+v", b.DocumentState())
	}
}

// A local (not core-connected) document has one undo history too: undo in
// any view undoes the last edit, whichever view made it.
func TestLinkedViewsShareLocalUndoHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.txt")
	if err := os.WriteFile(path, []byte("ab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(theme.DefaultDark())
	if err := a.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	b := a.NewLinkedView()
	t.Cleanup(b.Close)
	a.SetCursorPosition(0, 0)
	a.InsertRune('1')
	a.BreakUndoGroup()
	b.SetCursorPosition(0, 3)
	b.InsertRune('2')
	if a.Value() != "1ab2\n" {
		t.Fatalf("value %q", a.Value())
	}
	a.Undo()
	if b.Value() != "1ab\n" {
		t.Fatalf("undo in one view did not undo the other view's edit: %q", b.Value())
	}
	b.Undo()
	if a.Value() != "ab\n" || a.Modified() {
		t.Fatalf("second undo: %q modified=%v", a.Value(), a.Modified())
	}
	a.Redo()
	b.Redo()
	if a.Value() != "1ab2\n" {
		t.Fatalf("redo across views: %q", a.Value())
	}
}

// Closing one view keeps the document open in the others; closing the last
// view closes it.
func TestClosingOneLinkedViewKeepsTheDocument(t *testing.T) {
	a, _ := projectionEditor(t, "keep me\n")
	b := a.NewLinkedView()
	c := a.NewLinkedView()
	if a.Primary() != a || b.Primary() != a || len(a.LinkedViews()) != 3 {
		t.Fatalf("link set: primary=%p views=%d", b.Primary(), len(a.LinkedViews()))
	}
	b.InsertRune('x')
	a.Close()
	if !c.IsOpen() || !b.IsOpen() || c.Value() != "xkeep me\n" || c.DocumentState().DocumentID == "" {
		t.Fatalf("closing the primary closed the document: open=%v value=%q state=%+v", c.IsOpen(), c.Value(), c.DocumentState())
	}
	if b.Primary() != b && b.Primary() != c {
		t.Fatal("primary was not handed over")
	}
	c.InsertRune('y')
	if b.Value() != c.Value() {
		t.Fatalf("remaining views diverged: %q %q", b.Value(), c.Value())
	}
	b.Close()
	if !c.IsOpen() || len(c.LinkedViews()) != 1 || c.Primary() != c {
		t.Fatalf("last view is not standalone: views=%d", len(c.LinkedViews()))
	}
	c.Close()
}

// A remote edit applied to the primary reaches every view.
func TestLinkedViewsFollowRemoteEdits(t *testing.T) {
	a, s := projectionEditor(t, "abc\ndef\n")
	b := a.NewLinkedView()
	t.Cleanup(b.Close)
	b.SetCursorPosition(1, 2)
	other, _ := projectionEditor(t, "abc\ndef\n")
	other.SetCursorPosition(0, 0)
	other.insertText("Z\n")
	c, err := other.CaptureDocumentEdits(document.Owner{ClientID: "other", Epoch: 1}, "foreign", "")
	if err != nil {
		t.Fatal(err)
	}
	s.Text, _, err = document.Apply(s.Text, c.Edits)
	if err != nil {
		t.Fatal(err)
	}
	s.Revision = 2
	if err := a.ApplyDocumentDelta(c, s); err != nil {
		t.Fatal(err)
	}
	if b.Value() != "Z\nabc\ndef\n" || b.CursorLine() != 2 || b.CursorCol() != 2 {
		t.Fatalf("linked view missed the remote edit: %q (primary %q, edits %+v) caret %d:%d", b.Value(), a.Value(), c.Edits, b.CursorLine(), b.CursorCol())
	}
}

// Ctrl+click on a symbol places the caret there and asks for its definition;
// Ctrl+Alt+click asks for implementations. Ctrl+hover underlines the symbol.
func TestCtrlClickRequestsNavigationAtTheSymbol(t *testing.T) {
	e, _ := projectionEditor(t, "call target(x)\n")
	e.viewW, e.viewH = 40, 5
	x := e.gutterWidth() + 6 // inside "target"
	if !e.updateCodeLink(x, 0, tea.ModCtrl) || e.link == nil || e.link.start != 5 || e.link.end != 11 {
		t.Fatalf("Ctrl+hover did not underline the symbol: %+v", e.link)
	}
	if e.updateCodeLink(x, 0, 0); e.link != nil {
		t.Fatal("underline stayed after Ctrl was released")
	}
	cmd := e.HandleEvent(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+click returned no navigation")
	}
	msg, ok := cmd().(NavigateAtMsg)
	if !ok || msg.Editor != e || msg.Implementation || e.CursorLine() != 0 || e.CursorCol() != 6 || e.HasSelection() {
		t.Fatalf("Ctrl+click: msg=%+v caret=%d:%d", msg, e.CursorLine(), e.CursorCol())
	}
	cmd = e.HandleEvent(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft, Mod: tea.ModCtrl | tea.ModAlt})
	if msg, ok := cmd().(NavigateAtMsg); !ok || !msg.Implementation {
		t.Fatalf("Ctrl+Alt+click did not ask for implementations: %+v", msg)
	}
	// Ctrl+click on punctuation is an ordinary click.
	if cmd := e.HandleEvent(tea.MouseClickMsg{X: e.gutterWidth() + 11, Y: 0, Button: tea.MouseLeft, Mod: tea.ModCtrl}); cmd != nil {
		if _, ok := cmd().(NavigateAtMsg); ok {
			t.Fatal("Ctrl+click on punctuation navigated")
		}
	}
}

func paintedText(e *Editor) string {
	var b strings.Builder
	for line := e.scrollY; line < min(e.LineCount(), e.scrollY+e.bodyHeight()); line++ {
		b.WriteString(e.LineText(line))
		b.WriteByte('\n')
	}
	return b.String()
}
