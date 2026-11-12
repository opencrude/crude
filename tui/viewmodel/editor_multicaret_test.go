package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/types/control"
	"crude/workspace"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestEditorMultipleNativeJourneyPeerUndoEscapeAndControl(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			root := t.TempDir()
			path := writePaneFile(t, root, "multi.txt", "name\r\nname\r\nname")
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				s := w.AppDefaults()
				s.LSP.Enabled = false
				w.SetAppDefaults(s)
				restrictFixture(t, w)
			})
			one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
			app := one.app
			ed, peer := liveTestOpen(t, app, path), liveTestOpen(t, two.app, path)
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			app.setFocusEditor()
			app.Input.SetInput("Private draft.")
			key := func(k tea.KeyPressMsg) { renameConnectedKey(t, one, k) }
			// Search and select the native action through the actual picker.
			key(tea.KeyPressMsg{Code: '/', Mod: tea.ModAlt})
			for _, r := range "Select all occurrences" {
				key(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			key(tea.KeyPressMsg{Code: tea.KeyEnter})
			if ed.CaretCount() != 3 || peer.CaretCount() != 1 {
				t.Fatal("native picker did not make private multiple selections")
			}
			inspected := productRequest(t, app, "editor.actions", ed.DocumentState().DocumentID, "", map[string]any{}, nil)
			var state struct {
				Token      string                         `json:"view_token"`
				Selections []struct{ Anchor, Cursor int } `json:"selections"`
			}
			if err := json.Unmarshal(inspected.Output, &state); err != nil || len(state.Selections) != 3 {
				t.Fatal("semantic inspection omits multiple source selections", inspected, err)
			}
			key(tea.KeyPressMsg{Code: 'X', Text: "X"})
			shells := []remoteJourneyShell{one, two}
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "X\r\nX\r\nX" && peer.Value() == ed.Value() })
			stale := productRequest(t, app, "editor.action", ed.DocumentState().DocumentID, "", map[string]any{"action_id": "select.singleCaret", "view_token": state.Token}, nil)
			if stale.State != control.Failed || stale.Fault.Code != control.Stale {
				t.Fatal("old multiple selection token replayed", stale)
			}
			key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "name\r\nname\r\nname" && peer.Value() == ed.Value() })
			if ed.CaretCount() != 3 || ed.SelectedText() != "name\r\nname\r\nname" {
				t.Fatal("canonical Undo did not restore every range", ed.SourceSelections())
			}
			key(tea.KeyPressMsg{Code: tea.KeyRight})
			if ed.HasSelection() {
				t.Fatal("Right did not collapse all selections")
			}
			key(tea.KeyPressMsg{Code: tea.KeyEscape})
			if ed.CaretCount() != 1 || !ed.IsOpen() || app.EditorState.Editor != ed {
				t.Fatal("Escape closed the editor instead of removing extra carets")
			}
			key(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "name\r\nname\r\nnameY" && peer.Value() == ed.Value() })
			if !strings.Contains(frameText(app), "nameY") || app.Input.Value() != "Private draft." {
				t.Fatal("native multiple editing changed draft or failed paint")
			}
		})
	}
}
