package viewmodel

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
	"crude/types"
	"crude/types/recovery"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh fixture: no provider calls or real user data.
// F2 attention, F3 recovered, F4 Network Connection, F5 Proxy, F6 Certificates,
// F7 theme switch, F8 return to chat, F9 quit.
func TestRunRecoveryVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_RECOVERY") != "1" {
		t.Skip("requires the repository's dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width, app.height = 160, 40
	app.AddOrchestrator("visual-recovery")
	state := app.AgentMgr.Get("visual-recovery").State
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Time: time.Now(), Content: "Complete the refactor and verify it, keeping the accepted edits."})
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleAssistant, Time: time.Now(), Content: "The refactor is saved. Verification is still pending; recovery will continue from these exact results."})
	app.maybeHydrateComposerDraft()
	app.Input.SetInput("My next instruction is still a draft. Recovery must not consume it.")
	model := &runRecoveryVisualModel{App: app, status: recovery.Status{
		ID: "visual-incident", AgentID: state.ID, Generation: 1,
		StartedAt: time.Now(), UpdatedAt: time.Now(), Attempt: 7,
	}}
	model.setRecovery(recovery.Recovering)
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type runRecoveryVisualModel struct {
	*App
	status recovery.Status
	light  bool
}

func (model *runRecoveryVisualModel) Init() tea.Cmd { return nil }

func (model *runRecoveryVisualModel) setRecovery(state recovery.State) {
	model.status.State, model.status.UpdatedAt = state, time.Now()
	model.status.RetryAt = time.Time{}
	switch state {
	case recovery.Recovering:
		model.status.Summary = "The provider is temporarily unavailable"
		model.status.Detail = "HTTP 503: The upstream gateway interrupted the connection. Accepted output and completed file changes are retained; no historical tool will be replayed."
		model.status.Action = "Recovery continues automatically. Stop remains available."
		model.status.RetryAt = time.Now().Add(5 * time.Minute)
	case recovery.NeedsAttention:
		model.status.Summary = "The selected account needs attention"
		model.status.Detail = "HTTP 401: Authentication was rejected for the configured endpoint. Your unfinished run and accepted results are kept; Crude will not switch accounts or repeatedly send invalid requests."
		model.status.Action = "Update Accounts, then Continue run from the menu or use /retry."
	case recovery.Recovered:
		model.status.Summary = "The run recovered"
		model.status.Detail = "The connection is healthy and the completed verification was saved."
		model.status.Action = ""
	}
	_ = model.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: model.status.AgentID, Data: model.status})
	if state == recovery.Recovered {
		_ = model.handleAgentEvent(types.AgentEventMsg{Type: "done", AgentID: model.status.AgentID, Data: map[string]any{"reason": "completed", "content": "Verification passed."}})
	}
}

func (model *runRecoveryVisualModel) selectNetworkPage(label string) {
	if body := model.settingsSurface(model.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
		for _, item := range body.Model().CategoryItems() {
			if item.Label == label {
				body.Model().SelectCategory(item.Payload.(int))
				body.SetModel(body.Model())
				window := model.overlayHost.Window(ui.OverlayWorkspaceSettings)
				if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				window.MarkContentDirty()
				return
			}
		}
	}
}

func (model *runRecoveryVisualModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			model.setRecovery(recovery.NeedsAttention)
			return model, nil
		case tea.KeyF3:
			model.setRecovery(recovery.Recovered)
			return model, nil
		case tea.KeyF4:
			model.openNetworkSettings()
			return model, nil
		case tea.KeyF5:
			model.selectNetworkPage("Proxy")
			return model, nil
		case tea.KeyF6:
			model.selectNetworkPage("Certificates")
			return model, nil
		case tea.KeyF7:
			model.light = !model.light
			preset := "dark"
			if model.light {
				preset = "light"
			}
			model.ApplyThemePreset(preset)
			return model, nil
		case tea.KeyF8:
			model.overlayHost.Close(ui.OverlayWorkspaceSettings)
			return model, nil
		case tea.KeyF9:
			return model, tea.Quit
		}
	}
	_, command := model.App.Update(message)
	return model, command
}
