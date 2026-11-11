package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorActionsConnectedKeysPickerMenuControlAndUndo(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "actions.txt")
			if err := os.WriteFile(path, []byte("one\ntwo"), 0600); err != nil {
				t.Fatal(err)
			}
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
			app := one.app
			ed := liveTestOpen(t, app, path)
			peer := liveTestOpen(t, two.app, path)
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			app.setFocusEditor()
			app.Input.SetInput("private draft")
			sync := func(want string) {
				t.Helper()
				runAppCommand(t, app, app.drainLiveEditorCommands())
				liveTestPump(t, []*App{app, two.app}, nil)
				if ed.Value() != want || peer.Value() != want || ed.DocumentState().Conflict != nil {
					t.Fatalf("shared command differs: %q / %q want %q %+v", ed.Value(), peer.Value(), want, ed.DocumentState())
				}
			}
			key := func(k tea.KeyPressMsg) { t.Helper(); _, cmd := app.Update(k); runAppCommand(t, app, cmd) }
			ed.SetCursorPosition(0, 1)
			key(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
			sync("one\none\ntwo")
			key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			sync("one\ntwo")
			key(tea.KeyPressMsg{Code: '/', Mod: tea.ModAlt})
			if app.commandPaletteModel == nil {
				t.Fatal("editor actions shortcut unavailable")
			}
			if text := frameText(app); !strings.Contains(text, "Editor actions") || !strings.Contains(text, "Duplicate line") {
				t.Fatal(text)
			}
			for _, r := range "join lines" {
				key(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			if app.commandPaletteModel.filterText() != "join lines" || len(app.commandPaletteModel.filtered) != 1 || app.commandPaletteModel.filtered[0].ID != "edit.joinLines" {
				t.Fatal("native text did not filter editor actions", app.commandPaletteModel.filterText())
			}
			key(tea.KeyPressMsg{Code: tea.KeyEnter})
			sync("one two")
			key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			sync("one\ntwo")
			menu := app.buildEditorContextMenu(tea.Mouse{X: -1, Y: -1})
			found := false
			for _, section := range menu.Sections {
				for _, item := range section.Items {
					if item.Label == "Duplicate line or selection" {
						if item.Detail != "Ctrl+D" || item.Disabled {
							t.Fatal(item)
						}
						item.Action()
						found = true
					}
				}
			}
			if !found {
				t.Fatal("native action absent from editor menu")
			}
			sync("one\none\ntwo")
			key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			sync("one\ntwo")
			inspected := productRequest(t, app, "editor.actions", ed.DocumentState().DocumentID, "", map[string]any{}, nil)
			var out struct {
				Token string `json:"view_token"`
			}
			if inspected.State != control.Applied || json.Unmarshal(inspected.Output, &out) != nil || len(out.Token) != 64 {
				t.Fatal(inspected)
			}
			action := map[string]any{"action_id": "edit.duplicateLine", "view_token": out.Token}
			applied := productRequest(t, app, "editor.action", ed.DocumentState().DocumentID, "", action, nil)
			if applied.State != control.Admitted {
				t.Fatal(applied)
			}
			sync("one\none\ntwo")
			stale := productRequest(t, app, "editor.action", ed.DocumentState().DocumentID, "", action, nil)
			if stale.State != control.Failed || stale.Fault.Code != control.Stale {
				t.Fatal("old view token replayed", stale)
			}
			key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			sync("one\ntwo")
			if app.Input.Value() != "private draft" || app.focusTarget() != FocusEditor {
				t.Fatal("editor action changed composer or focus")
			}
			app.openEditorActions()
			old := app.commandPaletteModel.callback
			ed.SetCursorPosition(1, 0)
			runAppCommand(t, app, old("edit.duplicateLine"))
			sync("one\ntwo")
			// Read-only editors still offer selection/motion, not editing.
			preview := editor.NewPreview(app.Theme, path, "word", 1)
			t.Cleanup(preview.Close)
			app.EditorState.Editor = preview
			app.openEditorActions()
			for _, item := range app.commandPaletteModel.items {
				a, ok := ui.FindEditorAction(ui.ActionID(item.ID))
				if !ok || a.MutatesText {
					t.Fatal("readonly command", item)
				}
			}
			app.overlayHost.Close(ui.OverlayCommandPalette)
		})
	}
}

func TestEditorStructuralActionsUseSharedNativePublication(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "structure.go")
	source := "package p\nfunc f() {\n    result := wrap(arg + 1)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
	for _, shell := range shells {
		liveTestOpen(t, shell.app, path)
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			state, _ := shell.app.EditorState.Editor.InsightStatus()
			if state != types.InsightCurrent {
				return false
			}
		}
		return true
	})
	for _, shell := range shells {
		app := shell.app
		ed := app.EditorState.Editor
		app.setFocusEditor()
		ed.SetCursorPosition(2, 20)
		renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
		if ed.SelectedText() != "arg" {
			t.Fatal(ed.SelectedText())
		}
		renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
		if ed.SelectedText() != "arg + 1" {
			t.Fatal("canonical structure did not cross the wire", ed.SelectedText())
		}
		ed.SetCursorPosition(2, 18)
		renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt})
		if ed.CursorCol() != 26 || ed.Value() != source || ed.Modified() {
			t.Fatal("matching bracket changed source or missed canonical endpoint", ed.CursorCol(), ed.Value())
		}
	}
}
