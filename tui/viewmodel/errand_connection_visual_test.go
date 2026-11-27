package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/core/shellport"
	"crude/internal/buildinfo"
	"crude/tui/cell"
	"crude/tui/screens/termdock"
	"crude/tui/screens/workspacehub"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

// F2 opens Workspace. F3 observes the real panel link, never activates it.
// Ordinary pointer or Tab/Enter opens Errand. F4 observes its normal composer.
// F5 restores the original tab; wordmark hover opens version details.
// F6 observes both distinct identities; F9 exits with the proof.
func TestErrandPanelAndBuildsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_ERRAND_PANEL") != "1" {
		t.Skip("sequential native capture")
	}
	app := newFullTestApp(t)
	// The synthetic shell bypasses NewApp, which caches this at construction.
	// Capture this process's actual native hash, not an invented fixture value.
	app.tuiBuildID = buildinfo.ExecutableID()
	ws := wsOf(app)
	ws.Logger = stubLogger{}
	restrictSyntheticFixture(t, app, t.TempDir())
	app.AddOrchestrator("main")
	app.ActiveSession = workspace.NewSession("main", t.TempDir(), nil, stubLogger{})
	ws.SessionsByAgent = map[string]*workspace.Session{"main": app.ActiveSession}
	app.maybeHydrateComposerDraft()
	seedNamedErrandPanel(t, app)
	app.ws = &connectionHealthWorkspace{Workspace: app.ws, status: shellport.RemoteStatus{
		State: "connected", Carrier: "local", CoreBuildID: "c0dec0dec0dec0de"}}
	app.Input.SetInput("This draft stays with the main agent.")
	app.setActivity(activityAgents, true)
	model := &errandPanelVisual{App: app}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	restore := installDetachedMessageSinkForTest(app, p.Send)
	defer restore()
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	proof := fmt.Sprintf("ERRAND_PANEL placement=%v underlined=%v opened=%v builds=%v draft=%v\n", model.placed, model.underlined, model.opened, model.builds, app.Input.Value() == "This draft stays with the main agent.")
	if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
		if err := os.WriteFile(path, []byte(proof), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !model.placed || !model.underlined || !model.opened || !model.builds || app.Input.Value() != "This draft stays with the main agent." {
		t.Fatal(proof)
	}
}

type errandPanelVisual struct {
	*App
	placed, underlined, opened, builds bool
}

func (*errandPanelVisual) Init() tea.Cmd { return tickCmd() }
func (m *errandPanelVisual) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.toggleWorkspacePanel(termdock.PanelWorkspace)
			return m, nil
		case tea.KeyF3:
			m.View()
			// Narrow panels show the short label "Errand".
			rect, exists := m.screenRectOf(workspacehub.ErrandID)
			body := ansi.Strip(m.View().RenderContent())
			m.placed = exists && !rect.Empty() && rect.Y > m.headerScreen.Bounds().Y &&
				strings.Contains(rowText(m.App, rect.Y), "Errand") && !strings.Contains(body, "Naveen") &&
				!strings.Contains(rowText(m.App, m.frameTopRow()), "Errand")
			m.underlined = m.placed
			for i := range rect.W - 2 {
				m.underlined = m.underlined && m.mainSurf.Buffer().Get(rect.X+1+i, rect.Y).Attr.Has(cell.AttrUnderline)
			}
			return m, nil
		case tea.KeyF4:
			m.opened = m.AgentView.ActiveID() == "workspace-agent" && m.focusTarget() == FocusInput
			return m, nil
		case tea.KeyF5:
			m.restoreAgent("main")
			m.setFocusInput()
			return m, nil
		case tea.KeyF6:
			text := connectionPopoverText(m.App)
			m.builds = len(m.tuiBuildID) == 16 && strings.Contains(text, "TUI build") && strings.Contains(text, m.tuiBuildID) &&
				strings.Contains(text, "Core build") && strings.Contains(text, "c0dec0dec0dec0de")
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
