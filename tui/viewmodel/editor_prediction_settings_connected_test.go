package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/session"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// Enter through the real mounted settings form and framed core. These tests
// simulate native human keys; the external control client is deliberately
// limited to ordinary Read/Write and must not acquire consent through any row
// or through Save/Reset after the authority draft leaves the visible page.
func TestConnectedPredictionConsentNativeSaveAndHumanOnlyControls(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				http.Error(w, "consent must not probe the provider", http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
				cfg.AccountManager = provider.NewAccountManager(cfg.Accounts, nil)
			})
			root := t.TempDir()
			one := newRemoteJourneyShell(t, runtime, root)
			two := newRemoteJourneyShell(t, runtime, root)
			shells := []remoteJourneyShell{one, two}
			_, resize := one.app.Update(tea.WindowSizeMsg{Width: size.W, Height: size.H})
			runAppCommand(t, one.app, resize)
			if result := requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "workspace"}, nil); result.State != control.Applied {
				t.Fatal(result)
			}
			writer := analysisControlGateway(t, runtime, one, root, []control.Effect{control.Read, control.Write})
			navigate := func(page string) {
				t.Helper()
				value, _ := json.Marshal(page)
				for _, op := range []ui.ControlOperation{{Verb: "set_value", Value: value}, {Verb: "activate"}} {
					_, desc, node := invokeDeckControl(t, one, string(settingsui.WorkspaceSettingsSearchID), op)
					if result := writer(node, op, desc.ID); result.State != control.Applied {
						t.Fatalf("ordinary settings navigation failed: %+v", result)
					}
				}
			}
			body := func() *settingsui.WorkspaceSettingsSurface {
				t.Helper()
				surface := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
				if surface == nil {
					t.Fatal("prediction settings lost their mounted owner")
				}
				return surface
			}
			key := func(msg tea.KeyPressMsg) {
				t.Helper()
				_, cmd := one.app.Update(msg)
				runAppCommand(t, one.app, cmd)
			}
			focus := func(id string) *ui.Form {
				t.Helper()
				surface := body()
				surface.FocusForm()
				for _, child := range surface.Children() {
					if form, ok := child.(*ui.Form); ok && form.ID() == settingsui.WorkspaceSettingsFormID {
						if !form.FocusField(id) {
							t.Fatalf("native form cannot reach %s", id)
						}
						one.app.paintShellFrame()
						return form
					}
				}
				t.Fatal("Code Intelligence has no mounted native form")
				return nil
			}
			deny := func(id string, operation ui.ControlOperation) {
				t.Helper()
				_, desc, node := invokeDeckControl(t, one, id, operation)
				if !desc.HumanOnly || desc.Effect != control.Authority {
					t.Fatalf("prediction control lost human authority: %+v", desc)
				}
				result := writer(node, operation, desc.ID)
				if result.RPCFault != nil && result.RPCFault.Code == "control_failed" && strings.Contains(result.RPCFault.Message, "human-owned authority") {
					return
				}
				if result.State != control.Failed || result.Fault == nil || result.Fault.Code != control.Denied {
					t.Fatalf("generic Write acquired source-egress consent: %+v", result)
				}
			}
			save := func() {
				t.Helper()
				body().FocusActions()
				key(tea.KeyPressMsg{Code: tea.KeyEnter})
				if body().Model().Saving() || body().Model().SaveError() != "" || len(body().Model().Edits()) != 0 {
					t.Fatalf("native Save did not settle through its settings publication: %s", body().Model().SaveError())
				}
			}
			navigate("Code Intelligence")
			if one.app.ws.LoadAppSettings().Prediction.Mode != types.PredictionOff {
				t.Fatal("opening settings acquired source-egress consent")
			}
			modeID := string(ui.NewID(settingsui.WorkspaceSettingsFormID, "prediction.mode"))
			deny(modeID, ui.ControlOperation{Verb: "select", ItemID: "light"})
			focus("prediction.mode")
			key(tea.KeyPressMsg{Code: tea.KeyRight})
			if choice := body().Model().AppSettings().Prediction; choice.Mode != types.PredictionLight || choice.HourlyTokenLimit != 0 {
				t.Fatal("native mode key was lost or invented a spending cap", choice)
			}
			// The invalid enable is still a visible draft, never a normalized Off
			// save or a server-side partial grant.
			rejected := body().HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
			if rejected.Cmd != nil || one.app.ws.LoadAppSettings().Prediction.Mode != types.PredictionOff {
				t.Fatal("enabled zero-cap draft reached persistence")
			}
			// Traverse from mode with the real form key, including the extra
			// validation rows introduced by the refused Save.
			key(tea.KeyPressMsg{Code: tea.KeyTab})
			key(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			key(tea.KeyPressMsg{Code: tea.KeyBackspace})
			for _, r := range "4200" {
				key(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			if choice := body().Model().AppSettings().Prediction; choice.Mode != types.PredictionLight || choice.HourlyTokenLimit != 4200 {
				t.Fatal("native cap input did not reach the exact preference draft", choice)
			}
			if painted := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(painted, "4200") || !strings.Contains(painted, "Hourly cap") {
				t.Fatalf("native prediction cap was not painted at this grid:\n%s", painted)
			}
			for _, id := range []ui.ComponentID{settingsui.WorkspaceSettingsSaveID, settingsui.WorkspaceSettingsResetID} {
				deny(string(id), ui.ControlOperation{Verb: "activate"})
			}
			save()
			remoteJourneyUntil(t, shells, func() bool {
				return two.app.ws.LoadAppSettings().Prediction.Mode == types.PredictionLight
			})
			grant := one.app.ws.LoadWorkspaceOverrides()
			if grant.PredictionRootIdentity == "" || grant.PredictionConnectionIdentity == "" || grant.PredictionGrantID == "" ||
				two.app.ws.LoadAppSettings().Prediction.HourlyTokenLimit != 4200 ||
				one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisUndecided ||
				one.app.ws.AppDefaults().Prediction.Mode != types.PredictionOff {
				t.Fatal("native Save lost the exact root/connection grant, peer projection or authority independence")
			}
			focus("prediction.mode")
			key(tea.KeyPressMsg{Code: tea.KeyRight}) // Light -> Full, not yet saved.
			navigate("Editor")
			deny(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"})
			deny(string(settingsui.WorkspaceSettingsResetID), ui.ControlOperation{Verb: "activate"})
			if one.app.ws.LoadAppSettings().Prediction.Mode != types.PredictionLight {
				t.Fatal("generic off-page Save changed committed source-egress consent")
			}
			navigate("Code Intelligence")
			focus("prediction.mode")
			key(tea.KeyPressMsg{Code: tea.KeyLeft})
			key(tea.KeyPressMsg{Code: tea.KeyLeft})
			save()
			remoteJourneyUntil(t, shells, func() bool {
				return two.app.ws.LoadAppSettings().Prediction.Mode == types.PredictionOff
			})
			revoked := one.app.ws.LoadWorkspaceOverrides()
			if revoked.PredictionRootIdentity != "" || revoked.PredictionConnectionIdentity != "" || revoked.PredictionGrantID != "" ||
				one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisUndecided || calls.Load() != 0 {
				t.Fatal("native revocation retained a grant, changed Trust or probed a provider", calls.Load())
			}
		})
	}
}
