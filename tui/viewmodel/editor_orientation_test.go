package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"fmt"
	"os"
	"strings"
	"testing"
)

func openOrientationSettings(app *App) (*settingsui.WorkspaceSettingsSurface, error) {
	app.openWorkspaceSettings()
	body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	for index, category := range body.Model().Categories() {
		if category.Name == "Editor" {
			body.Model().SelectCategory(index)
			body.SetModel(body.Model())
		}
	}
	body.FocusForm()
	found := false
	for _, child := range body.Children() {
		if f, ok := child.(*ui.Form); ok && f.ID() == settingsui.WorkspaceSettingsFormID {
			found = f.FocusField("indent-guides")
		}
	}
	if !found {
		return nil, fmt.Errorf("existing Editor page has no indentation-guide preference")
	}
	return body, nil
}
func orientationInk(app *App, ed *editor.Editor) (guide bool, mapX int) {
	app.View()
	r := app.editorRect
	x, y := r.X+ed.PadL+ed.GutterWidth(), r.Y
	guide = app.mainSurf.Buffer().Get(x, y).Char == '╎'
	mapX = -1
	for col := r.X + r.W - 1; col >= x; col-- {
		for row := r.Y; row < r.Y+r.H; row++ {
			c := app.mainSurf.Buffer().Get(col, row).Char
			if c > 0x2800 && c <= 0x28ff {
				mapX = col
				return
			}
		}
	}
	return
}
func TestEditorOrientationPreferencesSavePeerResetAndPointer(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			root := t.TempDir()
			source := strings.Repeat("    alpha 界é = value;\r\n", 400)
			path := writePaneFile(t, root, "orientation.txt", source)
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				s := w.AppDefaults()
				s.LSP.Enabled = false
				s.Editor.LineNumbers = true
				s.Editor.IndentGuides = false
				s.Editor.Minimap = false
				w.SetAppDefaults(s)
				restrictFixture(t, w)
			})
			shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
			for _, shell := range shells {
				liveTestOpen(t, shell.app, path)
				shell.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				result := requestShellForTest(t, runtime, shells, shell, "tree.set_visible", map[string]any{"visible": false}, nil)
				if result.State != control.Applied {
					t.Fatal(result)
				}
			}
			app, ed := shells[0].app, shells[0].app.EditorState.Editor
			ed.SetSelection(0, 4, 0, 9)
			revision := ed.Revision()
			linked := ed.NewLinkedView()
			defer linked.Close()
			if g, x := orientationInk(app, ed); g || x >= 0 {
				t.Fatal("disabled display is visible")
			}
			body, err := openOrientationSettings(app)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []tea.KeyPressMsg{{Code: ' ', Text: " "}, {Code: tea.KeyTab}, {Code: ' ', Text: " "}} {
				renameConnectedKey(t, shells[0], key)
			}
			if app.editorSettings.IndentGuides || app.editorSettings.Minimap {
				t.Fatal("unsaved settings reached source")
			}
			body.FocusActions()
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyEnter})
			remoteJourneyUntil(t, shells, func() bool { return shells[1].app.editorSettings.IndentGuides && shells[1].app.editorSettings.Minimap })
			body.FocusActions()
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyRight})
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyEnter})
			app.setFocusEditor()
			g, x := orientationInk(app, ed)
			if !g || x < 0 {
				t.Fatal("saved orientation preferences do not paint", g, x)
			}
			if g, x := orientationInk(shells[1].app, shells[1].app.EditorState.Editor); !g || x < 0 {
				t.Fatal("peer view missed live settings")
			}
			r := app.editorRect
			_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: r.Y + r.H/2, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			_, cmd = app.Update(tea.MouseReleaseMsg{X: x, Y: r.Y + r.H/2, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			if ed.ScrollModel().Offset() < 150 || app.rootHost.HasPointerCapture() || ed.Value() != source || ed.SelectedText() != "alpha" || ed.Revision() != revision {
				t.Fatal("native minimap hit lost viewport/source/selection")
			}
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
			remoteJourneyUntil(t, shells, func() bool {
				return strings.HasPrefix(ed.Value(), "    X 界") && shells[1].app.EditorState.Editor.Value() == ed.Value()
			})
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == source })
			runAppCommand(t, app, app.saveSettingsShortcut(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "indent_guides"}}, {Path: []string{"editor", "minimap"}}}, "Editor settings reset.", nil, 0))
			remoteJourneyUntil(t, shells, func() bool {
				return !shells[1].app.editorSettings.IndentGuides && !shells[1].app.editorSettings.Minimap
			})
			if g, x := orientationInk(app, ed); g || x >= 0 {
				t.Fatal("Reset did not restore inherited presentation")
			}
			if ed.SelectedText() != "alpha" || ed.Modified() {
				t.Fatal("display settings changed Undo state")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != source {
				t.Fatal("presentation saved the file", err)
			}
		})
	}
}
