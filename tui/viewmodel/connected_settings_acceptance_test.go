package viewmodel

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/core"
	"crude/provider"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"

	"github.com/charmbracelet/x/ansi"
)

// Use the actual framed net.Pipe remote App, not an in-process Workspace port.
func TestConnectedSettingsAccountKeepAndFailedPreferenceDraft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var commits atomic.Int32
	const secret = "acceptance-private-account-key"
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, noopLogger{})
		ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {
			ID: "one", ProviderID: "openai", Label: "Original", APIKey: secret,
			Connection: provider.DefaultTemplates()["openai"],
		}}})
		baseline := ws.AppDefaults()
		ws.OnAppAccountCommit = func(change types.AccountChange) (types.AppSettings, error) {
			if change.Before.APIKey != secret || change.After.APIKey != secret {
				return types.AppSettings{}, errors.New("stored credential was not kept")
			}
			commits.Add(1)
			baseline.Accounts = map[string]provider.Account{"one": *change.After}
			return baseline, nil
		}
		ws.OnAppPreferencesCommit = func([]types.SettingEdit) (types.AppSettings, error) {
			return types.AppSettings{}, errors.New("durable acceptance write failed")
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	app := shell.app
	app.openApplicationAccounts()
	parent := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(parent)
	model := body.Model()
	body.ShowAccounts()
	model.SelectAccountForTest("one")
	body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	model.SetValueForTest(model.SelectedCategory(), "account-label", "Kept credential")
	request := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if request.Cmd == nil {
		t.Fatal("real account form did not prepare its change")
	}
	message := request.Cmd().(settingsui.AccountChangeMsg)
	if message.Change.Before.APIKey == secret || message.Change.After.APIKey == secret {
		t.Fatal("remote form received private credential")
	}
	_, cmd := app.Update(message)
	runAppCommand(t, app, cmd)
	if commits.Load() != 1 {
		t.Fatal("form save did not commit exactly once")
	}
	if strings.Contains(ansi.Strip(app.View().RenderContent()), secret) {
		t.Fatal("paint exposed credential")
	}
	// A later deliberate Save is valid without a configuration revision.
	if err := app.ws.SaveAccountChange(message.Change); err != nil {
		t.Fatal("repeated explicit account edit was rejected", err)
	}
	if commits.Load() != 2 {
		t.Fatal("second explicit save did not reach its owner")
	}

	// Start from a fresh retained form; real Save emits the owner-fenced message.
	app.openApplicationSettings()
	parent = app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body = app.settingsSurface(parent)
	model = body.Model()
	// Appearance is the category that owns the preset form, not the initial rail page.
	appearance := -1
	for index, category := range model.Categories() {
		if category.Form != nil && category.Form.Field("preset") != nil {
			appearance = index
			break
		}
	}
	if appearance < 0 {
		t.Fatal("Appearance preset form is missing")
	}
	model.SelectCategory(appearance)
	model.SetValueForTest(model.SelectedCategory(), "preset", "light")
	before := model.AppSettings()
	if before.Theme.Preset != "light" || len(model.Edits()) == 0 {
		t.Fatal("Appearance edit did not stage the light preset before Save")
	}
	save := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if save.Cmd == nil {
		t.Fatal("real preference form did not emit Save")
	}
	_, cmd = app.Update(save.Cmd())
	runAppCommand(t, app, cmd)
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || model.Saving() || model.SaveError() == "" {
		t.Fatal("durable failure closed form or stranded busy state")
	}
	if !reflect.DeepEqual(model.AppSettings().Theme, before.Theme) || before.Theme.Preset != "light" {
		t.Fatal("failed save consumed preference draft")
	}
}

func TestConnectedPreferenceSaveWaitsForAppliedTopic(t *testing.T) {
	for _, closeAttachment := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "detach"}[closeAttachment], func(t *testing.T) {
			committed := make(chan struct{})
			runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
				ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, noopLogger{})
				ws.SetAppDefaults(types.AppSettings{Theme: types.ThemeSettings{Preset: "dark"}})
				ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
					after := ws.AppDefaults()
					err := types.ApplySettingsEdits(&after, edits)
					close(committed)
					return after, err
				}
				return ws, nil
			}, filepath.Join(t.TempDir(), "receipts.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			shell := newRemoteJourneyShell(t, runtime, t.TempDir())
			app := shell.app
			app.openApplicationSettings()
			body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
			model := body.Model()
			for index, category := range model.Categories() {
				if category.Form != nil && category.Form.Field("preset") != nil {
					model.SelectCategory(index)
				}
			}
			model.SetValueForTest(model.SelectedCategory(), "preset", "light")
			save := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
			if save.Cmd == nil {
				t.Fatal("Save did not prepare the preference draft")
			}
			cmd := app.savePreferences(save.Cmd().(settingsui.SaveMsg))
			if cmd == nil {
				t.Fatal("Save was not admitted")
			}
			completed := make(chan preferencesSavedMsg, 1)
			go func() { completed <- cmd().(preferencesSavedMsg) }()
			select {
			case <-committed:
			case <-time.After(5 * time.Second):
				t.Fatal("preference persistence did not finish")
			}
			// Hold every real publication in the FIFO. A network receipt or queued
			// topic cannot report Save before Update installs the new settings.
			select {
			case result := <-completed:
				t.Fatalf("Save completed before any topic was applied: error=%v", result.result.Err)
			case <-time.After(100 * time.Millisecond):
			}
			if !model.Saving() || model.AppSettings().Theme.Preset != "light" || app.ws.AppDefaults().Theme.Preset != "dark" {
				t.Fatal("queued topic changed the draft or replica before Update")
			}
			if closeAttachment {
				if err := app.CloseWorkspaceAttachment(); err != nil {
					t.Fatal(err)
				}
				select {
				case result := <-completed:
					if result.result.Err == nil {
						t.Fatal("detached save reported an applied settings projection")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("detachment did not cancel the projection waiter")
				}
				return
			}
			var result preferencesSavedMsg
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				select {
				case result = <-completed:
					return true
				default:
					return false
				}
			})
			app.applyPreferencesSaved(result)
			if result.result.Err != nil || body.Model().Saving() || len(body.Model().Edits()) != 0 || body.Model().AppSettings().Theme.Preset != "light" || app.ws.AppDefaults().Theme.Preset != "light" {
				t.Fatal("applied save did not leave an authoritative clean light preset")
			}
		})
	}
}
