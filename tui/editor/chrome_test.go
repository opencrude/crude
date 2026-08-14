package editor

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestRenderPaintsSharedScrollbarChrome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.go")
	var b []byte
	for i := 0; i < 40; i++ {
		b = append(b, []byte("line\n")...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetStatusChrome(false)

	surf := surface.NewSurface("ed", 40, 10, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})

	sm := ed.ScrollModel()
	if sm == nil || sm.MaxOffset() <= 0 {
		t.Fatal("expected scrollable content")
	}
	foundThumb := false
	for y := 0; y < 10; y++ {
		c := surf.Buffer().Get(39, y)
		if c.Char == '█' {
			foundThumb = true
		}
		if c.Char != '░' && c.Char != '█' && c.Char != ' ' {
			t.Fatalf("scrollbar col unexpected rune %q at y=%d", c.Char, y)
		}
	}
	if !foundThumb {
		t.Fatal("expected scrollbar thumb")
	}
}

func TestRenderDiagnosticsAndSearchChrome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.go")
	if err := os.WriteFile(path, []byte("alpha beta alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetDiagnostics([]LSPDiagnostic{{
		Line: 0, Col: 0, EndLine: 0, EndCol: 5, Severity: DiagError, Message: "err",
	}})
	ed.SetQuery("alpha")

	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()

	// Gutter diagnostic icon in icon column (gutterWidth-3).
	sw := ed.gutterWidth()
	icon := buf.Get(sw-3, 0)
	if icon.Char != '✕' {
		t.Fatalf("diag icon = %q, want ✕", icon.Char)
	}

	// Search highlight: current match uses Accent2 bg.
	foundHL := false
	for x := sw; x < 49; x++ {
		c := buf.Get(x, 0)
		if c.Char == 'a' && c.BG.Equal(ed.searchHighlightBg(true)) {
			foundHL = true
			break
		}
		if c.Char == 'a' && c.BG.Equal(ed.searchHighlightBg(false)) {
			foundHL = true
			break
		}
	}
	if !foundHL {
		t.Fatal("expected search highlight background on match")
	}
}

func TestRenderGitGutterBesideDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.SetDiagnostics([]LSPDiagnostic{{
		Line: 0, Col: 0, EndLine: 0, EndCol: 5, Severity: DiagError, Message: "err",
	}})
	ed.SetGitAnnotation([]GitLineKind{GitLineAdded, GitLineModified}, nil)

	surf := surface.NewSurface("ed", 50, 6, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	buf := surf.Buffer()
	sw := ed.gutterWidth()
	if got := buf.Get(sw-3, 0).Char; got != '✕' {
		t.Fatalf("diag col = %q, want ✕", got)
	}
	if got := buf.Get(sw-2, 0).Char; got != '+' {
		t.Fatalf("git added col = %q, want +", got)
	}
	if got := buf.Get(sw-2, 1).Char; got != '~' {
		t.Fatalf("git modified col = %q, want ~", got)
	}
}
