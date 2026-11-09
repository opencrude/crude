package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
	"crude/types"
	"crude/types/toolresult"
	"crude/workspace"
)

// Called only by the opt-in tuishot branch of the real failure journey. Live
// and resumed data come from its actual Session/SQLite run, not strings copied
// from a screenshot. F2 discloses, F3 switches to cold data, F4 shows the batch.
func runCompactResultVisual(t *testing.T, live, resumed [][]types.ChatMessage) {
	root := filepath.Dir(live[0][0].ToolArgs)
	app := newEditorPaintApp(t, root)
	wsOf(app).RootPath = root
	app.subPaneProjections.SetProjectRoot(root)
	app.Tree.Visible = false
	app.AddOrchestrator("result-visual")
	model := &compactResultVisualModel{App: app, t: t, live: live, resumed: resumed}
	model.install(live[0], "Failed file operations")
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type compactResultVisualModel struct {
	*App
	t             *testing.T
	live, resumed [][]types.ChatMessage
	batch         int
	cold, light   bool
	disclosures   int
}

func (m *compactResultVisualModel) install(messages []types.ChatMessage, title string) {
	renderer := m.subPaneProjections.GetExisting("result-visual").renderer
	rows, _, _ := renderer.ExportMessageRows(m.width)
	for _, row := range rows {
		if row.ToggleAny {
			renderer.ToggleDisclosure(row.ID)
		}
	}
	state := m.ws.AgentMgr().Get("result-visual").State
	snapshots := []workspace.Message{{InternalID: "visual-request", Role: types.RoleUser, Content: title}}
	order := []string{"visual-request"}
	for _, message := range messages {
		snapshots = append(snapshots, workspaceMessageFromChat(message))
		order = append(order, message.InternalID)
	}
	if err := state.Messages.InstallDelta(workspace.ConversationDelta{Reset: true, Revision: state.Messages.Version() + 1, Upserts: snapshots, Order: order}); err != nil {
		m.t.Fatal(err)
	}
	m.syncAgentMessages("result-visual", false)
	// These fixture keys replace source data outside App.Update. Publish the
	// same immediate frame request that the real input path normally owns.
	m.presentImmediateFrame()
}

func (m *compactResultVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *compactResultVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		// Fixture commands intercept App.Update, so they must publish the
		// same input damage as the real key path. Frame urgency alone is not
		// source damage and cannot update a retained Host surface.
		defer m.invalidateMessageRegions(msg)
		switch key.Code {
		case tea.KeyF2:
			m.requirePainted([]string{"Edit File failed", "Read File failed"}, []string{"var Missing = true", "hint:"})
			renderer := m.subPaneProjections.GetExisting("result-visual").renderer
			rows, _, _ := renderer.ExportMessageRows(m.width)
			for _, row := range rows {
				if row.Toggle {
					renderer.ToggleDisclosure(row.ID)
					break
				}
			}
			m.disclosures++
			m.presentImmediateFrame()
			return m, nil
		case tea.KeyF3:
			m.requirePainted([]string{"var Missing = true"}, []string{"hint:", "copy full lines", "verify the exact content"})
			m.cold = !m.cold
			if m.cold {
				m.install(m.resumed[m.batch], "Failed file operations")
			} else {
				m.install(m.live[m.batch], "Failed file operations")
			}
			return m, nil
		case tea.KeyF4:
			m.requirePainted([]string{"var Missing = true"}, []string{"hint:", "copy full lines", "verify the exact content"})
			m.batch = 1 - m.batch
			if m.cold {
				m.install(m.resumed[m.batch], "Failed parallel operations")
			} else {
				m.install(m.live[m.batch], "Failed parallel operations")
			}
			return m, nil
		case tea.KeyF5:
			m.requirePainted([]string{"Apply Patch failed", "Read File failed"}, []string{"hint:"})
			m.install([]types.ChatMessage{
				{InternalID: "unknown", Role: types.RoleTool, ToolName: "apply_patch", ToolArgs: "target.go",
					ToolStatus: types.ToolStatusError, ToolOutcome: toolresult.Outcome{State: toolresult.Uncertain, Summary: "Effects could not be verified."}, Content: "Retained diagnostic evidence from an interrupted operation."},
				{InternalID: "denied", Role: types.RoleTool, ToolName: "write_file", ToolArgs: "target.go",
					ToolStatus: types.ToolStatusError, ToolOutcome: toolresult.Outcome{State: toolresult.Denied, Summary: "The user did not approve this change."}, Content: "No file changed."},
			}, "Uncertain and denied operations")
			return m, nil
		case tea.KeyF7:
			m.requirePainted([]string{"outcome unknown", "Write File denied"}, []string{"hint:"})
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			if m.overlayHost.Top() != nil || m.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
				m.t.Error("visual acceptance was obscured by a modal")
			}
			m.requirePainted([]string{"outcome unknown", "Write File denied"}, []string{"hint:"})
			if m.disclosures != 2 || !m.cold || m.batch != 1 || !m.light {
				m.t.Error("the visual journey did not exercise each required state")
			}
			if proof := os.Getenv("CRUDE_VISUAL_PROOF"); proof != "" {
				if err := os.WriteFile(proof, []byte("COMPACT_RESULTS_VISUAL_PASSED\n"), 0o600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

// Read the last presented shell surface. Do not call paintShellFrame or a
// renderer here: a fresh projection could conceal a missed real repaint.
func (m *compactResultVisualModel) requirePainted(present, absent []string) {
	text := strings.ToLower(bufferPlain(m.mainSurf.Buffer()))
	for _, want := range present {
		if !strings.Contains(text, strings.ToLower(want)) {
			m.t.Errorf("the presented screen is missing %q", want)
		}
	}
	for _, unwanted := range absent {
		if strings.Contains(text, strings.ToLower(unwanted)) {
			m.t.Errorf("the presented screen contains %q", unwanted)
		}
	}
}
