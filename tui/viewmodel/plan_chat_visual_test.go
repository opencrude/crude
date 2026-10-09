package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/cell"
	"crude/tui/ui"
	"crude/types"
	"crude/types/activity"
	"crude/types/recovery"
	"crude/workspace"
)

// Run sequentially through scripts/tuishot.sh. Data is isolated/seeded; normal
// Host pointer, scroll, hover, stop/recovery event and compositor routes remain.
func TestPlanChatVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_PLAN_CHAT") != "1" {
		t.Skip("dedicated terminal capture")
	}
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Tree.Visible = false
	app.AddOrchestrator("visual-plan")
	state := app.ws.AgentMgr().Get("visual-plan").State
	state.Running = true
	for i := range 12 {
		state.Messages.AppendMessage(workspace.Message{Role: types.RoleAssistant, Content: fmt.Sprintf("### Accepted result %d\n\nChecked state ownership and retained completed work. The draft stays untouched while browsing the plan and older messages.", i+1), Time: time.Now()})
	}
	app.syncAgentMessages("visual-plan", false)
	app.Input.SetInput("Draft stays here")
	p := hoverPlanFixture("Make plans useful and chat easier to read", 4)
	names := []string{"Trace current behavior", "Refine plan updates", "Simplify chat navigation", "Verify recovery and UI"}
	for i := range p.Steps {
		p.Steps[i].Text = names[i]
	}
	p.Current = []activity.Step{p.Steps[1]}
	wsOf(app).Status.SetAgent(types.AgentStatusSnapshot{Ref: types.ResourceRef{ID: "visual-plan"}, Phase: types.AgentTool, Detail: "Working"})
	model := &planChatVisualModel{App: app, t: t, plan: p}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type planChatVisualModel struct {
	*App
	t                   *testing.T
	plan                *activity.Plan
	light               bool
	sawLatest, sawHover bool
}

func (*planChatVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *planChatVisualModel) View() tea.View {
	m.compositor.BeginFrame()
	m.compositor.Root().Buffer().Fill(cell.BlankCell(m.rootHost.Tokens().Color(ui.ColorBg)))
	m.compositor.Root().AddChild(m.mainSurf)
	m.renderCtx.SpinFrame = m.SpinFrame
	m.syncRenderFocus()
	paintHoverFixture(m.App, map[string]*activity.Plan{"visual-plan": m.plan})
	m.sawLatest = m.sawLatest || bufferContainsPlain(m.mainSurf.Buffer(), "Latest")
	m.sawHover = m.sawHover || m.planHover != nil
	m.windowManager.Render(m.compositor.Root(), m.renderCtx)
	m.compositor.Composite()
	return m.newTerminalView(m.compositor.Present())
}

func (m *planChatVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.clearPlanHover()
			s := recovery.Status{ID: "visual-recovery", AgentID: "visual-plan", Generation: 1, State: recovery.Recovering, Attempt: 1,
				Summary: "Connection to the provider was interrupted", Detail: "stream error: stream disconnected before completion: stream closed before response.completed", Action: "Recovery continues automatically. Accepted output stays intact.", StartedAt: time.Now(), UpdatedAt: time.Now(), RetryAt: time.Now().Add(time.Minute)}
			m.handleAgentEvent(types.AgentEventMsg{AgentID: "visual-plan", Type: "recovery", Data: s})
			return m, nil
		case tea.KeyF3:
			m.cancelAgentTurn("visual-plan")
			m.handleAgentEvent(types.AgentEventMsg{AgentID: "visual-plan", Type: "recovery", Data: recovery.Status{ID: "visual-recovery", AgentID: "visual-plan", Generation: 1, State: recovery.Cancelled, Summary: "Run cancelled", Detail: "retained original diagnostic", UpdatedAt: time.Now()}})
			m.handleAgentEvent(types.AgentEventMsg{AgentID: "visual-plan", Type: "done", Data: map[string]any{"reason": "cancelled"}})
			return m, nil
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF8:
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				s := m.chatScreen.ScrollModel()
				if err := os.WriteFile(path+".state", []byte(fmt.Sprintf("scroll offset=%d max=%d content=%d viewport=%d auto=%v bounds=%+v\n%s", s.Offset(), s.MaxOffset(), s.Content(), s.Viewport(), s.AutoScroll(), m.chatScreen.Bounds(), paneJourneyText(m.App, m.chatRect))), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, nil
		case tea.KeyF9:
			if m.Input.Value() != "Draft stays here" || m.rootHost.HasPointerCapture() {
				m.t.Error("visual journey lost draft or left pointer captured")
			}
			if text := chatAreaText(m.App); strings.Contains(text, "Stopped by you") || strings.Contains(text, "Recovery stopped") {
				m.t.Error("redundant stop notice painted")
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("plan-chat visual journey completed; failed=%v latest=%v hover=%v\n", m.t.Failed(), m.sawLatest, m.sawHover)), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
