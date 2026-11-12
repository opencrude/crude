package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/clientstate"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func editorRowsSource() string {
	return "package p\nfunc work() {\n    if true {\n        call(\"" + strings.Repeat("word 界 é ", 24) + "\")\n    }\n}\n"
}

func TestEditorRowsConnectedPrivateFoldsResumePeerEditAndUndo(t *testing.T) {
	root := t.TempDir()
	path := writePaneFile(t, root, "rows.go", editorRowsSource())
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.Editor.WordWrap = true
		settings.Editor.LineNumbers = true
		settings.LSP.Enabled = false
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	left, right := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	profile := attachViewMemoryProfile(t, store, left, "fold-left")
	attachViewMemoryProfile(t, store, right, "fold-right")
	shells := []remoteJourneyShell{left, right}
	ed := liveTestOpen(t, left.app, path)
	other := liveTestOpen(t, right.app, path)
	for _, shell := range shells {
		shell.app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		shell.app.setFocusEditor()
	}
	ready := func() bool {
		for _, shell := range shells {
			shell.app.View()
			runAppCommand(t, shell.app, shell.app.drainEditorSyntaxCommands())
			state, _ := shell.app.EditorState.Editor.InsightStatus()
			if state != types.InsightCurrent || shell.app.EditorState.Editor.RowsDone() != nil {
				return false
			}
		}
		return true
	}
	remoteJourneyUntil(t, shells, ready)
	ed.SetCursorPosition(1, 0)
	renameConnectedKey(t, left, tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
	left.app.View()
	right.app.View()
	if ed.ScrollModel().Content() != 4 || other.ExportFolds() != nil || ed.Value() != other.Value() {
		t.Fatal("folding did not stay private")
	}
	right.app.Input.SetInput("private draft")
	left.app.noteViewMemoryChange()
	left.app.flushViewMemory()
	right.app.noteViewMemoryChange()
	right.app.flushViewMemory()
	left.app.StopAsyncIO()
	if err := store.Release(t.Context(), profile.ClientID, profile.Epoch); err != nil {
		t.Fatal(err)
	}
	left.app.composerDraftClient, left.app.viewMemory = nil, nil
	back := newRemoteJourneyShell(t, runtime, root)
	attachViewMemoryProfile(t, store, back, "fold-left")
	// The real private pane restore schedules retained open commands.
	queued := back.app.editorCommands
	back.app.editorCommands = nil
	for _, cmd := range queued {
		runAppCommand(t, back.app, cmd)
	}
	shells = []remoteJourneyShell{right, back}
	remoteJourneyUntil(t, shells, func() bool {
		if back.app.EditorState.Tabs[path] == nil {
			return false
		}
		back.app.View()
		runAppCommand(t, back.app, back.app.drainEditorSyntaxCommands())
		return back.app.EditorState.Tabs[path].Editor.ScrollModel().Content() == 4
	})
	resumed := back.app.EditorState.Tabs[path].Editor
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("peer=%+v resumed=%+v back queued=%d", other.DocumentState(), resumed.DocumentState(), len(back.app.editorCommands))
		}
	})
	if resumed.ExportFolds() == nil || other.ExportFolds() != nil {
		t.Fatal("profile lost or shared folds")
	}
	// A peer touches hidden source: reveal it rather than retaining an old fold.
	other.SetCursorPosition(3, 8)
	renameConnectedKey(t, right, tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, shells, func() bool { return resumed.Value() == other.Value() && strings.Contains(resumed.Value(), "Xcall") })
	back.app.View()
	if resumed.ExportFolds() != nil {
		t.Fatal("peer edit stayed hidden")
	}
	renameConnectedKey(t, right, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return resumed.Value() == editorRowsSource() && other.Value() == editorRowsSource() })
	if right.app.Input.Value() != "private draft" {
		t.Fatal("editor rows consumed composer draft")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != editorRowsSource() {
		t.Fatal("presentation wrote disk", err)
	}
	// Setting Save/Reset changes current and newly inherited views, not source.
	runAppCommand(t, right.app, right.app.saveSettingsShortcut(types.SettingsScopeWorkspace, []types.SettingEdit{{Path: []string{"editor", "word_wrap"}, After: json.RawMessage(`false`)}}, "Word wrap saved.", nil, 0))
	remoteJourneyUntil(t, shells, func() bool { return !back.app.editorSettings.WordWrap })
	right.app.View()
	if other.ScrollModel().Content() != other.LineCount() {
		t.Fatal("Word wrap off kept continuation rows")
	}
	right.app.openEditorActions()
	if right.app.commandPaletteModel == nil {
		t.Fatal("fold actions picker absent")
	}
	found := false
	for _, item := range right.app.commandPaletteModel.items {
		if item.ID == "view.foldAll" {
			found = true
		}
	}
	if !found {
		t.Fatal("fold action not discoverable")
	}
	right.app.overlayHost.Close(ui.OverlayCommandPalette)
	// Read-only views share the same typed non-mutating action executor.
	preview := editor.NewPreview(right.app.Theme, path, editorRowsSource(), 1)
	defer preview.Close()
	_ = preview.HandleEvent(editor.ActionMsg{ID: "view.unfoldAll"})
}
