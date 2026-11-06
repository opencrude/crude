package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	ctypes "crude/types"
	"crude/workspace"
)

// Opt-in fixture for scripts/tuishot.sh with the compiled viewmodel test binary:
// CRUDE_VISUAL_CHAT_FIND=1 ... -test.run=^TestChatFindVisualAcceptance$
// It shows a long conversation with Markdown, a wrapped phrase and a streaming
// tail. Drive it with the real keys: Ctrl+F, type "viewport", Enter,
// Shift+Enter, Esc. F2 appends a streaming message; F9 exits.
func TestChatFindVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CHAT_FIND") != "1" {
		t.Skip("requires the dedicated tuishot terminal")
	}
	app := newEditorPaintApp(t, t.TempDir())
	app.registerOverlayHandlers()
	// The fixture starts after dismissing the unrelated trust question.
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.width, app.height = 160, 40
	app.ApplyThemePreset("dark")
	app.AddOrchestrator("find-audit")
	state := app.ws.AgentMgr().Get("find-audit").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleUser, Content: "Why does the chat jump when I scroll a long history?"})
	for i := 0; i < 120; i++ {
		body := fmt.Sprintf("Reply %d keeps **the transcript** steady while the history grows.\n\n- item with `inline code` number %d\n- the scroll model owns the offset", i, i)
		if i == 17 {
			body = "The virtual list measures only the viewport and trusts the cached height of every row above it, so the scroll model and the renderer must agree about wrapping when a very long sentence reaches the edge of a narrow column."
		}
		if i == 64 {
			body = "Only rows inside the viewport are drawn; rows above it keep an estimated height until the renderer settles them."
		}
		state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant, Content: body})
	}
	app.syncAgentMessages(state.ID, false)
	model := &chatFindVisualModel{App: app, owner: state.ID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type chatFindVisualModel struct {
	*App
	owner string
	n     int
}

func (m *chatFindVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *chatFindVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.n++
			state := m.ws.AgentMgr().Get(m.owner).State
			state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant, Content: strings.Repeat("streamed viewport ", m.n)})
			m.syncAgentMessages(m.owner, false)
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
