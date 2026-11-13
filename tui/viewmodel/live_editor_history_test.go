package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/shellport"
	"crude/tui/editor"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

func TestLiveEditorEmptyHistoryIsQuietAndKeepsEditing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "history.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	ed := liveTestOpen(t, shell.app, path)
	shell.app.setFocusEditor()
	ed.SetCursorPosition(0, 4)
	for _, key := range []tea.KeyPressMsg{
		{Code: 'z', Mod: tea.ModCtrl}, // empty history
		{Code: 'x', Text: "x"},
		{Code: 'z', Mod: tea.ModCtrl},
		{Code: 'z', Mod: tea.ModCtrl}, // empty again, without discarding redo
		{Code: 'y', Mod: tea.ModCtrl},
		{Code: 'y', Text: "y"},
	} {
		_, cmd := shell.app.Update(key)
		runAppCommand(t, shell.app, cmd)
		liveTestSync(t, shell.app)
	}
	if ed.Value() != "basexy" {
		t.Fatalf("empty Undo blocked subsequent edit/redo: %q", ed.Value())
	}
	for _, notice := range shell.app.ws.Status().Notifications(false) {
		if strings.Contains(strings.ToLower(notice.Summary), "undo") || strings.Contains(notice.Summary, "authors") {
			t.Fatalf("empty Undo generated misleading feedback: %+v", notice)
		}
	}
}

func TestLiveEditorHistoryLogicalGroupsSurvivePacedAndBurstDispatch(t *testing.T) {
	for _, paced := range []bool{true, false} {
		name := "burst"
		if paced {
			name = "paced"
		}
		t.Run(name, func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			path := filepath.Join(root, "history.txt")
			if err := os.WriteFile(path, []byte("    base"), 0600); err != nil {
				t.Fatal(err)
			}
			shell := newRemoteJourneyShell(t, runtime, root)
			ed := liveTestOpen(t, shell.app, path)
			shell.app.activateEditorTab(path)
			shell.app.setFocusEditor()
			ed.SetCursorPosition(0, 8)
			send := func(msg tea.Msg) tea.Cmd {
				_, cmd := shell.app.Update(msg)
				if paced {
					runAppCommand(t, shell.app, cmd)
					liveTestSync(t, shell.app)
					return nil
				}
				return cmd
			}
			var queued []tea.Cmd
			for _, r := range "xy" {
				queued = append(queued, send(tea.KeyPressMsg{Code: r, Text: string(r)}))
			}
			queued = append(queued, send(tea.KeyPressMsg{Code: tea.KeyEnter}))
			queued = append(queued, send(tea.PasteMsg{Content: "AB\nCD"}))
			for _, cmd := range queued {
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
			}
			runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
			liveTestSync(t, shell.app)
			if ed.Value() != "    basexy\n    AB\nCD" {
				t.Fatalf("dispatch did not edit: %q focus=%v", ed.Value(), shell.app.focusTarget())
			}
			expected := []string{"    basexy\n    ", "    basexy", "    base"}
			for _, want := range expected {
				_, cmd := shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
				if ed.Value() != want {
					t.Fatalf("undo boundary: got %q want %q", ed.Value(), want)
				}
			}
			for _, want := range []string{"    basexy", "    basexy\n    ", "    basexy\n    AB\nCD"} {
				_, cmd := shell.app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
				if ed.Value() != want {
					t.Fatalf("redo boundary: got %q want %q", ed.Value(), want)
				}
			}
			if state := shell.app.liveEditors[ed]; state.err != nil {
				t.Fatal(state.err)
			}
		})
	}
}

func TestLiveEditorHistoryDismissesStaleDiagnosticPopup(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		grantSemanticFixture(t, w)
	})
	root := t.TempDir()
	path := filepath.Join(root, "diagnostic.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	shell.app.activateEditorTab(path)
	shell.app.setFocusEditor()
	_, cmd := shell.app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 0, EndLine: 0, EndCol: 5, Severity: editor.DiagError, Message: "stale diagnostic"}}))
	ed.RefreshCursorHover(0, 0)
	shell.app.updateDiagHoverPopupFromCursor()
	if !shell.app.overlayHost.IsOpen("diag-hover") {
		t.Fatal("diagnostic popup not opened")
	}
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "base" || shell.app.overlayHost.IsOpen("diag-hover") {
		t.Fatalf("stale popup survived undo: %q %t", ed.Value(), shell.app.overlayHost.IsOpen("diag-hover"))
	}
}

func TestLiveEditorHistoryDelayedAcknowledgementPreservesGesturesAndTyping(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "delayed.txt")
	if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	shell.app.activateEditorTab(path)
	shell.app.setFocusEditor()
	_, held := shell.app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	// The first command has been admitted but its acknowledgement is delayed.
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.PasteMsg{Content: "AB"},
		tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'q', Text: "q"},
	} {
		shell.app.Update(msg)
	}
	if ed.Value() != "x\nABbase" {
		t.Fatalf("post-undo typing was not fenced: %q", ed.Value())
	}
	runAppCommand(t, shell.app, held)
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	liveTestSync(t, shell.app)
	if ed.Value() != "x\nqbase" || ed.CursorLine() != 1 || ed.CursorCol() != 1 {
		t.Fatalf("ordered undo/redo/typing: %q cursor=%d:%d state=%+v", ed.Value(), ed.CursorLine(), ed.CursorCol(), ed.DocumentState())
	}
	state := shell.app.liveEditors[ed]
	if state.err != nil || ed.DocumentState().HistoryQueued || ed.DocumentState().UndoPending {
		t.Fatalf("history did not settle: %+v %v", ed.DocumentState(), state.err)
	}
	synced, err := shell.app.ws.(shellport.LiveDocuments).SyncDocument(t.Context(), ed.DocumentState().DocumentID, 0)
	if err != nil || string(synced.Snapshot.Text) != "x\nqbase" {
		t.Fatalf("canonical order: %q %v", synced.Snapshot.Text, err)
	}
}

func TestLiveEditorHistoryCombiningAppendUsesExactSourceOffsets(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "combining.txt")
	if err := os.WriteFile(path, []byte("e"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	shell.app.activateEditorTab(path)
	shell.app.setFocusEditor()
	ed.SetCursorPosition(0, 1)
	_, cmd := shell.app.Update(tea.KeyPressMsg{Code: '\u0301', Text: "\u0301"})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "e\u0301" || ed.CursorCol() != 1 {
		t.Fatalf("append: %q cursor=%d", ed.Value(), ed.CursorCol())
	}
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "e" || ed.CursorCol() != 1 {
		t.Fatalf("undo removed base grapheme: %q cursor=%d", ed.Value(), ed.CursorCol())
	}
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "e\u0301" || ed.CursorCol() != 1 {
		t.Fatalf("redo source bytes: %q cursor=%d", ed.Value(), ed.CursorCol())
	}
}

func TestLiveEditorHistoryRestoresDeletedSelectionAndCaret(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "selection.txt")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	shell.app.activateEditorTab(path)
	shell.app.setFocusEditor()
	ed.SetCursorPosition(0, 3)
	ed.SetSelection(0, 1, 0, 3)
	_, cmd := shell.app.Update(tea.PasteMsg{Content: "X"})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "aX" || ed.HasSelection() {
		t.Fatalf("selection replacement: %q", ed.Value())
	}
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "abc" || ed.SelectedText() != "bc" || ed.CursorCol() != 3 {
		t.Fatalf("undo view: %q selected=%q cursor=%d", ed.Value(), ed.SelectedText(), ed.CursorCol())
	}
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	runAppCommand(t, shell.app, cmd)
	liveTestSync(t, shell.app)
	if ed.Value() != "aX" || ed.HasSelection() || ed.CursorCol() != 2 {
		t.Fatalf("redo view: %q selected=%q cursor=%d", ed.Value(), ed.SelectedText(), ed.CursorCol())
	}
}

// VS Code's undo stops survive the core round trip: a word, a space-led word
// and a run of Backspace are three steps whatever the dispatch pacing.
func TestLiveEditorHistoryFollowsVSCodeUndoStops(t *testing.T) {
	for _, paced := range []bool{true, false} {
		t.Run(map[bool]string{true: "paced", false: "burst"}[paced], func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			path := filepath.Join(root, "stops.txt")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			shell := newRemoteJourneyShell(t, runtime, root)
			ed := liveTestOpen(t, shell.app, path)
			shell.app.activateEditorTab(path)
			shell.app.setFocusEditor()
			var queued []tea.Cmd
			send := func(msg tea.Msg) {
				_, cmd := shell.app.Update(msg)
				if paced {
					runAppCommand(t, shell.app, cmd)
					liveTestSync(t, shell.app)
					return
				}
				queued = append(queued, cmd)
			}
			for _, r := range "ab cd" {
				send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			send(tea.KeyPressMsg{Code: tea.KeyBackspace})
			send(tea.KeyPressMsg{Code: tea.KeyBackspace})
			for _, cmd := range queued {
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
			}
			runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
			liveTestSync(t, shell.app)
			if ed.Value() != "ab " {
				t.Fatalf("typing produced %q", ed.Value())
			}
			for _, want := range []string{"ab cd", "ab", ""} {
				_, cmd := shell.app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
				if ed.Value() != want {
					t.Fatalf("undo step: got %q want %q", ed.Value(), want)
				}
			}
			for _, want := range []string{"ab", "ab cd", "ab "} {
				_, cmd := shell.app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
				runAppCommand(t, shell.app, cmd)
				liveTestSync(t, shell.app)
				if ed.Value() != want {
					t.Fatalf("redo step: got %q want %q", ed.Value(), want)
				}
			}
		})
	}
}
