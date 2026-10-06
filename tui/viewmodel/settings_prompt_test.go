package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

func newSettingsPromptApp(t *testing.T) (*App, *wm.FloatingWindow, *settingsui.WorkspaceSettingsSurface) {
	t.Helper()
	app := newTestApp(t)
	app.Logger = noopLogger{}
	app.width, app.height = 160, 40
	app.registerOverlayHandlers()
	app.SetAppDefaults(types.AppSettings{

		Accounts: map[string]provider.Account{
			"work": {ID: "work", ProviderID: "openrouter", Label: "Work", Connection: provider.DefaultTemplates()["openrouter"]},
			"home": {ID: "home", ProviderID: "openrouter", Label: "Home", Connection: provider.DefaultTemplates()["openrouter"]},
		},
	})
	app.openApplicationSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if parent == nil || app.settingsSurface(parent) == nil {
		t.Fatal("settings window did not mount")
	}
	return app, parent, app.settingsSurface(parent)
}

func requestSettingsPrompt(t *testing.T, app *App, body *settingsui.WorkspaceSettingsSurface, action string) settingsui.PromptMsg {
	t.Helper()
	res := body.HandleAction(ui.EventContext{}, ui.Action{ID: action})
	if res.Cmd == nil {
		t.Fatalf("%s did not request confirmation", action)
	}
	msg, ok := res.Cmd().(settingsui.PromptMsg)
	if !ok {
		t.Fatalf("%s returned %T", action, res.Cmd())
	}
	_, _ = app.Update(msg)
	return msg
}

func TestSettingsPromptStacksAboveMovedParentAndKeepsSelection(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	body.ShowAccounts()
	body.Model().SelectAccountForTest("work")
	parent.SetPosition(18, 7)
	px, py := parent.Position()
	pw, ph := parent.Size()
	msg := requestSettingsPrompt(t, app, body, "settings.account.remove")
	if msg.Kind != settingsui.PromptRemoveAccount || app.overlayHost.Top().ID != ui.OverlaySettingsPrompt || len(app.overlayHost.All()) != 2 {
		t.Fatal("account removal did not stack a separate settings prompt")
	}
	prompt := app.overlayHost.Window(ui.OverlaySettingsPrompt)
	w, h := prompt.Size()
	x, y := prompt.Position()
	if x != px+(pw-w)/2 || y != py+(ph-h)/2 {
		t.Fatalf("prompt at (%d,%d), not centered over moved settings (%d,%d,%d,%d)", x, y, px, py, pw, ph)
	}
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || app.settingsSurface(parent) != body || body.Model() != msg.Target {
		t.Fatal("prompt replaced the settings window or model")
	}
	cw := prompt.Content.(*ui.ComponentWidget)
	ww, hh := prompt.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	buf := surface.NewCellBuffer(ww, hh)
	cw.Host().Paint(buf)
	if painted := bufferPlain(buf); !strings.Contains(painted, "Remove account") || !strings.Contains(painted, "Work") || !strings.Contains(painted, "Keep account") {
		t.Fatalf("immutable account preview was not painted: %s", painted)
	}
	if cw.Host().Focused() != ui.ConfirmButtonID(1) {
		t.Fatal("destructive prompt does not default to Keep account")
	}
	_, _ = app.Update(tea.MouseClickMsg{X: px + 1, Y: py + 2, Button: tea.MouseLeft})
	if app.overlayHost.Top().ID != ui.OverlaySettingsPrompt || !body.PromptRelevant(msg) {
		t.Fatal("outside click reached settings beneath modal prompt")
	}
	_, _ = app.Update(tea.MouseClickMsg{X: px + 1, Y: py + 2, Button: tea.MouseRight})
	if app.overlayHost.Top().ID != ui.OverlaySettingsPrompt || app.overlayHost.IsOpen(ui.OverlayContextMenu) {
		t.Fatal("outside right click escaped the modal prompt")
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || !body.PromptRelevant(msg) {
		t.Fatal("safe Enter did not keep the selected account and window")
	}
	nx, ny := parent.Position()
	nw, nh := parent.Size()
	if nx != px || ny != py || nw != pw || nh != ph {
		t.Fatal("dismissal changed the settings window rect")
	}
}

func TestSettingsPromptMouseAcceptAndConflictFailClosed(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	body.ShowAccounts()
	body.Model().SelectAccountForTest("work")
	msg := requestSettingsPrompt(t, app, body, "settings.account.remove")
	prompt := app.overlayHost.Window(ui.OverlaySettingsPrompt)
	cw := prompt.Content.(*ui.ComponentWidget)
	w, h := prompt.Size()
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	r, ok := cw.Host().ScreenRect(ui.ConfirmButtonID(0))
	if !ok {
		t.Fatal("confirm button has no hit bounds")
	}
	x, y := prompt.Position()
	_, cmd := app.Update(tea.MouseClickMsg{X: x + r.X + r.W/2, Y: y + r.Y, Button: tea.MouseLeft})
	if cmd == nil || app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent {
		t.Fatal("mouse accept did not return asynchronous change while keeping settings")
	}
	if change, ok := cmd().(settingsui.AccountChangeMsg); !ok || change.Target != msg.Target || change.Change.ID != msg.ID || change.Change.After != nil {
		t.Fatalf("mouse accept did not return the selected account removal: %T", cmd())
	}
	if _, ok := body.Model().AppSettings().Accounts[msg.ID]; !ok {
		t.Fatal("mouse accept mutated account data before async completion")
	}
	// Delivering a prompt after its parent has been replaced must not reopen it.
	app.openApplicationSettings()
	_, _ = app.Update(msg)
	if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
		t.Fatal("old settings model opened a prompt on replacement settings")
	}
}

func TestSettingsPromptRejectsChangedSelectionOnAccept(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	body.ShowAccounts()
	body.Model().SelectAccountForTest("work")
	requestSettingsPrompt(t, app, body, "settings.account.remove")
	body.Model().SelectAccountForTest("home")
	app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if cmd := app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("changed selection produced a removal command")
	}
	if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent {
		t.Fatal("conflict closed or replaced settings")
	}
	found := false
	for _, notice := range app.Status.Notifications(true) {
		if notice.Summary == "Settings changed; review and retry" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale confirmation did not report the conflict")
	}
}

func TestSettingsPromptNarrowTerminalKeepsSafeChoiceVisible(t *testing.T) {
	app, _, _ := newSettingsPromptApp(t)
	app.width, app.height = 70, 18
	app.openApplicationSettings()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	body.ShowAccounts()
	body.Model().SelectAccountForTest("work")
	requestSettingsPrompt(t, app, body, "settings.account.remove")
	prompt := app.overlayHost.Window(ui.OverlaySettingsPrompt)
	x, y := prompt.Position()
	w, h := prompt.Size()
	if x < 0 || y < 0 || x+w > app.width || y+h > app.height {
		t.Fatalf("prompt outside 70x18 terminal: (%d,%d,%d,%d)", x, y, w, h)
	}
	cw := prompt.Content.(*ui.ComponentWidget)
	cw.Host().Resize(w, h)
	cw.Host().Layout()
	buf := surface.NewCellBuffer(w, h)
	cw.Host().Paint(buf)
	if !strings.Contains(bufferPlain(buf), "Keep account") || cw.Host().Focused() != ui.ConfirmButtonID(1) {
		t.Fatal("narrow prompt lost its safe visible default")
	}
	app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent {
		t.Fatal("Esc closed narrow settings window")
	}
}

func TestSettingsPromptDirtyCloseXAndParentTeardown(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	for _, item := range body.Model().CategoryItems() {
		if item.Label == "Appearance" {
			body.Model().SelectCategory(item.Payload.(int))
			break
		}
	}
	body.Model().SetValueForTest(body.Model().SelectedCategory(), "preset", "light")
	px, py := parent.Position()
	pw, _ := parent.Size()
	_, cmd := app.Update(tea.MouseClickMsg{X: px + pw - 2, Y: py, Button: tea.MouseLeft})
	if cmd == nil || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent {
		t.Fatal("settings X closed dirty settings without a decision")
	}
	msg, ok := cmd().(settingsui.PromptMsg)
	if !ok || msg.Kind != settingsui.PromptDiscardSettings {
		t.Fatalf("dirty X returned %T, want discard prompt", cmd())
	}
	_, _ = app.Update(msg)
	if !app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
		t.Fatal("dirty X did not show a second window")
	}
	app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent {
		t.Fatal("Esc discarded drafts or closed settings")
	}
	_, _ = app.Update(msg)
	app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.settingsPrompt != nil {
		t.Fatal("closing parent left an orphan prompt")
	}
}

func TestSettingsPromptDirtyEditorBackDecisionKeepsParent(t *testing.T) {
	for _, tc := range []struct {
		name, category, editAction, backAction, field string
	}{
		{"account", "Accounts", "settings.account.edit", "settings.account.cancel-edit", "account-label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, parent, body := newSettingsPromptApp(t)
			for _, item := range body.Model().CategoryItems() {
				if item.Label == tc.category {
					body.Model().SelectCategory(item.Payload.(int))
					break
				}
			}
			body.HandleAction(ui.EventContext{}, ui.Action{ID: tc.editAction})
			body.Model().SetValueForTest(body.Model().SelectedCategory(), tc.field, "Unsaved edit")
			prompt := requestSettingsPrompt(t, app, body, tc.backAction)
			if prompt.Kind != settingsui.PromptDiscardEditor {
				t.Fatalf("dirty editor Back returned %+v instead of a discard prompt", prompt)
			}
			app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || !body.PromptRelevant(prompt) {
				t.Fatal("safe default did not keep the editor and selected row")
			}
			_, _ = app.Update(prompt)
			app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyLeft})
			if cmd := app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
				t.Fatal("discarding editor draft scheduled persistence")
			}
			if app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || body.PromptRelevant(prompt) {
				t.Fatal("discard did not return to the same settings catalog")
			}
		})
	}
}

func TestSettingsPromptDiscardDraftsClosesOnlyAfterCancelMsg(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	for _, item := range body.Model().CategoryItems() {
		if item.Label == "Appearance" {
			body.Model().SelectCategory(item.Payload.(int))
			break
		}
	}
	body.Model().SetValueForTest(body.Model().SelectedCategory(), "preset", "light")
	requestSettingsPrompt(t, app, body, "settings.cancel")
	app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	cmd := app.handleSettingsPromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || app.overlayHost.IsOpen(ui.OverlaySettingsPrompt) {
		t.Fatal("discard confirmation did not defer parent close to CancelMsg")
	}
	msg := cmd()
	if _, ok := msg.(settingsui.CancelMsg); !ok {
		t.Fatalf("discard returned %T instead of CancelMsg", msg)
	}
	_, _ = app.Update(msg)
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("confirmed discard did not close settings")
	}
}
