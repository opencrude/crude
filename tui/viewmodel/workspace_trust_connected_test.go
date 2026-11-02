package viewmodel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestConnectedWorkspaceTrustStartsServerAndClosesPeerPrompt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := &immediateTreeDiagRuntime{filePath: path}
	authority := make(chan *workspace.LSPManager, 1)
	runtime := remoteJourneyRuntime(t, "", func(w *workspace.Workspace) {
		w.LSPMgr.SetRuntime(server)
		authority <- w.LSPMgr
	})
	one := newRemoteJourneyShell(t, runtime, root)
	manager := <-authority
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	one.app.Update(types.TickMsg{})
	two.app.Update(types.TickMsg{})
	if !one.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) || !two.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("both undecided shells should ask")
	}
	ed := liveTestOpen(t, one.app, path)
	remoteJourneyUntil(t, shells, func() bool { return one.app.liveEditors[ed].topicReady })
	_, _ = one.app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool {
		two.app.Update(types.TickMsg{})
		return !two.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) && two.app.ws.LoadAppSettings().Analysis.Trusted()
	})
	if one.app.ws.LoadWorkspaceOverrides().AnalysisRootIdentity == "" {
		t.Fatal("trusted choice did not bind root identity")
	}
	remoteJourneyUntil(t, shells, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.rootURIs) > 0
	})
	if !manager.AnalysisPolicy().Trusted() {
		t.Fatal("language-server owner did not receive trust")
	}
	three := newRemoteJourneyShell(t, runtime, root)
	three.app.Update(types.TickMsg{})
	if three.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("new shell asked despite the stored decision")
	}
}

func TestConnectedWorkspaceRestrictedChoiceSurvivesNextShell(t *testing.T) {
	root := t.TempDir()
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, root)
	one.app.Update(types.TickMsg{})
	if !one.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("undecided folder did not ask")
	}
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	if got := one.app.ws.LoadAppSettings().Analysis; got.DecisionNeeded() || got.Trusted() {
		t.Fatalf("restricted choice not published: %+v", got)
	}
	stored := one.app.ws.LoadWorkspaceOverrides()
	if string(stored.Analysis) != `{"trust":"restricted"}` || stored.AnalysisRootIdentity != "" {
		t.Fatalf("restricted choice or identity incorrect: %+v", stored)
	}
	two := newRemoteJourneyShell(t, runtime, root)
	two.app.Update(types.TickMsg{})
	if two.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		t.Fatal("new shell asked again after restricted choice")
	}
}

func workspaceTrustPromptPaint(t *testing.T, app *App) (string, []*ui.Button) {
	t.Helper()
	window := app.overlayHost.Window(ui.OverlayWorkspaceTrust)
	if window == nil {
		t.Fatal("Trust prompt is not mounted")
	}
	w, h := window.Size()
	if w > app.width || h > app.height {
		t.Fatalf("Trust prompt exceeds the screen: prompt=%dx%d screen=%dx%d", w, h, app.width, app.height)
	}
	frame := window.Content.(*ui.ComponentWidget)
	frame.Host().Resize(w, h)
	frame.Host().Layout()
	buf := surface.NewCellBuffer(w, h)
	frame.Host().Paint(buf)
	var buttons []*ui.Button
	var visit func(ui.Component)
	visit = func(component ui.Component) {
		if button, ok := component.(*ui.Button); ok {
			if button.Bounds().Empty() {
				t.Fatal("Trust action has no reachable bounds", button.Semantics().Label)
			}
			buttons = append(buttons, button)
		}
		if parent, ok := component.(ui.Parent); ok {
			for _, child := range parent.Children() {
				visit(child)
			}
		}
	}
	visit(frame.Root())
	if len(buttons) != 2 {
		t.Fatalf("Trust prompt has %d actions, want two", len(buttons))
	}
	return bufferPlain(buf), buttons
}

func TestConnectedWorkspaceTrustPersistenceFailureKeepsActionsAndRetries(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			authority := make(chan *workspace.Workspace, 1)
			runtime := remoteJourneyRuntime(t, "", func(w *workspace.Workspace) {
				authority <- w
			})
			one := newRemoteJourneyShell(t, runtime, t.TempDir())
			w := <-authority
			if _, err := w.WorkspaceDB.DB.Exec(`CREATE TRIGGER reject_trust BEFORE INSERT ON workspace_metadata
				WHEN NEW.key = 'workspace_settings' BEGIN SELECT RAISE(ABORT, 'trust storage refused'); END`); err != nil {
				t.Fatal(err)
			}
			_, cmd := one.app.Update(tea.WindowSizeMsg{Width: size.W, Height: size.H})
			runAppCommand(t, one.app, cmd)
			one.app.Update(types.TickMsg{})
			paint, _ := workspaceTrustPromptPaint(t, one.app)
			for _, want := range []string{"Trust folder", "Open in restricted mode", "Workspace settings."} {
				if !strings.Contains(paint, want) {
					t.Fatalf("Trust detail or action %q is unreachable:\n%s", want, paint)
				}
			}
			_, cmd = one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("native restricted choice did not prepare persistence")
			}
			paint, buttons := workspaceTrustPromptPaint(t, one.app)
			if !strings.Contains(paint, "Saving your choice") {
				t.Fatal("the in-flight save is not painted", paint)
			}
			for _, button := range buttons {
				if !button.Semantics().Disabled {
					t.Fatal("in-flight Trust action remains enabled", button.Semantics().Label)
				}
			}
			one.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			_, duplicate := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if duplicate != nil || !one.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
				t.Fatal("in-flight input dismissed or repeated the preference transaction")
			}
			runAppCommand(t, one.app, cmd)
			paint, buttons = workspaceTrustPromptPaint(t, one.app)
			if !strings.Contains(paint, "trust storage refused") || !strings.Contains(paint, "Try again.") ||
				!one.app.ws.LoadAppSettings().Analysis.DecisionNeeded() ||
				len(one.app.ws.LoadWorkspaceOverrides().Analysis) != 0 {
				t.Fatalf("real persistence failure lost the decision or painted repair guidance:\n%s", paint)
			}
			for _, button := range buttons {
				if button.Semantics().Disabled {
					t.Fatal("failed save left its retry action disabled", button.Semantics().Label)
				}
			}
			if _, err := w.WorkspaceDB.DB.Exec("DROP TRIGGER reject_trust"); err != nil {
				t.Fatal(err)
			}
			_, cmd = one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, one.app, cmd)
			if one.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) ||
				one.app.ws.LoadAppSettings().Analysis.DecisionNeeded() || one.app.ws.LoadAppSettings().Analysis.Trusted() {
				t.Fatal("the native retry did not persist Restricted and close the prompt")
			}
			var encoded string
			if err := w.WorkspaceDB.DB.QueryRow("SELECT value FROM workspace_metadata WHERE key = 'workspace_settings'").Scan(&encoded); err != nil {
				t.Fatal(err)
			}
			var saved types.WorkspaceOverrides
			if err := json.Unmarshal([]byte(encoded), &saved); err != nil {
				t.Fatal(err)
			}
			if string(saved.Analysis) != `{"trust":"restricted"}` || saved.AnalysisRootIdentity != "" {
				t.Fatal("the retry's durable decision differs from the painted result", encoded)
			}
		})
	}
}

func TestConnectedWorkspaceTrustEscAsksAfterRealSwitchBack(t *testing.T) {
	root, away := t.TempDir(), t.TempDir()
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, root)
	one.app.Update(types.TickMsg{})
	workspaceTrustPromptPaint(t, one.app)
	generation := one.app.workspaceSwitchGen.Load()
	one.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	one.app.Update(types.TickMsg{})
	if one.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) || !one.app.ws.LoadAppSettings().Analysis.DecisionNeeded() ||
		len(one.app.ws.LoadWorkspaceOverrides().Analysis) != 0 {
		t.Fatal("Esc stored a choice or asked twice in one attachment")
	}
	runAppCommand(t, one.app, confirmWorkspaceSwitchTest(t, one.app, away))
	if one.app.ws.RootPath() != away || one.app.workspaceSwitchGen.Load() <= generation {
		t.Fatal("test did not complete a real workspace switch")
	}
	one.app.Update(types.TickMsg{})
	workspaceTrustPromptPaint(t, one.app)
	one.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, one.app, confirmWorkspaceSwitchTest(t, one.app, root))
	one.app.Update(types.TickMsg{})
	paint, _ := workspaceTrustPromptPaint(t, one.app)
	if one.app.ws.RootPath() != root || !strings.Contains(paint, "Trust this folder?") ||
		!one.app.ws.LoadAppSettings().Analysis.DecisionNeeded() || len(one.app.ws.LoadWorkspaceOverrides().Analysis) != 0 {
		t.Fatal("returning to the undecided folder did not ask again", paint)
	}
}

func TestConnectedWorkspaceTrustPageResetRemovesDurableChoice(t *testing.T) {
	root := t.TempDir()
	authority := make(chan *workspace.Workspace, 1)
	runtime := remoteJourneyRuntime(t, "", func(w *workspace.Workspace) {
		grantSemanticFixture(t, w)
		result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
			{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")},
			{Path: []string{"lsp", "languages", "go", "enabled"}, After: json.RawMessage("false")},
		})()
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		w.ApplyPreferencesSave(result)
		authority <- w
	})
	one := newRemoteJourneyShell(t, runtime, root)
	w := <-authority
	one.app.openSettings(types.SettingsScopeWorkspace)
	body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil {
		t.Fatal("Workspace settings did not open")
	}
	body.FocusSearch()
	for _, r := range "Code Intelligence" {
		_, cmd := one.app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		runAppCommand(t, one.app, cmd)
	}
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Trust") {
		t.Fatal("native settings search did not reach Code Intelligence")
	}
	reset := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.reset"})
	if reset.Cmd == nil {
		t.Fatal("Code Intelligence has no page Reset action")
	}
	runAppCommand(t, one.app, reset.Cmd)
	if body.Model().Saving() || body.Model().SaveError() != "" ||
		!one.app.ws.LoadAppSettings().Analysis.DecisionNeeded() || one.app.ws.LoadWorkspaceOverrides().AnalysisRootIdentity != "" ||
		one.app.ws.LoadAppSettings().Editor.TabSize != 7 {
		t.Fatal("page Reset retained Trust, lost a neighbor, or did not finish", body.Model().SaveError())
	}
	var encoded string
	if err := w.WorkspaceDB.DB.QueryRow("SELECT value FROM workspace_metadata WHERE key = 'workspace_settings'").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &saved); err != nil {
		t.Fatal(err)
	}
	if _, present := saved["analysis"]; present {
		t.Fatal("page Reset persisted a trust choice", encoded)
	}
	if _, present := saved["analysis_root_identity"]; present {
		t.Fatal("page Reset persisted the cleared native binding", encoded)
	}
	if !strings.Contains(string(saved["lsp"]), `"go":{"enabled":false}`) {
		t.Fatal("Code Intelligence Reset erased the Language Servers inventory", encoded)
	}
	next := newRemoteJourneyShell(t, runtime, root)
	next.app.Update(types.TickMsg{})
	paint, _ := workspaceTrustPromptPaint(t, next.app)
	if !strings.Contains(paint, "Trust this folder?") {
		t.Fatal("new shell did not ask after the persisted page Reset", paint)
	}
}
