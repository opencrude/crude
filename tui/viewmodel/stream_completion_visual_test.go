package viewmodel

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent"
	"crude/types"
	"crude/types/recovery"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh fixture. F2 streams a multiline recovered reply,
// F3 settles recovery then completion, F4 rehydrates the accepted source, F9 exits.
// It never contacts a provider or reads a real saved conversation.
func TestStreamCompletionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_STREAM_COMPLETION") != "1" {
		t.Skip("requires the dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Tree.Visible = false
	app.AddOrchestrator("stream-owner")
	for _, message := range seedBudgetSession(80, 0) {
		app.appendActiveMessage(message)
	}
	app.Input.SetInput("This next draft must stay unsent.")
	m := &streamCompletionVisualModel{App: app, t: t}
	program := tea.NewProgram(m, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !m.completed || !m.hydrated {
		t.Fatal("capture did not exercise completion and rehydration")
	}
}

type streamCompletionVisualTick struct{}

type streamCompletionVisualModel struct {
	*App
	t                   *testing.T
	status              recovery.Status
	position            int
	completed, hydrated bool
}

func (m *streamCompletionVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *streamCompletionVisualModel) emit(kind string, data any) tea.Cmd {
	_, cmd := m.App.Update(agent.AgentEvent{AgentID: "stream-owner", Type: kind, Data: data})
	return cmd
}

func (m *streamCompletionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			if m.status.ID != "" {
				return m, nil
			}
			m.emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "test-model"})
			m.status = recovery.Status{ID: "stream-incident", AgentID: "stream-owner", Generation: 1,
				State: recovery.Recovering, StartedAt: time.Now(), UpdatedAt: time.Now(), Summary: "Retrying unfinished run"}
			m.emit("recovery", m.status)
			m.emit("phase", map[string]any{"name": "preparing", "request_id": "accepted-response"})
			return m, func() tea.Msg { return streamCompletionVisualTick{} }
		case tea.KeyF3:
			if m.position != len(streamParityAnswer) || m.completed {
				return m, nil
			}
			m.status.State, m.status.Summary, m.status.UpdatedAt = recovery.Recovered, "Run recovered", time.Now()
			m.emit("recovery", m.status)
			cmd := m.emit("done", map[string]any{"reason": "completed", "content": streamParityAnswer})
			var answers []string
			for _, message := range m.ws.AgentMgr().Get("stream-owner").State.Messages.Messages() {
				if message.Role == types.RoleAssistant && message.TurnGeneration == 1 {
					answers = append(answers, message.Content)
				}
			}
			if len(answers) != 1 || answers[0] != streamParityAnswer || m.Input.Value() != "This next draft must stay unsent." {
				m.t.Fatalf("completion repeated response or consumed draft: %q", answers)
			}
			m.completed = true
			return m, cmd
		case tea.KeyF4:
			if !m.completed {
				return m, nil
			}
			state := m.ws.AgentMgr().Get("stream-owner").State
			messages := state.Messages.Messages()
			order := make([]string, len(messages))
			for i := range messages {
				order[i] = messages[i].InternalID
			}
			if err := state.Messages.InstallDelta(workspace.ConversationDelta{Reset: true, Revision: state.Messages.Version(), Order: order, Upserts: messages}); err != nil {
				m.t.Fatal(err)
			}
			m.syncAgentMessages("stream-owner", true)
			m.forceFrame()
			m.hydrated = true
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	if _, ok := msg.(streamCompletionVisualTick); ok {
		end := min(m.position+7, len(streamParityAnswer))
		cmd := m.emit("stream_token", streamParityAnswer[m.position:end])
		m.position = end
		if end < len(streamParityAnswer) {
			return m, tea.Batch(cmd, tea.Tick(55*time.Millisecond, func(time.Time) tea.Msg { return streamCompletionVisualTick{} }))
		}
		return m, cmd
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
