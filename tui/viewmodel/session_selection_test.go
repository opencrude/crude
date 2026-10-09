package viewmodel

import (
	"reflect"
	"testing"

	"crude/types"
	"crude/workspace"
)

func TestShellSessionSelectionDoesNotChangeAnotherShell(t *testing.T) {
	one := &workspace.Session{SessionID: "one", AgentID: "first", Settings: workspace.NewSessionSettingsState(types.Settings{Model: "model-one"}, types.Settings{}, nil)}
	two := &workspace.Session{SessionID: "two", AgentID: "second", Settings: workspace.NewSessionSettingsState(types.Settings{Model: "model-two"}, types.Settings{}, nil)}
	owner := &workspace.Workspace{
		Sessions:        []*workspace.Session{one, two},
		SessionsByID:    map[string]*workspace.Session{"one": one, "two": two},
		SessionsByAgent: map[string]*workspace.Session{"first": one, "second": two},
	}
	// This tests the selected-session seam, not multi-client event transport.
	left := &App{Workspace: owner, FocusStack: NewFocusStack()}
	right := &App{Workspace: owner, FocusStack: NewFocusStack()}
	left.ActivateSessionByAgent("first")
	right.ActivateSessionByAgent("second")
	left.FocusStack.Push(FocusTree)
	right.FocusStack.Push(FocusInput)
	if left.ActiveSession != one || right.ActiveSession != two || left.ModelName != "model-one" || right.ModelName != "model-two" {
		t.Fatal("one shell's session/model selection changed the other")
	}
	left.ActivateSessionByAgent("unknown")
	if left.ActiveSession != one || right.ActiveSession != two || right.FocusStack.Top() != FocusInput {
		t.Fatal("invalid selection changed an existing client owner")
	}
	left.FocusStack.Clear()
	if right.FocusStack.Top() != FocusInput {
		t.Fatal("focus was cleared in another shell")
	}
}

func TestPaletteMultiAgentToggleUsesExactOwnerAndOnlyChangesDelegation(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	firstID := addBackendTestOrchestrator(t, app)
	first := app.SessionsByAgent[firstID]
	secondID := addBackendTestOrchestrator(t, app)
	second := app.SessionsByAgent[secondID]
	configure := func(owner *workspace.Session, model string) {
		t.Helper()
		settings := app.BackendSession(owner.SessionID).SettingsSnapshot()
		settings.SessionOverrides = map[string]types.CategorySettings{"default": {
			AccountID: settings.AccountID, Model: model, DeniedTools: []string{"write_file"}, SkillModes: map[string]string{"local": "manual"},
		}}
		if err := app.HandleSettingsSaveForSession(owner.SessionID, settings); err != nil {
			t.Fatal(err)
		}
	}
	configure(first, "first-model")
	configure(second, "second-model")
	firstBefore := app.BackendSession(first.SessionID).SettingsSnapshots()
	secondBefore := app.BackendSession(second.SessionID).SettingsSnapshots()
	app.ActivateSessionByAgent(secondID)
	// A display cache is never command authority, even if a prior save left it stale.
	app.LastSettings = firstBefore.Effective
	command := app.executePaletteCommand("agent.toggle.multiagent")
	if command == nil {
		t.Fatal("palette toggle did not prepare its operation")
	}
	app.ActivateSessionByAgent(firstID) // switch before the off-thread command runs
	runAppCommand(t, app, command)
	firstAfter := app.BackendSession(first.SessionID).SettingsSnapshots()
	secondAfter := app.BackendSession(second.SessionID).SettingsSnapshots()
	if !reflect.DeepEqual(firstBefore, firstAfter) || !reflect.DeepEqual(secondBefore.Overrides, secondAfter.Overrides) ||
		!reflect.DeepEqual(secondBefore.Base, secondAfter.Base) || secondBefore.Effective.Model != secondAfter.Effective.Model ||
		secondBefore.Effective.Agents.Enabled == secondAfter.Effective.Agents.Enabled {
		t.Fatal("palette toggle overwrote unrelated owner/settings or failed to toggle")
	}
	if app.ActiveSession != first || app.ModelName != "first-model" || app.LastSettings.Model != "first-model" {
		t.Fatal("late toggle completion stole the selected session")
	}
}

func TestSelectedSettingsResultCannotOverwriteAnotherSession(t *testing.T) {
	app := newFullTestApp(t)
	firstID := addBackendTestOrchestrator(t, app)
	first := app.SessionsByAgent[firstID]
	secondID := addBackendTestOrchestrator(t, app)
	second := app.SessionsByAgent[secondID]
	app.ActiveSession = second
	app.ModelName = "second-model"
	app.LastSettings = types.Settings{Model: "second-model"}
	operation, err := app.PrepareMultiAgentToggle(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result := selectedSessionSettingsResult{workspace: app.Workspace, owner: first,
		generation: app.workspaceSwitchGen.Load(), result: operation.Run()}
	app.applySelectedSessionSettings(result)
	if app.ModelName != "second-model" || app.LastSettings.Model != "second-model" || app.ActiveSession != second {
		t.Fatal("background settings result replaced selected session telemetry")
	}
	app.workspaceSwitchGen.Add(1)
	app.ActiveSession = first
	app.applySelectedSessionSettings(result)
	if app.ModelName != "second-model" {
		t.Fatal("old workspace generation applied settings")
	}
}
