package editor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func TestSyntaxLongPythonDocstringDoesNotInvertAtViewport(t *testing.T) {
	src := "def explain():\n    \"\"\"\n" + strings.Repeat("    documentation, not executable code\n", 900) + "    \"\"\"\n    return 42\n\n# actual comment\ndef after():\n    return 7\n"
	ed := loadEditorText(t, "long.py", src)
	// Jump directly into a long docstring, without ever painting its opener.
	ed.scrollY = 600
	buf := paintEditor(ed, 100, 16)
	wantString := ed.cellColor(ed.th.Colors.Code.String)
	if got := buf.Get(ed.gutterWidth()+4, 0); got.Char != 'd' || !got.FG.Equal(wantString) {
		t.Fatalf("docstring body paints as code: char=%q fg=%v want=%v", got.Char, got.FG, wantString)
	}
	// The closing delimiter is not an opener just because the original opener
	// lies outside an arbitrary scan window.
	ed.scrollY = 900
	buf = paintEditor(ed, 100, 12)
	wantKeyword := ed.cellColor(ed.th.Colors.Code.Keyword)
	if got := buf.Get(ed.gutterWidth()+4, 3); got.Char != 'r' || !got.FG.Equal(wantKeyword) {
		t.Fatalf("code after docstring paints as string: char=%q fg=%v want=%v", got.Char, got.FG, wantKeyword)
	}
	// Revisiting the same source at another geometry must not change its meaning.
	ed.scrollY = 600
	buf = paintEditor(ed, 45, 8)
	if got := buf.Get(ed.gutterWidth()+4, 0); !got.FG.Equal(wantString) {
		t.Fatalf("resized docstring fg=%v want=%v", got.FG, wantString)
	}
}

func TestSyntaxLongBlockCommentKeepsDocumentContext(t *testing.T) {
	ed := loadEditorText(t, "long.go", "package main\n/*\n"+strings.Repeat("body of the comment\n", 900)+"*/\nvar after = 42\n")
	ed.scrollY = 600
	buf := paintEditor(ed, 80, 12)
	if got := buf.Get(ed.gutterWidth(), 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("interior comment paints as code: %v", got)
	}
	ed.scrollY = 900
	buf = paintEditor(ed, 80, 12)
	if got := buf.Get(ed.gutterWidth(), 3); got.Char != 'v' || got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("code after block paints as comment: %v", got)
	}
}

func BenchmarkSyntaxLongPythonViewport(b *testing.B) {
	src := "\"\"\"\n" + strings.Repeat("documentation\n", 20_000) + "\"\"\"\ndef after():\n    return 7\n"
	ed := NewPreview(theme.DefaultDark(), "long.py", src, 1)
	b.Cleanup(func() { closeSyntax(ed) })
	ed.scrollY = 15_000
	settleSyntax(b, ed)
	ed.ensureChromaColors(15_000, 15_040)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ed.ensureChromaColors(15_000, 15_040)
	}
}

func TestSyntaxOffscreenDelimiterEditAndStaleCompletion(t *testing.T) {
	ed := loadEditorText(t, "edit.py", "\"\"\"\n"+strings.Repeat("# looks like a comment\n", 900)+"\"\"\"\nreturn_value = 42\n")
	ed.scrollY = 600
	first := paintEditor(ed, 80, 12)
	x := ed.gutterWidth()
	if got := first.Get(x, 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.String)) {
		t.Fatal("fixture not inside string")
	}
	// Change only an offscreen delimiter. Identical visible bytes must change
	// lexical meaning. Mapped unchanged-line decorations may remain visible
	// during preparation, but the replacement full-document parse must update
	// them even though their visible source bytes did not change.
	ed.SetSelection(0, 0, 0, 3)
	if !ed.DeleteSelection() {
		t.Fatal("delete opener")
	}
	ed.scrollY = 600
	settleSyntax(t, ed)
	buf := paintEditor(ed, 80, 12)
	if got := buf.Get(x, 0); got.Char != '#' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("offscreen edit did not recolour: %+v", got)
	}
	ed.HandleEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	ed.scrollY = 600
	settleSyntax(t, ed)
	if got := paintEditor(ed, 80, 12).Get(x, 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.String)) {
		t.Fatalf("undo did not restore lexical context: %+v", got)
	}
	// A completed-but-unapplied older parse cannot attach after a later edit.
	ed.invalidateSyntax()
	command := ed.SyntaxCommand()
	if command == nil {
		t.Fatal("missing syntax command")
	}
	msg := command().(SyntaxReadyMsg)
	ed.ApplyDisk([]byte("# new document\n"))
	msg.Apply()
	if ed.syntaxDocument != nil {
		t.Fatal("old completion installed after replacement")
	}
	settleSyntax(t, ed)
	ed.invalidateSyntax()
	command = ed.SyntaxCommand()
	ed.Close()
	command().(SyntaxReadyMsg).Apply()
	if ed.syntaxDocument != nil || ed.IsOpen() {
		t.Fatal("closed editor accepted completion")
	}
}

func TestSyntaxCRLFAndUnicodeKeepSourceOffsets(t *testing.T) {
	ed := loadEditorText(t, "unicode.py", "# 界🙂 comment\r\nname = \"é\"\r\nreturn_value = 42\r\n")
	buf := paintEditor(ed, 80, 10)
	x := ed.gutterWidth()
	if got := buf.Get(x, 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("comment=%+v", got)
	}
	if got := buf.Get(x+7, 1); got.Char != '"' || !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.String)) {
		t.Fatalf("CRLF shifted string offset: %+v", got)
	}
}

func TestSyntaxSnapshotDoesNotRelexForGeometryAndTheme(t *testing.T) {
	ed := loadEditorText(t, "theme.py", "# comment\nname = \"value\"\n")
	doc := ed.syntaxDocument
	ed.SetCursorPosition(1, 0)
	ed.InvalidateWidthProfile()
	paintEditor(ed, 45, 8)
	if ed.syntaxDocument != doc || ed.syntaxJob != nil {
		t.Fatal("geometry change re-lexed source")
	}
	ed.SetTheme(theme.DefaultLight())
	settleSyntax(t, ed)
	buf := paintEditor(ed, 45, 8)
	if got := buf.Get(ed.gutterWidth(), 0); !got.FG.Equal(ed.cellColor(ed.th.Colors.Code.Comment)) {
		t.Fatalf("old theme colours survived: %+v", got)
	}
}

func TestSyntaxNavigationDoesNotCancelPendingSourceWork(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "pending.py", "\"\"\"\n"+strings.Repeat("documentation\n", 2000)+"\"\"\"\n", 1)
	t.Cleanup(func() { closeSyntax(ed) })
	job := ed.syntaxJob
	for range 100 {
		ed.SetCursorPosition(1200, 2)
		ed.InvalidateWidthProfile()
	}
	if ed.syntaxJob != job || job.generation != ed.syntaxGeneration {
		t.Fatal("navigation superseded pending source parse")
	}
	settleSyntax(t, ed)
	if ed.syntaxDocument == nil {
		t.Fatal("navigation starved syntax publication")
	}
}

func BenchmarkSyntaxDocumentPreparation(b *testing.B) {
	for _, lines := range []int{200, 20_000} {
		b.Run(fmt.Sprintf("Python_%d", lines), func(b *testing.B) {
			ed := NewPreview(theme.DefaultDark(), "work.py", "\"\"\"\n"+strings.Repeat("documentation\n", lines)+"\"\"\"\ndef after():\n    return 7\n", 1)
			b.Cleanup(func() { closeSyntax(ed) })
			settleSyntax(b, ed)
			source := ed.buf.snapshotText()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := lexSyntax(context.Background(), ed.chromaLexer, source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
