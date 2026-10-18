package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestEditorSyntaxCompletionFollowsOwnerAcrossTabChange(t *testing.T) {
	app, _ := newRealShellApp(t)
	path := filepath.Join(app.ws.RootPath(), "long.py")
	if err := os.WriteFile(path, []byte("\"\"\"\n"+strings.Repeat("documentation\n", 900)+"\"\"\"\nreturn_value = 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := openEditorTabs(t, app, "long.py", "other.go")
	// Complete the real App command path while the other tab owns focus.
	runAppCommand(t, app, app.drainEditorSyntaxCommands())
	ed := app.EditorState.Tabs[paths[0]].Editor
	if ed.SyntaxPending() {
		t.Fatal("hidden editor completion was lost to selected tab")
	}
	app.activateEditorTab(path)
	ed.SetCursorPosition(600, 0)
	ed.ScrollToCursor()
	buf := surface.NewSurface("syntax-acceptance", 80, 12, cell.TransparentBlank())
	ed.Render(buf, ui.PaintContext{Theme: app.Theme})
	want := rendering.ThemeColorToCell(app.Theme.Colors.Code.String)
	found := false
	for y := 0; y < 12; y++ {
		for x := 0; x < 80; x++ {
			c := buf.Buffer().Get(x, y)
			if c.Char == 'd' && c.FG.Equal(want) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("returning to completed editor paints docstring as code")
	}
	app.StopAsyncIO()
	if ed.IsOpen() {
		t.Fatal("syntax lifecycle retained editor after shutdown")
	}
}

func TestSyntaxCompletionAfterCloseDoesNotReopenEditor(t *testing.T) {
	ed := editor.NewPreview(theme.DefaultDark(), "work.py", strings.Repeat("# comment\n", 500), 1)
	command := ed.SyntaxCommand()
	ed.Close()
	if command != nil {
		command().(editor.SyntaxReadyMsg).Apply()
	}
	if done := ed.SyntaxDone(); done != nil {
		<-done
	}
	if ed.IsOpen() {
		t.Fatal("closed editor reopened by old completion")
	}
}
