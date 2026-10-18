package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/document"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Seed a real workspace checkpoint, close it, then remove its parent before the
// core opens the workspace. This is the persisted workload that disabled every
// editor and file mutation, not a mock failure of a particular UI entry point.
func seedMissingDocumentParent(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "removed-folder")
	path := filepath.Join(dir, "retained.txt")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("retained source"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
	_, err := seed.LiveDocuments.Open(document.Owner{ClientID: "seed", Epoch: 1}, path)
	seed.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
}

func editorTreePoint(t *testing.T, app *App, name string) (int, int) {
	t.Helper()
	_ = app.View()
	r, ok := app.rootHost.ScreenRect(shelltree.TreeID)
	if !ok || r.Empty() {
		t.Fatal("file tree is not mounted")
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		if strings.Contains(rowText(app, y), name) {
			return r.X + 3, y
		}
	}
	t.Fatalf("tree did not paint %s", name)
	return 0, 0
}

func editorChooseTreeMenu(t *testing.T, app *App, label string) {
	t.Helper()
	_ = app.View()
	st := app.LastContextMenu
	if st == nil {
		t.Fatal("tree context menu is not open")
	}
	for i := 0; i < len(st.FlatItems) && paneMenuLabel(st) != label; i++ {
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		runAppCommand(t, app, cmd)
	}
	if paneMenuLabel(st) != label {
		t.Fatalf("tree menu does not contain %q", label)
	}
	fw := app.overlayHost.Window(ui.OverlayContextMenu)
	cw := fw.Content.(*ui.ComponentWidget)
	list := ui.ContextMenuList(cw)
	r, ok := cw.Host().ScreenRect(ui.ComponentID(ui.ContextMenuListID))
	if !ok {
		t.Fatal("tree context menu list is not mounted")
	}
	x, y := fw.Position()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + r.X + 1, Y: y + r.Y + list.SelectedIndex() - list.Scroll().Offset(), Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
}

func TestEditorRoutesSurviveRetainedMissingParent(t *testing.T) {
	for _, width := range []int{160, 80} {
		for _, route := range []string{"click", "context open", "context window", "pane picker", "navigation"} {
			t.Run(fmt.Sprintf("%d/%s", width, route), func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				root := t.TempDir()
				path := filepath.Join(root, "healthy.txt")
				const content = "EDITOR_OPEN_SURVIVED\n"
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				seedMissingDocumentParent(t, root)
				runtime := remoteJourneyRuntime(t, "http://unused.invalid")
				shell := newRemoteJourneyShell(t, runtime, root)
				app := shell.app
				_, cmd := app.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				runAppCommand(t, app, cmd)
				runAppCommand(t, app, app.beginClientFileTreeLoad())
				x, y := editorTreePoint(t, app, "healthy.txt")
				switch route {
				case "click":
					_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
					runAppCommand(t, app, cmd)
				case "context open", "context window":
					_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
					runAppCommand(t, app, cmd)
					action := "Open"
					if route == "context window" {
						action = "Open in window"
					}
					editorChooseTreeMenu(t, app, action)
				case "pane picker":
					app.openPaneFilePicker(app.Win.ActiveGroup())
					remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
					_ = app.View()
					if app.FilePicker.FocusedPath() != path {
						t.Fatal("document picker did not select the only existing file")
					}
					_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					runAppCommand(t, app, cmd)
					if app.FilePicker.Mode() != filepickerscreen.ModeOpenFile {
						t.Fatal("pane picker did not retain document intent")
					}
				case "navigation":
					app.openFileAtLine(path, 1)
					_, cmd := app.Update(nil)
					runAppCommand(t, app, cmd)
				}
				remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.EditorState.ActiveView == path })
				if ed := app.EditorState.Editor; ed == nil || ed.Value() != content || ed.DocumentState().DocumentID == "" {
					t.Fatal("entry point did not install the canonical document")
				}
				if !strings.Contains(ansi.Strip(app.View().Content), "EDITOR_OPEN_SURVIVED") {
					t.Fatal("opened document was not painted")
				}
				if route == "context window" && !app.Win.IsFloating(app.Win.ActiveGroup()) {
					t.Fatal("Open in window lost its destination")
				}
				if app.overlayHost.Window(ui.OverlayContextMenu) != nil {
					t.Fatal("activation retained the old context menu")
				}
				result := app.ws.CreateFile(filepath.Join(root, "created.txt"))
				if !result.Succeeded() {
					t.Fatalf("stale checkpoint still disabled file actions: %v", result.Err)
				}
			})
		}
	}
}
