package viewmodel

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

// Opt-in fixture for scripts/tuishot.sh with the compiled viewmodel test binary:
// CRUDE_VISUAL_STREAM=1 ... -test.run=^TestStreamSeparatorVisualAcceptance$
// It replays the event shape of inclusionai/ling-3.1 over a long transcript.
// F2 streams a thought that overflows its pane, F3 sends the answer and three
// parallel reads separated by late "\n" deltas, F4 settles the reads. F9 exits.
func TestStreamSeparatorVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_STREAM") != "1" {
		t.Skip("requires the dedicated tuishot terminal")
	}
	app := newEditorPaintApp(t, t.TempDir())
	app.registerOverlayHandlers()
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.width, app.height = 160, 40
	app.ApplyThemePreset("dark")
	app.AddOrchestrator("ling")
	state := app.ws.AgentMgr().Get("ling").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleUser, Content: "Redesign the website."})
	for i := range 40 {
		state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant,
			Content: fmt.Sprintf("Earlier reply %d keeps **its wrapping** steady while a thought streams below; this sentence is long enough to reach the right edge.", i)})
	}
	app.syncAgentMessages(state.ID, false)
	model := &streamSeparatorVisualModel{App: app, owner: state.ID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type streamSeparatorVisualModel struct {
	*App
	owner string
}

func (m *streamSeparatorVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *streamSeparatorVisualModel) event(typ string, data any) {
	m.handleAgentEvent(ctypes.AgentEventMsg{AgentID: m.owner, Type: typ, Data: data})
}

func (m *streamSeparatorVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		ids := []string{"eslint.config.js", "design-system.lint.json", "src/main.tsx"}
		switch key.Code {
		case tea.KeyF2:
			for line := range 30 {
				m.event("thinking", reasoning.Delta{Text: fmt.Sprintf("Considering step %d of the redesign and what the lint rules allow.\n", line), Kind: reasoning.Raw})
			}
			return m, nil
		case tea.KeyF3:
			m.event("stream_token", "Before designing, let me check the lint rules and establish a green baseline.")
			m.event("stream_token", "\n")
			for i, id := range ids {
				if i > 0 {
					m.event("stream_token", "\n")
				}
				m.event("tool_call_streaming", map[string]any{"tool_id": id, "internal_id": id, "name": "read_file", "path": "site/" + id})
			}
			for _, id := range ids {
				m.event("tool_call", map[string]any{"tool_id": id, "internal_id": id, "request_id": "q1", "name": "read_file", "args": fmt.Sprintf(`{"path":"site/%s"}`, id)})
			}
			return m, nil
		case tea.KeyF4:
			for _, id := range ids {
				m.event("tool_result", map[string]any{"tool_id": id, "internal_id": id, "name": "read_file", "result": "export default {}"})
			}
			state := m.ws.AgentMgr().Get(m.owner).State
			for i := range 3 {
				state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
			}
			m.syncAgentMessages(m.owner, false)
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
