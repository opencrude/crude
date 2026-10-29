package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Driven through scripts/tuishot.sh with a private core and offline provider.
func TestModesVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_MODES") != "1" {
		t.Skip("dedicated terminal capture")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The scheduled check completed.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	shell.app.setFocusInput()
	model := &modesVisualModel{App: shell.app, t: t, id: id}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.planOn || !model.planOff || !model.goal || !model.loop || !model.goalView || !model.loopView {
		t.Fatalf("visual journey incomplete: %+v", model)
	}
}

type modesVisualModel struct {
	*App
	t                           *testing.T
	id                          string
	planOn, planOff, goal, loop bool
	goalView, loopView          bool
}

func (m *modesVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *modesVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" && m.planOn && m.planOff && m.goal && m.loop && m.goalView && m.loopView {
			if err := os.WriteFile(path, []byte("MODES_VISUAL_SUCCESS plan, goal, loop; connected private core\n"), 0600); err != nil {
				m.t.Error(err)
			}
		}
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if m.ws.PlanModeForAgent(m.id) {
		m.planOn = true
	} else if m.planOn {
		m.planOff = true
	}
	if _, ok := m.ws.GoalForAgent(m.id); ok {
		m.goal = true
	}
	if len(m.ws.LoopsForAgent(m.id)) > 0 {
		m.loop = true
	}
	if screen := m.sessionTextOverlay(); screen != nil && screen.IsOpen() {
		m.goalView = m.goalView || screen.Title() == "Goal"
		m.loopView = m.loopView || screen.Title() == "Scheduled loops"
	}
	return m, cmd
}
