package viewmodel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type typingCompletionCall struct {
	text      string
	line, col int
}

type typingCompletionRuntime struct {
	base  renameConnectedRuntime
	calls []typingCompletionCall // guarded by base.mu
}

type typingCompletionClient struct {
	*renameConnectedClient
	owner *typingCompletionRuntime
}

func (r *typingCompletionRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &typingCompletionClient{r.base.NewClient(spec, root, admit).(*renameConnectedClient), r}
}

func (c *typingCompletionClient) RequestCompletions(_ context.Context, path string, line, col int) (types.LSPCompletionResponse, error) {
	r := c.owner
	r.base.mu.Lock()
	defer r.base.mu.Unlock()
	d := c.docs[path]
	r.calls = append(r.calls, typingCompletionCall{d.text, line, col})
	// Like many real servers, return candidates and their filter/ranking data;
	// the client must narrow the list, not guess edits from labels.
	raw := `[
		{"label":"printValue","kind":3,"sortText":"2","insertText":"printValue"},
		{"label":"privateValue","kind":6,"sortText":"1","insertText":"privateValue"},
		{"label":"run","kind":2,"insertText":"run"},
		{"label":"reset","kind":2,"insertText":"reset"}
	]`
	if strings.HasPrefix(d.text, "device.") {
		raw = `[{"label":"run","kind":2,"sortText":"1"},{"label":"reset","kind":2,"sortText":"2"}]`
	}
	return types.LSPCompletionResponse{WireVersion: d.version, JSON: []byte(raw)}, nil
}

func newTypingCompletionFixture(t *testing.T, initial string) ([]remoteJourneyShell, []*editor.Editor, *workspace.Workspace, *typingCompletionRuntime, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	for name, text := range map[string]string{path: initial, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	engine := &typingCompletionRuntime{}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect = false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable, RootMarkers: []string{"go.mod"}}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
	eds := []*editor.Editor{liveTestOpen(t, shells[0].app, path), liveTestOpen(t, shells[1].app, path)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, shells, func() bool {
		_, ready := eds[0].CurrentInsightSource()
		return ready && shells[0].app.ws.LSPMgr().ServerReady()
	})
	shells[0].app.setFocusEditor()
	shells[0].app.Input.SetInput("private draft")
	eds[0].SetCursorPosition(0, editor.LineVisualWidth(initial, editor.DefaultTabWidth))
	return shells, eds, owned, engine, path
}

func TestConnectedTypingOffersNamesAndMembersAndTabAccepts(t *testing.T) {
	for _, tc := range []struct{ name, initial, typed, want string }{
		{"name", "pr", "i", "privateValue"},
		{"member", "device", ".", "device.run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shells, eds, _, engine, path := newTypingCompletionFixture(t, tc.initial)
			app := shells[0].app
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: []rune(tc.typed)[0], Text: tc.typed})
			remoteJourneyUntil(t, shells, func() bool {
				runAppCommand(t, app, app.autoCompletionCmd(time.Now()))
				return app.completion != nil && app.completion.screen != nil
			})
			engine.base.mu.Lock()
			calls := append([]typingCompletionCall(nil), engine.calls...)
			engine.base.mu.Unlock()
			if len(calls) != 1 || calls[0].text != tc.initial+tc.typed || calls[0].col != len(tc.initial+tc.typed) {
				t.Fatalf("typing did not query the exact synchronized source once: %+v", calls)
			}
			if tc.name == "name" {
				completionRequireText(t, app, []string{"privateValue", "printValue"}, []string{" reset", " run"})
			}
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
			remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == tc.want && eds[1].Value() == tc.want })
			if app.completion != nil || shells[1].app.completion != nil || app.Input.Value() != "private draft" {
				t.Fatal("acceptance reopened suggestions or changed another input owner")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != tc.initial {
				t.Fatalf("completion must remain an unsaved editor change: %q %v", data, err)
			}
		})
	}
}

func TestConnectedCompletionSettingsApplyWithoutRestartAndPreserveManualRequest(t *testing.T) {
	shells, eds, _, engine, _ := newTypingCompletionFixture(t, "pr")
	app := shells[0].app
	app.openWorkspaceSettings()
	body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	for index, category := range body.Model().Categories() {
		if category.Name == "Code Intelligence" {
			body.Model().SelectCategory(index)
			body.SetModel(body.Model())
		}
	}
	body.FocusForm()
	var form *ui.Form
	for _, child := range body.Children() {
		if candidate, ok := child.(*ui.Form); ok && candidate.ID() == settingsui.WorkspaceSettingsFormID {
			form = candidate
		}
	}
	if form == nil || !form.FocusField("auto-complete") {
		t.Fatal("completion preference is not reachable in Settings")
	}
	app.paintShellFrame()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: ' ', Text: " "})
	if !form.FocusField("completion-delay") {
		t.Fatal("suggestion delay is not reachable")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: '0', Text: "0"})
	form.FocusField("completion-accept")
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyLeft})
	body.FocusActions()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyEnter})
	if body.Model().SaveError() != "" || len(body.Model().Edits()) != 0 {
		t.Fatalf("completion settings did not save: %s", body.Model().SaveError())
	}
	remoteJourneyUntil(t, shells, func() bool {
		settings := shells[1].app.ws.LSPMgr().Settings()
		return !settings.AutoComplete && settings.CompletionDelay() == 0 && settings.CompletionAcceptance() == "tab"
	})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyEsc})
	app.setFocusEditor()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool {
		_, current := eds[0].CurrentInsightSource()
		return current && eds[1].Value() == "pri"
	})
	engine.base.mu.Lock()
	requests, clients := len(engine.calls), len(engine.base.clients)
	engine.base.mu.Unlock()
	if requests != 0 || clients != 1 || app.completion != nil || app.completionIntent != nil {
		t.Fatal("saving Off still requested suggestions or restarted the language server", requests, clients)
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return app.completion != nil && app.completion.screen != nil })
	completionRequireText(t, app, []string{"Tab: accept", "privateValue"}, nil)
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == "privateValue" && eds[1].Value() == "privateValue" })
}

func TestConnectedSuggestionSettingsSaveByPointer(t *testing.T) {
	shells, _, _, _, _ := newTypingCompletionFixture(t, "pr")
	app := shells[0].app
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.openWorkspaceSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	selectSettingsCategory(t, body, "Code Intelligence")
	body.FocusForm()
	var form *ui.Form
	for _, child := range body.Children() {
		if candidate, ok := child.(*ui.Form); ok && candidate.ID() == settingsui.WorkspaceSettingsFormID {
			form = candidate
		}
	}
	if form == nil || !form.FocusField("auto-complete") {
		t.Fatal("automatic suggestions setting is not reachable")
	}
	app.paintShellFrame()
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: ' ', Text: " "})
	col, row := settingsTextCell(t, parent, "[ Save ]")
	x, y := parent.Position()
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: x + col + 3, Y: y + row},
		tea.MouseClickMsg{X: x + col + 3, Y: y + row, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: x + col + 3, Y: y + row, Button: tea.MouseLeft},
	} {
		_, cmd := app.Update(msg)
		runAppCommand(t, app, cmd)
	}
	if body.Model().SaveError() != "" || len(body.Model().Edits()) != 0 {
		t.Fatalf("clicking Save did not save the suggestion setting: error=%q edits=%+v",
			body.Model().SaveError(), body.Model().Edits())
	}
	remoteJourneyUntil(t, shells, func() bool { return !shells[1].app.ws.LSPMgr().Settings().AutoComplete })
}

func TestConnectedCompletionNarrowsAfterMoreTypingAndBackspace(t *testing.T) {
	shells, eds, _, _, _ := newTypingCompletionFixture(t, "pr")
	app := shells[0].app
	for _, key := range []tea.KeyPressMsg{
		{Code: 'i', Text: "i"}, {Code: 'n', Text: "n"}, {Code: tea.KeyBackspace},
	} {
		renameConnectedKey(t, shells[0], key)
		remoteJourneyUntil(t, shells, func() bool { return app.completion != nil && app.completion.screen != nil })
		if eds[0].Value() == "prin" {
			completionRequireText(t, app, []string{"printValue"}, []string{"privateValue"})
		} else {
			completionRequireText(t, app, []string{"printValue", "privateValue"}, nil)
		}
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyDown})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == "printValue" && eds[1].Value() == "printValue" })
}

func TestConnectedCompletionPeerEditRetiresTheOldInsertion(t *testing.T) {
	shells, eds, _, _, _ := newTypingCompletionFixture(t, "pr")
	app := shells[0].app
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool { return app.completion != nil && app.completion.screen != nil })
	shells[1].app.setFocusEditor()
	eds[1].SetCursorPosition(0, 0)
	renameConnectedKey(t, shells[1], tea.KeyPressMsg{Code: 'x', Text: "x"})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == "xpri" && app.completion == nil })
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == "xpri    " && eds[1].Value() == "xpri    " })
	if app.Input.Value() != "private draft" {
		t.Fatal("peer invalidation changed the private input draft")
	}
}

func TestConnectedAutomaticCompletionKeepsShortSuggestionsCompact(t *testing.T) {
	shells, eds, _, _, _ := newTypingCompletionFixture(t, "pr")
	app := shells[0].app
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'i', Text: "i"})
	remoteJourneyUntil(t, shells, func() bool { return app.completion != nil && app.completion.screen != nil })

	for _, size := range [][2]int{{160, 40}, {80, 24}, {160, 40}} {
		app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		app.View()
		fw := app.overlayHost.Window(ui.OverlayCompletion)
		if fw == nil {
			t.Fatal("resizing closed the current suggestions")
		}
		w, h := fw.Size()
		if w > 76 || h > 10 {
			t.Fatalf("%dx%d: two short suggestions obscure the editor with a %dx%d chooser",
				size[0], size[1], w, h)
		}
		completionRequireText(t, app, []string{"privateValue", "printValue", "No documentation available.", "Tab"}, nil)
		if eds[0].Value() != "pri" || app.Input.Value() != "private draft" {
			t.Fatal("showing or resizing suggestions changed the source or private draft")
		}
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, shells, func() bool { return eds[0].Value() == "privateValue" && eds[1].Value() == "privateValue" })
}
