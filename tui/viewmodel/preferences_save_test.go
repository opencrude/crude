package viewmodel

import (
	"errors"
	"testing"
	"time"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
)

func TestPreferencesSaveIsOffUIAndPreservesDraftOnFailure(t *testing.T) {
	app, parent, body := newSettingsPromptApp(t)
	model := body.Model()
	before := model.AppSettings()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	app.OnAppSettingsSave = func(settings types.AppSettings) (types.AppSettings, error) {
		close(entered)
		<-release
		return types.AppSettings{}, errors.New("client certificate cannot be read")
	}
	if !model.BeginSave() {
		t.Fatal("could not arm preference save")
	}
	started := time.Now()
	command := app.savePreferences(settingsui.SaveMsg{Target: model, Scope: types.SettingsScopeApplication, AppSettings: before})
	if command == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatal("preference save blocked UI admission")
	}
	select {
	case <-entered:
		t.Fatal("certificate/persistence work ran before the command")
	default:
	}
	done := make(chan preferencesSavedMsg, 1)
	go func() { done <- command().(preferencesSavedMsg) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("persistence command did not run")
	}
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || !model.Saving() {
		t.Fatal("settings did not stay mounted during validation")
	}
	// Release once without closing the channel twice during cleanup.
	release <- struct{}{}
	result := <-done
	app.applyPreferencesSaved(result)
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != parent || model.Saving() || model.SaveError() == "" {
		t.Fatal("failed save closed the form or stranded its busy state")
	}
	if model.AppSettings().Network != before.Network {
		t.Fatal("failed save consumed the staged network draft")
	}
}

func TestLatePreferenceSaveCannotCloseReplacementSettings(t *testing.T) {
	app, _, body := newSettingsPromptApp(t)
	model := body.Model()
	if !model.BeginSave() {
		t.Fatal("could not arm save")
	}
	command := app.savePreferences(settingsui.SaveMsg{Target: model, Scope: types.SettingsScopeApplication, AppSettings: model.AppSettings()})
	result := command().(preferencesSavedMsg)
	app.openApplicationSettings()
	replacement := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	app.applyPreferencesSaved(result)
	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != replacement {
		t.Fatal("late save closed a replacement overlay")
	}
}
