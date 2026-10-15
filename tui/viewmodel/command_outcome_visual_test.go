package viewmodel

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/collaboration"
	"crude/types/command"
	"crude/workspace"
)

// Opt-in production-shell capture with no provider calls or real user data.
// Use sequential scripts/tuishot.sh; F2 expands evidence and F9 quits.
func TestCommandOutcomeVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMMAND_OUTCOMES") != "1" {
		t.Skip("requires a dedicated visual capture terminal")
	}
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width, app.height = 160, 40
	app.AddOrchestrator("outcome-owner")
	state := app.ws.AgentMgr().Get("outcome-owner").State
	state.Messages.AppendMessage(workspace.Message{Role: "user", Content: "Verify background command completion."})
	state.Messages.AppendMessage(workspace.Message{InternalID: "build-call", Role: "tool", ToolName: "async_execute_command",
		ToolCommand: "go build ./...", ToolStatus: "success", ToolPreviewPhase: "done"})
	for _, info := range []command.Outcome{
		{Handle: "settings-repro-build", Command: "go build ./...", PID: 41, Status: "completed", Duration: 4 * time.Second, ExitKnown: true, OutputBytes: 14},
		{Handle: "tests", Command: "go test ./...", PID: 42, Status: "failed", Duration: 6 * time.Second, ExitKnown: true, ExitCode: 7, OutputBytes: 120, Error: "one test failed"},
	} {
		state.ApplyEvent(workspace.AgentEvent{Type: "collaboration_message", Data: collaboration.Event{
			ID: "outcome-" + info.Handle, Kind: collaboration.EventCommandOutcome, Origin: collaboration.OriginRuntime,
			SenderName: "Background command " + info.Handle, Command: &info,
			Body: "Model-only runtime guidance: read_async_output; do not rerun completed effects.",
		}})
	}
	app.syncAgentMessages(state.ID, false)
	model := &commandOutcomeVisualModel{App: app, ownerID: state.ID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type commandOutcomeVisualModel struct {
	*App
	ownerID string
}

func (m *commandOutcomeVisualModel) Init() tea.Cmd { return nil }

func (m *commandOutcomeVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.subPaneProjections.GetExisting(m.ownerID).renderer.ToggleDisclosure("outcome-settings-repro-build")
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
