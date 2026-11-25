package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/core"
	"crude/core/shellport/remote"
	"crude/tui/screens/activitybar"
	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/types/setting"
	"crude/workspace"
)

func TestConnectedMCPReferenceSurvivesSettingsRefreshAndRename(t *testing.T) {
	const id, reference = "registered", "secret:SYNTHETIC_CLIENT"
	commits := make(chan captypes.ConnectionConfig, 2)
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", setting.Settings{}, noopLogger{})
		settings := ws.AppDefaults()
		settings.Capabilities.Connections[id] = captypes.ConnectionConfig{
			ID: id, Name: "Client", Transport: "http", URL: "https://mcp.example.test",
			Scope: captypes.ScopeApplication, Auth: "oauth", OAuthClientID: "registered-client",
			OAuthIssuer: "https://issuer.example.test", CredentialRef: reference,
		}
		ws.SetAppDefaults(settings)
		if _, err := ws.CapabilitySnapshot(t.Context()); err != nil {
			ws.Close()
			return nil, err
		}
		ws.OnAppPreferencesCommit = func(edits []setting.Edit) (setting.App, error) {
			next := ws.AppDefaults()
			if err := setting.ApplyEdits(&next, edits); err != nil {
				return setting.App{}, err
			}
			commits <- next.Capabilities.Connections[id]
			return next, nil
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	app := newRemoteJourneyShell(t, runtime, t.TempDir()).app
	app.openApplicationSettings()
	initial := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings)).Model().AppSettings()
	if initial.Capabilities.Connections[id].CredentialRef != remote.StoredSecret {
		t.Error("opening Settings discarded the stored reference before the first capability refresh")
	}

	openEditor := func(wantStored bool) *ui.Form {
		t.Helper()
		app.openApplicationSettings()
		runAppCommand(t, app, app.openCapabilityManager(activitybar.ViewMCP, "", id))
		poll, err := app.ws.CapabilityRuntimeSnapshot(t.Context())
		if err != nil || (poll.Settings.Connections[id].CredentialRef == remote.StoredSecret) != wantStored {
			t.Fatal("passive observation lost the current credential state")
		}
		window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
		body := app.settingsSurface(window)
		body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.edit"})
		host := window.Content.(*ui.WindowFrameWidget).Host()
		host.MarkLayout()
		host.Layout()
		form := host.Component("settings/capabilities/form").(*ui.Form)
		field := form.Field("oauth-secret-ref")
		if field == nil || field.FieldState().Type != ui.FieldPassword || field.FieldState().Value.String() != "" {
			t.Fatal("client reference is not a write-only field")
		}
		form.FocusField("oauth-secret-ref")
		host.MarkLayout()
		paint := paintCapabilitySettings(t, app)
		if !strings.Contains(paint, "Secret reference") || strings.Contains(paint, reference) {
			t.Fatal("the painted MCP editor hid the field or exposed its reference")
		}
		if strings.Contains(paint, "Stored") != wantStored {
			t.Errorf("refreshed editor: want Stored=%t", wantStored)
		}
		return form
	}
	save := func(wantReference string) {
		t.Helper()
		body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
		result := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.save"})
		if result.Cmd == nil {
			t.Fatal("MCP editor did not submit Save")
		}
		runAppCommand(t, app, result.Cmd)
		select {
		case conn := <-commits:
			if conn.Name != "Client2" || conn.CredentialRef != wantReference {
				t.Errorf("Save did not commit the name and requested reference state")
			}
		default:
			t.Fatal("Save did not reach the settings owner")
		}
	}
	form := openEditor(true)
	name := form.Field("name")
	name.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
	name.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: '2', Text: "2"}))
	save(reference)
	form = openEditor(true)
	if form.Field("name").FieldState().Value.String() != "Client2" {
		t.Fatal("renamed client did not reopen")
	}
	form.Field("oauth-secret-ref").Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: '-', Text: "-"}))
	save("")
	openEditor(false)
}
