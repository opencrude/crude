package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorPresentationSettingsReachOpenLinkedAndNewViews(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "paint.go")
			source := "package p\n\tvar name = \"é界\"  \r\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				settings := w.AppDefaults()
				settings.LSP.Enabled = false
				settings.Editor.LineNumbers = true
				w.SetAppDefaults(settings)
				restrictFixture(t, w)
			})
			shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
			for _, shell := range shells {
				liveTestOpen(t, shell.app, path)
				shell.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			}
			app := shells[0].app
			ed := app.EditorState.Editor
			remoteJourneyUntil(t, shells, func() bool { state, _ := ed.InsightStatus(); return state == types.InsightCurrent })
			app.View()
			ed.SetSelection(1, 8, 1, 12)
			selection := ed.SelectedText()
			revision := ed.Revision()
			linked := ed.NewLinkedView()
			t.Cleanup(linked.Close)
			beforeWidth := ed.GutterWidth()
			ink := func() cell.Color {
				app.View()
				r := app.editorRect
				painted := app.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+1, r.Y)
				if painted.Char != 'a' {
					t.Fatalf("color sample is not the source keyword: %+v", painted)
				}
				return painted.FG
			}
			beforeInk := ink()
			app.openWorkspaceSettings()
			body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
			for index, category := range body.Model().Categories() {
				if category.Name == "Editor" {
					body.Model().SelectCategory(index)
					body.SetModel(body.Model())
				}
			}
			body.FocusForm()
			var form *ui.Form
			for _, child := range body.Children() {
				if f, ok := child.(*ui.Form); ok && f.ID() == settingsui.WorkspaceSettingsFormID {
					form = f
				}
			}
			if form == nil {
				t.Fatal("Editor preferences unavailable")
			}
			key := func(key tea.KeyPressMsg) { renameConnectedKey(t, shells[0], key) }
			for _, edit := range []struct {
				id    string
				key   tea.KeyPressMsg
				count int
			}{
				{"line-numbers", tea.KeyPressMsg{Code: ' ', Text: " "}, 1},
				{"syntax-theme", tea.KeyPressMsg{Code: tea.KeyRight}, 1},
				{"render-whitespace", tea.KeyPressMsg{Code: tea.KeyRight}, 2},
			} {
				if !form.FocusField(edit.id) {
					t.Fatal("missing preference", edit.id)
				}
				for range edit.count {
					key(edit.key)
				}
			}
			body.FocusActions()
			key(tea.KeyPressMsg{Code: tea.KeyEnter})
			if body.Model().SaveError() != "" || len(body.Model().Edits()) != 0 {
				t.Fatal("preferences did not save", body.Model().SaveError())
			}
			remoteJourneyUntil(t, shells, func() bool { return shells[1].app.editorSettings.SyntaxTheme == "dracula" })
			body.FocusActions()
			key(tea.KeyPressMsg{Code: tea.KeyRight})
			key(tea.KeyPressMsg{Code: tea.KeyEnter}) // Select the adjacent Close button.
			if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
				t.Fatal("native Close left Settings open")
			}
			app.setFocusEditor()
			if ed.GutterWidth() >= beforeWidth || linked.GutterWidth() != ed.GutterWidth() || shells[1].app.EditorState.Editor.GutterWidth() != ed.GutterWidth() {
				t.Fatalf("saved line-number preference did not reach current views: before=%d current=%d linked=%d peer=%d settings=%+v", beforeWidth, ed.GutterWidth(), linked.GutterWidth(), shells[1].app.EditorState.Editor.GutterWidth(), app.editorSettings)
			}
			if after := ink(); after.Equal(beforeInk) {
				t.Fatal("saved syntax colors did not change source ink")
			}
			r := app.editorRect
			x, y := r.X+ed.PadL+ed.GutterWidth(), r.Y+1
			if got := app.mainSurf.Buffer().Get(x, y).Char; got != '→' {
				t.Fatal("tab marker absent", got)
			}
			if got := app.mainSurf.Buffer().Get(x+7, y).Char; got != '·' {
				t.Fatal("internal space marker absent", got)
			}
			if ed.Value() != source || ed.Revision() != revision || ed.SelectedText() != selection || ed.Modified() {
				t.Fatal("display preferences changed document state")
			}
			nextPath := filepath.Join(root, "next.go")
			if err := os.WriteFile(nextPath, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			next := liveTestOpen(t, app, nextPath)
			if next.GutterWidth() != ed.GutterWidth() {
				t.Fatal("new view ignored saved line-number preference")
			}
			edits := []types.SettingEdit{{Path: []string{"editor", "line_numbers"}}, {Path: []string{"editor", "syntax_theme"}}, {Path: []string{"editor", "render_whitespace"}}}
			runAppCommand(t, app, app.saveSettingsShortcut(types.SettingsScopeWorkspace, edits, "Editor settings reset.", nil, 0))
			remoteJourneyUntil(t, shells, func() bool {
				return shells[1].app.editorSettings.LineNumbers && shells[1].app.editorSettings.RenderWhitespace == "none"
			})
			if ed.GutterWidth() != beforeWidth || linked.GutterWidth() != beforeWidth || next.GutterWidth() != beforeWidth || ed.SelectedText() != selection {
				t.Fatal("reset did not restore inherited presentation and selection")
			}
			app.activateEditorTab(path)
			app.View()
			if !ink().Equal(beforeInk) {
				t.Fatal("palette reset did not restore inherited source ink")
			}
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'Z', Text: "Z"})
			remoteJourneyUntil(t, shells, func() bool {
				return ed.Value() == strings.Replace(source, "name", "Z", 1) && shells[1].app.EditorState.Editor.Value() == ed.Value()
			})
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == source && shells[1].app.EditorState.Editor.Value() == source })
			if data, err := os.ReadFile(path); err != nil || string(data) != source {
				t.Fatal("display preferences wrote disk", err)
			}
		})
	}
}
