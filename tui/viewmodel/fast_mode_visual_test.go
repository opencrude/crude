package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/session"
	"crude/types"
)

// Sequential scripts/tuishot.sh journey: type /fast twice, capturing the
// acknowledgement and the settled guidance row between toggles, then F9.
// The framed core and inference endpoint are private, deterministic fixtures.
func TestFastModeVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_FAST") != "1" {
		t.Skip("dedicated terminal capture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"I’ll review the current implementation and keep the result focused and clear.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
		profile := cfg.Agents.Profiles["default"]
		main := profile.Categories[session.CategoryDefault]
		main.Model = "gpt-6.1-terra"
		profile.Categories[session.CategoryDefault] = main
		cfg.Agents.Profiles["default"] = profile
	})
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	runAppCommand(t, shell.app, shell.app.submitPreparedInput(id, provider.PlainInput("Please review the current implementation."), shell.app.Input.DraftRevision()))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return shell.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	shell.app.setFocusInput()
	model := &fastModeVisualModel{App: shell.app, t: t, id: id}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.on || !model.off {
		t.Fatalf("visual journey did not complete both toggles: on=%t off=%t", model.on, model.off)
	}
}

type fastModeVisualModel struct {
	*App
	t       *testing.T
	id      string
	on, off bool
}

func (m *fastModeVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *fastModeVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" && m.on && m.off {
			if err := os.WriteFile(path, []byte("FAST_VISUAL_SUCCESS on and off; connected private core\n"), 0600); err != nil {
				m.t.Error(err)
			}
		}
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if result, ok := msg.(selectedSessionSettingsResult); ok && result.result.Err == nil {
		if owner := m.ws.SessionForAgent(m.id); owner != nil && owner.Settings != nil {
			if owner.Settings.Effective.FastMode == types.FastModeOn {
				m.on = true
			} else if m.on && owner.Settings.Effective.FastMode == types.FastModeOff {
				m.off = true
			}
		}
	}
	return m, cmd
}
