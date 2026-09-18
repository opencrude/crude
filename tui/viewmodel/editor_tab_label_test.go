package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

// paintedTabStrip renders the shell tab strip and returns its label row, so tab
// marker assertions read what the user actually sees instead of the label field.
func paintedTabStrip(t *testing.T, app *App, width int) string {
	t.Helper()
	app.syncTabBarHost()
	tabs := app.tabScreen.TabsControl()
	tabs.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: width, H: ui.StripHeight})
	buf := surface.NewCellBuffer(width, ui.StripHeight)
	tabs.Paint(buf, 0, 0, ui.PaintContext{})
	var b strings.Builder
	for x := 0; x < width; x++ {
		b.WriteRune(buf.Get(x, 0).Char)
	}
	return b.String()
}

// A dirty editor tab carries exactly one unsaved marker, in front of the
// filename, with no marker glued to the name and no second dot beside it.
func TestEditorTabPrefixesTheUnsavedMarker(t *testing.T) {
	app := newFullTestApp(t)
	path := filepath.Join(t.TempDir(), "dirty.go")
	ed := newSaveTestEditorAt(t, app, path, "base")
	app.Win.OpenTab(app.editorTabTitle(path), "✎", true)

	if got := paintedTabStrip(t, app, 40); strings.Contains(got, "●") {
		t.Fatalf("clean tab already shows an unsaved marker: %q", got)
	}

	ed.InsertRune('x')
	got := paintedTabStrip(t, app, 40)
	if !strings.Contains(got, "● dirty.go") {
		t.Fatalf("dirty tab label=%q, want a prefix unsaved marker", got)
	}
	if strings.Contains(got, "dirty.go ●") {
		t.Fatalf("unsaved marker collides with the filename: %q", got)
	}
	if strings.Contains(got, "● ●") {
		t.Fatalf("unsaved marker duplicated: %q", got)
	}

	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	if got := paintedTabStrip(t, app, 40); strings.Contains(got, "●") {
		t.Fatalf("saved tab kept the unsaved marker: %q", got)
	}
}

// The tab marker follows a rename: the strip must show the new filename with
// its unsaved marker, never the old path.
func TestEditorTabMarkerTracksRenamedPath(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "before.go")
	newPath := filepath.Join(root, "after.go")
	if err := os.WriteFile(oldPath, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, _ := newUndrainedTreeMutationApp(t, root)
	if _, err := app.openEditorTab(oldPath); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Editor.InsertRune('x')
	if got := paintedTabStrip(t, app, 40); !strings.Contains(got, "● before.go") {
		t.Fatalf("pre-rename label=%q", got)
	}

	app.startRenameInTree(oldPath)
	app.TreePrompt.Edit.SetValue(filepath.Base(newPath))
	updateTreeMutationWithoutDeadlock(t, app, "rename with a dirty editor tab")

	got := paintedTabStrip(t, app, 40)
	if !strings.Contains(got, "● after.go") || strings.Contains(got, "before.go") {
		t.Fatalf("renamed tab label=%q, want the new path with its marker", got)
	}
}

// The editor status prefix owns one dirty marker; the old code appended a second
// dot because Editor.Title() already carried one.
func TestEditorStatusPrefixShowsOneUnsavedMarker(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	app.EditorState.ActiveView = ed.FilePath()
	ed.InsertRune('x')

	prefix, _ := app.contextualHelpSegments()
	if !strings.HasPrefix(prefix, "● ") {
		t.Fatalf("status prefix=%q, want a leading unsaved marker", prefix)
	}
	if count := strings.Count(prefix, "●"); count != 1 {
		t.Fatalf("status prefix=%q carries %d markers, want 1", prefix, count)
	}
	if strings.HasSuffix(strings.SplitN(prefix, "  ", 2)[0], " ●") {
		t.Fatalf("status prefix=%q still glues the marker to the filename", prefix)
	}
}
