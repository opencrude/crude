package viewmodel

import (
	"context"
	"crude/clientstate"
	"crude/indentation"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/shellheader"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/workspace"
)

func TestEditorIndentationReadsProjectConfigBeforeNativeTab(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte("root = true\n[*]\nindent_style = tab\nindent_size = tab\ntab_width = 8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := writePaneFile(t, root, "policy.txt", "\tword\n")
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP.Enabled = false
		w.SetAppDefaults(settings)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.TabWidth() == 8 })
	ed.SetCursorPosition(1, 0)
	shell.app.setFocusEditor()
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !ed.DocumentState().InFlight && ed.DocumentState().PendingEdits == 0 })
	if ed.Value() != "\tword\n\t" || ed.CursorCol() != 8 {
		t.Fatal("native Tab ignored project indentation", ed.Value(), ed.CursorCol())
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == "\tword\n" })
	if data, err := os.ReadFile(path); err != nil || string(data) != "\tword\n" {
		t.Fatal("reading EditorConfig changed source on disk", err)
	}
}

func chooseIndentationMenu(t *testing.T, shell remoteJourneyShell, label string) {
	t.Helper()
	menu := shell.app.LastContextMenu
	if menu == nil {
		t.Fatal("native menu missing")
	}
	index, found := 0, false
	for _, sec := range menu.Sections {
		for _, item := range sec.Items {
			if item.Disabled {
				continue
			}
			if item.Label == label {
				found = true
				break
			}
			index++
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("native menu item missing", label)
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyHome})
	for range index {
		renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEnter})
}
func TestEditorIndentationNativeChoicesControlAndPrivateResume(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			root := t.TempDir()
			path := writePaneFile(t, root, "policy.txt", "\tword\r\n  other\r\n")
			config := filepath.Join(root, ".editorconfig")
			if err := os.WriteFile(config, []byte("root=true\n[*]\nindent_style=space\nindent_size=2\ntab_width=8\n"), 0600); err != nil {
				t.Fatal(err)
			}
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
				settings.LSP.Enabled = false
				w.SetAppDefaults(settings)
				restrictFixture(t, w)
			})
			one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
			profile := attachViewMemoryProfile(t, store, one, "indent-client")
			ed, peer := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
			shells := []remoteJourneyShell{one, two}
			remoteJourneyUntil(t, shells, func() bool { return ed.TabWidth() == 8 && peer.TabWidth() == 8 })
			one.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			one.app.setFocusEditor()
			ed.SetCursorPosition(0, 12)
			ed.SetSelection(0, 8, 0, 12)
			open := func() {
				t.Helper()
				_, cmd := one.app.Update(shellheader.ActionMsg{Action: "editor-indentation", TargetID: "editor:" + one.app.EditorState.ActiveView})
				runAppCommand(t, one.app, cmd)
			}
			open()
			if frame := frameText(one.app); !strings.Contains(frame, "EditorConfig") || !strings.Contains(frame, "Indent size") {
				t.Fatal("indentation menu does not explain the active policy", frame)
			}
			chooseIndentationMenu(t, one, "Indent size…")
			typeIndentationWidth(t, one, "3")
			open()
			chooseIndentationMenu(t, one, "Tab width…")
			typeIndentationWidth(t, one, "4")
			if ed.SelectedText() != "word" || ed.TabWidth() != 4 || ed.Indentation().Size != 3 || peer.TabWidth() != 8 || ed.Modified() {
				t.Fatal("private choice moved source or changed peer settings")
			}
			open()
			chooseIndentationMenu(t, one, "Convert to spaces")
			remoteJourneyUntil(t, shells, func() bool { return peer.Value() == "    word\r\n  other\r\n" && ed.Value() == peer.Value() })
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "\tword\r\n  other\r\n" && peer.Value() == ed.Value() })
			inspected := productRequest(t, one.app, "editor.actions", ed.DocumentState().DocumentID, "", map[string]any{}, nil)
			var state struct {
				Token       string              `json:"view_token"`
				Indentation indentation.Choices `json:"indentation"`
			}
			if err := json.Unmarshal(inspected.Output, &state); err != nil || state.Indentation.Size != 3 {
				t.Fatal("control omitted file policy", err, inspected)
			}
			input := map[string]any{"view_token": state.Token, "choices": map[string]any{"style": "tab", "size": 4, "tab_width": 4}}
			applied := productRequest(t, one.app, "editor.indentation", ed.DocumentState().DocumentID, "", input, nil)
			if applied.State != control.Applied || ed.Indentation().Style != indentation.Tabs {
				t.Fatal(applied)
			}
			stale := productRequest(t, one.app, "editor.indentation", ed.DocumentState().DocumentID, "", input, nil)
			if stale.State != control.Failed || stale.Fault.Code != control.Stale {
				t.Fatal("stale policy replayed", stale)
			}
			one.app.noteViewMemoryChange()
			one.app.flushViewMemory()
			one.app.StopAsyncIO()
			if err := store.Release(t.Context(), profile.ClientID, profile.Epoch); err != nil {
				t.Fatal(err)
			}
			one.app.composerDraftClient, one.app.viewMemory = nil, nil
			back := newRemoteJourneyShell(t, runtime, root)
			attachViewMemoryProfile(t, store, back, "indent-client")
			commands := back.app.editorCommands
			back.app.editorCommands = nil
			for _, cmd := range commands {
				runAppCommand(t, back.app, cmd)
			}
			shells = []remoteJourneyShell{two, back}
			remoteJourneyUntil(t, shells, func() bool {
				return back.app.EditorState.Tabs[path] != nil && back.app.EditorState.Tabs[path].Editor.Indentation().Style == indentation.Tabs
			})
			resumed := back.app.EditorState.Tabs[path].Editor
			if resumed.TabWidth() != 4 || resumed.Value() != ed.Value() {
				t.Fatal("profile lost private file choices")
			}
			// File changes arrive through the core's normal file feed. Explicit choices
			// retain priority while the other shell follows the saved project data.
			if err := os.WriteFile(config, []byte("root=true\n[*]\nindent_style=space\nindent_size=5\ntab_width=6\n"), 0600); err != nil {
				t.Fatal(err)
			}
			remoteJourneyUntil(t, shells, func() bool { return peer.TabWidth() == 6 && peer.Indentation().Size == 5 })
			if resumed.TabWidth() != 4 {
				t.Fatal("project refresh overwrote file override")
			}
			back.app.setFocusEditor()
			runAppCommand(t, back.app, back.app.openIndentationMenu(resumed))
			chooseIndentationMenu(t, back, "Use project settings")
			if resumed.TabWidth() != 6 || resumed.Indentation().Size != 5 {
				t.Fatal("Reset did not use refreshed project policy")
			}
			back.app.overlayHost.Close(ui.OverlayContextMenu)
		})
	}
}

type indentationBlockingPort struct {
	shellport.Workspace
	start   chan struct{}
	release chan struct{}
	path    string
}

func (p *indentationBlockingPort) ReadEditorConfig(ctx context.Context, _ string) (string, indentation.Choices, error) {
	close(p.start)
	select {
	case <-p.release:
		return p.path, indentation.Choices{TabWidth: 12}, nil
	case <-ctx.Done():
		return p.path, indentation.Choices{}, ctx.Err()
	}
}
func TestEditorIndentationCancelledReadRetiresBeforeRetry(t *testing.T) {
	root := t.TempDir()
	path := writePaneFile(t, root, "file.txt", "\tword")
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		s := w.AppDefaults()
		s.LSP.Enabled = false
		w.SetAppDefaults(s)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	app := shell.app
	original := app.ws
	defer func() { app.ws = original }()
	port := &indentationBlockingPort{Workspace: original, path: path, start: make(chan struct{}), release: make(chan struct{})}
	app.ws = port
	app.cancelEditorConfiguration()
	cmd := app.editorConfigurationCommand(ed)
	app.invalidateEditorConfiguration()
	if next := app.editorConfigurationCommand(ed); next != nil {
		t.Fatal("cancelled queued read was replaced before retirement")
	}
	app.applyEditorConfiguration(cmd().(editorConfigResult))
	if app.editorConfigRequests[ed] != nil {
		t.Fatal("cancelled before admission retained a loading slot")
	}
	cmd = app.editorConfigurationCommand(ed)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-port.start
	app.invalidateEditorConfiguration()
	result := (<-done).(editorConfigResult)
	app.applyEditorConfiguration(result)
	if ed.TabWidth() == 12 || app.editorConfigRequests[ed] != nil {
		t.Fatal("cancelled result installed or kept its slot")
	}
	// A late successful result cannot overwrite a newer policy or resurrect a view.
	result.err = nil
	result.choices = indentation.Choices{TabWidth: 16}
	app.applyEditorConfiguration(result)
	if ed.TabWidth() == 16 {
		t.Fatal("stale result changed geometry")
	}
}

func TestEditorIndentationResetWinsOverLatePaneRestore(t *testing.T) {
	ed := editor.NewPreview(theme.DefaultDark(), "policy.txt", "\tword", 1)
	defer ed.Close()
	if err := ed.SetIndentationChoices(indentation.Choices{}); err != nil {
		t.Fatal(err)
	}
	ed.RestoreIndentationChoices(indentation.Choices{TabWidth: 8})
	if ed.TabWidth() != 4 {
		t.Fatal("late restore undid explicit Reset")
	}
}

func TestEditorIndentationPreparedOpenAndProjectFailure(t *testing.T) {
	root := t.TempDir()
	path := writePaneFile(t, root, "policy.txt", "\tword\n")
	config := filepath.Join(root, ".editorconfig")
	if err := os.WriteFile(config, []byte("root=true\n[*]\nindent_style=tab\nindent_size=tab\ntab_width=8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		s := w.AppDefaults()
		s.LSP.Enabled = false
		w.SetAppDefaults(s)
		restrictFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	msg := app.openPaneFiles([]string{path}, app.Win.ActiveGroup())().(paneFilesOpenedMsg)
	_ = app.applyPaneFilesOpened(msg) // no follow-up command may be required before typing
	ed := app.EditorState.Editor
	if ed == nil || ed.TabWidth() != 8 || ed.Indentation().Size != 8 {
		t.Fatal("first mounted frame did not use the prepared policy")
	}
	ed.SetCursorPosition(1, 0)
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if ed.Value() != "\tword\n\t" {
		t.Fatal("first Tab used default indentation")
	}
	liveTestSync(t, app)
	if err := os.WriteFile(config, []byte("root=true\n[*]\nindent_size=nope\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app.invalidateEditorConfiguration()
	runAppCommand(t, app, app.editorConfigurationCommand(ed))
	if r := app.editorConfigRequests[ed.Primary()]; r == nil || r.err == nil || ed.TabWidth() != 8 {
		t.Fatal("invalid policy silently replaced the last valid geometry")
	}
	runAppCommand(t, app, app.openIndentationMenu(ed))
	if !strings.Contains(frameText(app), "Reload to retry") {
		t.Fatal("configuration error has no native recovery action")
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEscape})
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	app.invalidateEditorConfiguration()
	runAppCommand(t, app, app.editorConfigurationCommand(ed))
	if ed.TabWidth() != 4 {
		t.Fatal("removing project config failed to restore inheritance")
	}
}

func typeIndentationWidth(t *testing.T, shell remoteJourneyShell, width string) {
	t.Helper()
	if shell.app.TextPrompt == nil {
		t.Fatal("width prompt missing")
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	for _, r := range width {
		renameConnectedKey(t, shell, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEnter})
}
func TestEditorIndentationWidthPromptRejectsBadInputAndStaleOwner(t *testing.T) {
	root := t.TempDir()
	path := writePaneFile(t, root, "file.txt", "word")
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) { restrictFixture(t, w) })
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	runAppCommand(t, shell.app, shell.app.openIndentationWidth(ed, true))
	for _, value := range []string{"0", "33", "nope"} {
		typeIndentationWidth(t, shell, value)
		if shell.app.TextPrompt == nil || shell.app.TextPrompt.Error == "" || ed.TabWidth() != 4 {
			t.Fatal("invalid width changed source geometry")
		}
	}
	ed.SetCursorPosition(0, 1)
	typeIndentationWidth(t, shell, "8")
	if shell.app.TextPrompt == nil || !strings.Contains(shell.app.TextPrompt.Error, "editor changed") || ed.TabWidth() != 4 {
		t.Fatal("stale width prompt changed another view state")
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEscape})
	if shell.app.TextPrompt != nil || ed.Value() != "word" {
		t.Fatal("Cancel did not leave the source unchanged")
	}
}
