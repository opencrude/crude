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

func loadEditorText(t *testing.T, name, text string) *Editor {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ed.Close() })
	settleSyntax(t, ed)
	return ed
}

func paintEditor(ed *Editor, w, h int) *surface.CellBuffer {
	surf := surface.NewSurface("ed", w, h, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})
	return surf.Buffer()
}

func TestSearchHighlight_LeadingTabPainted(t *testing.T) {
	ed := loadEditorText(t, "tab.go", "\tfoo bar\n")
	n := ed.SetQuery("foo")
	if n != 1 {
		t.Fatalf("matches=%d want 1", n)
	}
	m := ed.Search().Matches[0]
	if m.Start.Col != 4 || m.End.Col != 7 {
		t.Fatalf("match cols=%v-%v want 4-7 (tab stop)", m.Start.Col, m.End.Col)
	}

	buf := paintEditor(ed, 60, 8)
	sw := ed.gutterWidth()
	hl := ed.searchHighlightBg(true)
	// Visual cols 4..6 are 'f','o','o' after the leading tab.
	for col := 4; col < 7; col++ {
		c := buf.Get(sw+col, 0)
		if !c.BG.Equal(hl) {
			t.Fatalf("col %d char=%q bg=%v want search highlight", col, c.Char, c.BG)
		}
		want := rune("foo"[col-4])
		if c.Char != want {
			t.Fatalf("col %d char=%q want %q", col, c.Char, want)
		}
	}
	// Column 0 (inside tab padding) must NOT be highlighted.
	if buf.Get(sw+0, 0).BG.Equal(hl) {
		t.Fatal("tab padding col 0 must not be search-highlighted")
	}
}

func TestSearchHighlight_MultibytePainted(t *testing.T) {
	ed := loadEditorText(t, "mb.go", "日本語foo\n")
	n := ed.SetQuery("foo")
	if n != 1 {
		t.Fatalf("matches=%d", n)
	}
	m := ed.Search().Matches[0]
	wantStart := LineVisualWidth("日本語", tabWidth)
	if m.Start.Col != wantStart {
		t.Fatalf("start col=%d want %d", m.Start.Col, wantStart)
	}

	buf := paintEditor(ed, 60, 8)
	sw := ed.gutterWidth()
	hl := ed.searchHighlightBg(true)
	for col := wantStart; col < wantStart+3; col++ {
		c := buf.Get(sw+col, 0)
		if !c.BG.Equal(hl) {
			t.Fatalf("col %d char=%q bg=%v want highlight", col, c.Char, c.BG)
		}
	}
}

func TestSearchHighlight_EmojiZWJPainted(t *testing.T) {
	emoji := "👨‍👩‍👧"
	ed := loadEditorText(t, "zwj.go", emoji+"foo\n")
	n := ed.SetQuery("foo")
	if n != 1 {
		t.Fatalf("matches=%d", n)
	}
	m := ed.Search().Matches[0]
	wantStart := LineVisualWidth(emoji, tabWidth)
	if m.Start.Col != wantStart {
		t.Fatalf("start col=%d want %d", m.Start.Col, wantStart)
	}
	buf := paintEditor(ed, 60, 8)
	sw := ed.gutterWidth()
	hl := ed.searchHighlightBg(true)
	c := buf.Get(sw+wantStart, 0)
	if c.Char != 'f' || !c.BG.Equal(hl) {
		t.Fatalf("after ZWJ: char=%q bg=%v want f+highlight", c.Char, c.BG)
	}
}

func TestSearch_SetQuery_LeadingTabColumns(t *testing.T) {
	ed := loadEditorText(t, "t.go", "\tfoo\n")
	ed.SetQuery("foo")
	m := ed.Search().Matches[0]
	if m.Start != (Position{Line: 0, Col: 4}) || m.End != (Position{Line: 0, Col: 7}) {
		t.Fatalf("got start=%v end=%v", m.Start, m.End)
	}
}
