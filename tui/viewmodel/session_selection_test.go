package viewmodel

import (
	"testing"

	"crude/shellport"
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
	shared := shellport.Local(owner)
	// This tests the selected-session seam, not multi-client event transport.
	left := &App{ws: shared, FocusStack: NewFocusStack()}
	right := &App{ws: shared, FocusStack: NewFocusStack()}
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

func TestSelectedSettingsResultCannotOverwriteAnotherSession(t *testing.T) {
	app := newFullTestApp(t)
	firstID := addBackendTestOrchestrator(t, app)
	first := app.ws.SessionsByAgent()[firstID]
	secondID := addBackendTestOrchestrator(t, app)
	second := app.ws.SessionsByAgent()[secondID]
	app.ActiveSession = second
	app.ModelName = "second-model"
	app.LastSettings = types.Settings{Model: "second-model"}
	operation, err := app.ws.PrepareMultiAgentToggle(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result := selectedSessionSettingsResult{workspace: app.ws, owner: first,
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
