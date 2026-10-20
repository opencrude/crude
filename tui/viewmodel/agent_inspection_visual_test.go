package viewmodel

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/sessiondb"
	ctypes "crude/types"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh: F2 expands evidence, F3 rehydrates it, F9
// exits. This exercises the real chat without a provider or agent lifecycle.
func TestAgentInspectionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_AGENT_INSPECTION") != "1" {
		t.Skip("requires the dedicated tuishot terminal")
	}
	app := newEditorPaintApp(t, t.TempDir())
	app.width, app.height = 160, 40
	app.AddOrchestrator("inspection-observer")
	state := app.ws.AgentMgr().Get("inspection-observer").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleUser, Content: "Why was Mara working? Inspect her without opening or waking her session."})
	content := `{"agent":{"id":"mara","name":"Mara","state":"available","presence":"offline"},"run":null,"latest_run":{"id":"finished-run","agent_id":"mara","origin":"peer","state":"succeeded","outcome":"completed","version":2,"started_at":"2026-10-20T13:13:49Z","ended_at":"2026-10-20T13:15:57Z"},"trigger":{"available":true,"origin":"runtime","kind":"peer_note","preview":"Child cancelled during cleanup. No old work will resume.","event_id":"cancelled-child-event","sender_id":"old-child"},"recent_work":{"available":true,"scope":"latest_persisted_turn","turn":9,"generation":10,"messages":[{"role":"tool","tool_name":"execution.inspect","status":"success"},{"role":"assistant","text":"Checked the cancellation notice. No coding task resumed and no project files changed."}],"truncated":false},"reports":[]}`
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{"tool_id": "inspect", "internal_id": "inspect", "name": "agent_inspect", "args": `{"agent_id":"mara"}`}})
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{"tool_id": "inspect", "internal_id": "inspect", "name": "agent_inspect", "result": content}})
	for i := range 8 {
		state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
	}
	state.ApplyEvent(workspace.AgentEvent{Type: "done", Data: map[string]any{"reason": "completed"}})
	app.syncAgentMessages(state.ID, false)
	model := &agentInspectionVisualModel{App: app, owner: state.ID, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type agentInspectionVisualModel struct {
	*App
	owner string
	t     *testing.T
}

type agentInspectionVisualQuit struct{}

func (m *agentInspectionVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *agentInspectionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(agentInspectionVisualQuit); ok {
		return m, tea.Quit
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.subPaneProjections.GetExisting(m.owner).renderer.ToggleDisclosure("inspect")
			return m, nil
		case tea.KeyF3:
			state := m.ws.AgentMgr().Get(m.owner).State
			var rows []sessiondb.MessageWithMeta
			for _, message := range state.Messages.Messages() {
				rows = append(rows, sessiondb.MessageWithMeta{Msg: provider.ChatMessage{Role: message.Role}, Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{message}}})
			}
			m.subPaneProjections.GetExisting(m.owner).renderer.SyncMessages(ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(rows)), state.Messages.Version()+1)
			return m, nil
		case tea.KeyF9:
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte("INSPECTION_VISUAL_PASS\n"), 0600); err != nil {
					m.t.Error(err)
				}
			}
			// Give tuishot its final screenshot before a clean test exit.
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return agentInspectionVisualQuit{} })
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
