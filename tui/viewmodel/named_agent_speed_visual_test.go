package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/session"
	"crude/types"
)

// The capture uses the real session deck and keyboard. F9 exits only after
// the named agent's Fast selection was applied to the session draft.
func TestNamedAgentSpeedVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_NAMED_SPEED") != "1" {
		t.Skip("dedicated terminal capture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
		cfg.Agents.AgentProfiles["reviewer"] = types.AgentProfileSettings{Category: "researcher", Model: "review-model"}
	})
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	remoteJourneyCreate(t, shell)
	shell.app.openSessionSettings()
	model := &namedAgentSpeedVisualModel{App: shell.app, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.applied {
		t.Fatal("native journey did not apply named agent's fast setting")
	}
}

type namedAgentSpeedVisualModel struct {
	*App
	t       *testing.T
	applied bool
}

func (m *namedAgentSpeedVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *namedAgentSpeedVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		if screen := m.sessionSettings(); screen != nil {
			m.applied = screen.ToSettings().SessionOverrides["profile/reviewer"].ServiceTier == "fast"
		}
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
