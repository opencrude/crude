package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/surface"
)

func countNonBlankGlyphs(buf *surface.CellBuffer) int {
	if buf == nil {
		return 0
	}
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 && c.Char != ' ' {
				n++
			}
		}
	}
	return n
}

func bufferContainsPlain(buf *surface.CellBuffer, needle string) bool {
	if buf == nil || needle == "" {
		return false
	}
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		if strings.Contains(b.String(), needle) {
			return true
		}
	}
	return false
}

// TestOpenFile_FirstFramePaintsEditorGlyphs asserts the very first View()
// after opening a file paints real editor content — no blink / second frame.
func TestOpenFile_FirstFramePaintsEditorGlyphs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unique_marker_alpha.go")
	body := "package unique_marker_alpha\n\nfunc AlphaMarker() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)

	// Paint a launcher frame first, then open the file: the next View()
	// must lay out the editor rect and paint real content immediately.
	app.EditorState.ActiveView = ""
	_ = app.View()

	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	// Do NOT run blink / second frame — only the immediate View after open.
	_ = app.View()

	if app.editorRect.Empty() {
		t.Fatal("first frame: editorRect must be laid out")
	}
	if !app.isEditorActive() || !app.shellSlotActive(shellEditorSlot) {
		t.Fatal("first frame: editor slot must be active")
	}
	buf := app.mainSurf.Buffer()
	glyphs := countNonBlankGlyphs(buf)
	if glyphs < 10 {
		t.Fatalf("first frame: expected editor glyphs, got %d non-blank cells", glyphs)
	}
	if !bufferContainsPlain(buf, "unique_marker_alpha") && !bufferContainsPlain(buf, "AlphaMarker") {
		t.Fatal("first frame: shell surface missing opened file content")
	}
}

// TestOpenFile_RepeatedFramesKeepPainting verifies the shell surface is
// repainted unconditionally on every View() — a chrome-only frame (no dirty
// mark, no size change) must still show the file, not a stale buffer.
func TestOpenFile_RepeatedFramesKeepPainting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guard_file.go")
	if err := os.WriteFile(path, []byte("package guard_file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, dir)
	_ = app.View() // launcher frame
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if !bufferContainsPlain(app.mainSurf.Buffer(), "guard_file") {
		t.Fatal("setup: first editor frame must paint guard_file")
	}

	// Chrome-only frames (tree hover, cursor blink ticks, status ticks)
	// previously relied on the contentDirty cache gate; painting is now
	// unconditional, so repeated frames must keep the file visible.
	for i := 0; i < 3; i++ {
		_ = app.View()
		if countNonBlankGlyphs(app.mainSurf.Buffer()) < 5 {
			t.Fatalf("frame %d: repeated View() dropped editor glyphs", i)
		}
		if !bufferContainsPlain(app.mainSurf.Buffer(), "guard_file") {
			t.Fatalf("frame %d: repeated View() lost guard_file content", i)
		}
	}
}

// TestEditorTabSwitch_FirstFramePaintsNewFile asserts switching tabs paints
// the newly selected file immediately — not the previous file until blink.
func TestEditorTabSwitch_FirstFramePaintsNewFile(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "file_aaa.go")
	pathB := filepath.Join(dir, "file_bbb.go")
	if err := os.WriteFile(pathA, []byte("package file_aaa\n\nfunc TripleA() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("package file_bbb\n\nfunc TripleB() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if !bufferContainsPlain(app.mainSurf.Buffer(), "file_aaa") {
		t.Fatal("setup: tab A should paint file_aaa")
	}

	if _, err := app.openEditorTab(pathB); err != nil {
		t.Fatal(err)
	}
	// Immediate first frame after switch — no blink.
	_ = app.View()
	buf := app.mainSurf.Buffer()
	if !bufferContainsPlain(buf, "file_bbb") && !bufferContainsPlain(buf, "TripleB") {
		t.Fatal("tab switch first frame must paint file B content")
	}
	if bufferContainsPlain(buf, "TripleA") {
		t.Fatal("tab switch first frame must not keep file A content")
	}

	// Switch back to A via tab bar / handleTabSwitch.
	if tab := app.Win.TabByTitle(app.editorTabTitle(pathA)); tab == nil {
		t.Fatal("missing tab A")
	} else {
		app.Win.SwitchTo(app.Win.IndexOf(tab.ID))
		app.handleTabSwitch()
	}
	_ = app.View()
	buf = app.mainSurf.Buffer()
	if !bufferContainsPlain(buf, "file_aaa") && !bufferContainsPlain(buf, "TripleA") {
		t.Fatal("switch back first frame must paint file A")
	}
	if bufferContainsPlain(buf, "TripleB") {
		t.Fatal("switch back first frame must not keep file B content")
	}
}
