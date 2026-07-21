package viewmodel

import (
	"testing"

	"crude/types"
	"crude/workspace"
)

func TestSessionDeckIsRetainedPerSession(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}

	app.ActiveSession = first
	app.openSessionSettings()
	firstProjection := app.activeSessionDeck()
	if firstProjection == nil || !firstProjection.deck.IsOpen() || !firstProjection.deckFocused {
		t.Fatal("first session deck did not open and focus")
	}

	app.ActiveSession = second
	app.openSessionSettings()
	secondProjection := app.activeSessionDeck()
	if secondProjection == nil || secondProjection == firstProjection || !secondProjection.deck.IsOpen() {
		t.Fatal("second session did not receive an independent deck")
	}
	app.closeSessionPanel(sessionSettingsPanelID)
	if secondProjection.deck.IsOpen() {
		t.Fatal("closing second session panel left its deck open")
	}

	app.ActiveSession = first
	if !firstProjection.deck.IsOpen() || firstProjection.settings != app.sessionSettings() {
		t.Fatal("switching back did not restore the first session deck")
	}
}

func TestSessionDeckReservesNativeRegionBelowMessages(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.launcher = nil
	app.ActiveSession = &workspace.Session{SessionID: "session", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	app.openSessionSettings()

	view := app.buildMainView()
	if view == "" || app.sessionDeckRect.Empty() {
		t.Fatal("main view did not reserve a native session deck region")
	}
	if got, want := app.sessionDeckRect.Y, app.ChatAreaHeight+3; got != want {
		t.Fatalf("deck Y=%d, want directly below messages at %d", got, want)
	}
	if region := app.classifyMouse(app.sessionDeckRect.X, app.sessionDeckRect.Y, false); region != regionSessionDeck {
		t.Fatalf("deck hit classified as %v", region)
	}
	if app.Overlay.IsOpen() {
		t.Fatal("native session settings must not create a legacy inline overlay tab")
	}
}

func TestSettingsProjectionLookupTargetsInactiveSession(t *testing.T) {
	app := newFullTestApp(t)
	first := &workspace.Session{SessionID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	second := &workspace.Session{SessionID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{}, types.Settings{}, nil)}
	firstProjection := app.sessionProjections.Get(first)
	app.sessionProjections.Get(second)
	app.ActiveSession = second

	owner, projection := app.sessionProjections.projectionForSettings(firstProjection.settings)
	if owner != first || projection != firstProjection {
		t.Fatal("async settings result was not routed to its owning inactive session")
	}
}
