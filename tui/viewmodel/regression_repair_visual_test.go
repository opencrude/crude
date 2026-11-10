package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Native scripts/tuishot.sh journey, sequentially at 160x40 and 80x24.
// F2 opens Problems; Down/Right/End read its full message; F4 observes.
// F3 seeds a draft; ordinary Escape/Ctrl+Z clear and restore it.
// F5 opens Language Tools; wheel, F6, move outside, F6 verifies retained scroll.
// F9 checks the observations and exits. No provider or user data is used.
func TestRegressionRepairVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_REGRESSION_REPAIR") != "1" {
		t.Skip("requires the dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	ws := wsOf(app)
	ws.Logger = stubLogger{}
	restrictSyntheticFixture(t, app, t.TempDir())
	app.AddOrchestrator("draft-owner")
	app.ActiveSession = workspace.NewSession("draft-owner", t.TempDir(), nil, stubLogger{})
	ws.SessionsByAgent = map[string]*workspace.Session{"draft-owner": app.ActiveSession}
	app.maybeHydrateComposerDraft()
	ws.AgentMgr.AddOrchestrator("aeron-fixture", workspace.NewAgentState("aeron-fixture", nil, nil))
	ws.SessionsByAgent["aeron-fixture"] = &workspace.Session{
		SessionID: "aeron-fixture", AgentID: "aeron-fixture",
		Settings: &workspace.SessionSettingsState{Effective: types.Settings{SessionMainAgent: "aeron"}},
	}
	app.AgentView.SetHidden("aeron-fixture", true)
	app.setActivity(activityAgents, true)
	requireAeronAbsentFromAgentList(t, app)
	path := filepath.Join(app.ws.RootPath(), "example.go")
	app.lspFacts = map[string]types.InsightDiagnostics{path: {
		ID: "visual-problem",
		Diagnostics: []types.LSPDiagnostic{{
			Line: 14, Col: 0, Severity: types.DiagError, Source: "compiler",
			Message: strings.Repeat("The declaration is not valid. Remove this text or replace it with a declaration. ", 32) + "FINAL_SENTENCE.",
		}},
	}}
	model := &regressionRepairVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	result := fmt.Sprintf("REGRESSION_REPAIR expanded=%v clear=%v undo=%v scroll=%v\n",
		model.expanded, model.cleared, model.undone, model.scrolled)
	if path := os.Getenv("CRUDE_REGRESSION_REPAIR_RESULT"); path != "" {
		if err := os.WriteFile(path, []byte(result), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !model.expanded || !model.cleared || !model.undone || !model.scrolled {
		t.Fatal(result)
	}
}

const regressionRepairDraft = "Keep this draft after Escape.\nUndo restores the complete text."

type regressionRepairVisualModel struct {
	*App
	expanded, cleared, undone, scrolled bool
	scrollBefore                        int
}

func (m *regressionRepairVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *regressionRepairVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.toggleWorkspacePanel(termdock.PanelProblems)
			return m, nil
		case tea.KeyF3:
			if m.termDock != nil && m.termDock.IsOpen() {
				m.toggleWorkspacePanel(termdock.PanelProblems)
			}
			m.Input.SetInput(regressionRepairDraft)
			m.setFocusInput()
			return m, nil
		case tea.KeyF4:
			m.expanded = m.expanded || strings.Contains(ansi.Strip(m.View().RenderContent()), "FINAL_SENTENCE.")
			return m, nil
		case tea.KeyF5:
			m.openWorkspaceSettings()
			fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
			if body := m.settingsSurface(fw); body != nil {
				for i, category := range body.Model().Categories() {
					if category.Name == "Language Tools" {
						body.Model().SelectCategory(i)
						break
					}
				}
				body.SetModel(body.Model())
				if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				fw.MarkContentDirty()
			}
			return m, nil
		case tea.KeyF6:
			m.View()
			if fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings); fw != nil {
				if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
					if table, ok := frame.Host().Component("settings/language-servers/table").(*ui.Table); ok {
						offset := table.Scroll().Offset()
						if m.scrollBefore > 0 && offset == m.scrollBefore {
							m.scrolled = true
						}
						m.scrollBefore = offset
					}
				}
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.Code == tea.KeyEsc && m.Input.Value() == "" {
			m.cleared = true
		}
		if key.Code == 'z' && key.Mod.Contains(tea.ModCtrl) && m.cleared && m.Input.Value() == regressionRepairDraft {
			m.undone = true
		}
	}
	return m, cmd
}
