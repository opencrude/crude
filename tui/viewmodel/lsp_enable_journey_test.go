package viewmodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/termdock"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestConnectedLSPEnableSaveExplainsIdleAndStartsOpenSource(t *testing.T) {
	for _, alreadyOpen := range []bool{false, true} {
		t.Run(map[bool]string{false: "open-after-save", true: "already-open"}[alreadyOpen], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "main.go")
			if err := os.WriteFile(path, []byte("package main\nvar _ = missingEnableIdentifier\n"), 0600); err != nil {
				t.Fatal(err)
			}
			server := &immediateTreeDiagRuntime{filePath: path, diags: []workspace.LSPDiagnostic{{Severity: workspace.DiagError, Message: "undefined: missingEnableIdentifier"}}}
			saved := filepath.Join(t.TempDir(), "application.json")
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				w.LSPMgr.SetRuntime(server)
				s := w.AppDefaults()
				s.LSP.Enabled = false
				w.SetAppDefaults(s)
				grantSemanticFixture(t, w)
				w.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
					after := w.AppDefaults()
					if err := types.ApplySettingsEdits(&after, edits); err != nil {
						return after, err
					}
					raw, err := json.Marshal(after)
					if err == nil {
						err = os.WriteFile(saved, raw, 0600)
					}
					return after, err
				}
			})
			one := newRemoteJourneyShell(t, runtime, root)
			openSource := func() {
				t.Helper()
				cmd, err := one.app.openEditorTab(path)
				if err != nil {
					t.Fatal(err)
				}
				runAppCommand(t, one.app, cmd)
				runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
				if tab := one.app.EditorState.Tabs[path]; tab == nil || !tab.Editor.IsOpen() {
					t.Fatal("source did not open through the connected editor")
				}
			}
			if alreadyOpen {
				openSource()
			}
			invoke := func(id string, op ui.ControlOperation) {
				t.Helper()
				if result := journeySettingsControl(t, runtime, []remoteJourneyShell{one}, one, id, op); result.State != control.Applied {
					t.Fatalf("%s: %+v", id, result)
				}
			}
			requestShellForTest(t, runtime, []remoteJourneyShell{one}, one, "settings.open", map[string]any{"scope": "application"}, nil)
			invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Code Intelligence"`)})
			invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "activate"})
			invoke("workspace-settings/form/enabled", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"on"`)})
			body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
			if !body.Model().AppSettings().LSP.Enabled {
				t.Fatalf("enable did not stage a changed setting: effective=%t initial=%t", body.Model().AppSettings().LSP.Enabled, one.app.ws.AppDefaults().LSP.Enabled)
			}
			invoke(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"})
			remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool { return one.app.ws.LSPMgr().Settings().Enabled })
			invoke(string(settingsui.WorkspaceSettingsCancelID), ui.ControlOperation{Verb: "activate"})
			if !alreadyOpen {
				one.app.toggleWorkspacePanel(termdock.PanelProblems)
				one.app.paintShellFrame()
				one.app.rootHost.Focus("status-overview/list")
				_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
				runAppCommand(t, one.app, cmd)
				paint := ansi.Strip(one.app.View().RenderContent())
				if !strings.Contains(paint, "Waiting for source file") || strings.Contains(paint, "No language server") {
					t.Fatalf("saved enabled idle state is misleading:\n%s", paint)
				}
				if !strings.Contains(paint, "Open a supported source file") {
					t.Fatalf("expanded idle state hides startup guidance:\n%s", paint)
				}
				server.mu.Lock()
				started := len(server.rootURIs)
				server.mu.Unlock()
				if started != 0 {
					t.Fatal("Save eagerly started unused server")
				}
				openSource()
			}
			remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool { return one.app.ws.LSPMgr().ProjectInsights().Errors == 1 })
			one.app.hideWorkspacePanel()
			one.app.toggleWorkspacePanel(termdock.PanelProblems)
			if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "missingEnableIdentifier") || strings.Contains(paint, "Waiting for source file") {
				t.Fatalf("opened source diagnostics not rendered:\n%s", paint)
			}
			if !one.app.ws.LoadAppSettings().LSP.Enabled || !one.app.ws.AppDefaults().LSP.Enabled {
				t.Fatal("Save did not retain application LSP enable")
			}
			raw, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			var persisted types.AppSettings
			if err := json.Unmarshal(raw, &persisted); err != nil || !persisted.LSP.Enabled {
				t.Fatalf("Save did not persist LSP enable: enabled=%t error=%v", persisted.LSP.Enabled, err)
			}
		})
	}
}
