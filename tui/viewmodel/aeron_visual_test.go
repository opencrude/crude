package viewmodel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/termdock"
	"crude/tui/ui"
)

// Run sequentially with scripts/tuishot.sh and a prebuilt viewmodel test image
// (CRUDE_VISUAL_AERON=1). The pointer rests on the file tab and F4 checks its
// tooltip. F3 releases the fixture provider and F4 checks Aeron's modal
// question over the unchanged main tab. Its own Cancel action closes it with the
// normal question behavior and F4 checks that it closed. F5 opens Workspace.
// Clicking its panel header link and F4
// checks its normal agent tab and composer. F9 checks all four and exits.
func TestAeronAndTabTooltipVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_AERON") != "1" {
		t.Skip("dedicated sequential Aeron and tooltip capture")
	}
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			aeronToolReply(w, "question", map[string]any{"questions": []map[string]any{{
				"header": "Install", "question": "Install the YAML language server for this user only?", "options": []string{"This user", "All users"},
			}}}, 1)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Setup stopped. Nothing was installed.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	file := filepath.Join(root, "settings.yaml")
	if err := os.WriteFile(file, []byte("name: crude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, server.URL), root)
	app := shell.app
	main := remoteJourneyCreate(t, shell)
	runAppCommand(t, app, app.ensureWorkspaceAeron())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.workspaceAeronAgent() != "" })
	if _, err := app.openEditorTab(file); err != nil {
		t.Fatal(err)
	}
	app.activateAgentWindowTab(main)
	app.Input.SetInput("Main session draft stays here.")
	runAppCommand(t, app, app.taskWorkspaceAeron("<tool_setup_task>Set up YAML</tool_setup_task>", "Set up 1 code intelligence tool: yaml-language-server."))
	model := &aeronVisualModel{App: app, release: func() { once.Do(func() { close(release) }) }, file: file}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	model.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	var pump sync.WaitGroup
	pump.Add(1)
	go func() {
		defer pump.Done()
		for {
			select {
			case message := <-shell.fifo:
				program.Send(message)
			case <-ctx.Done():
				return
			}
		}
	}()
	_, err := program.Run()
	cancel()
	pump.Wait()
	if err != nil {
		t.Fatal(err)
	}
	result := "AERON_VISUAL_SUCCESS: tooltip, modal question, closed, Aeron tab\n"
	if model.failure != nil || model.phase != 4 {
		result = fmt.Sprintf("AERON_VISUAL_FAILURE: reached %d/4 boundaries: %v\n", model.phase, model.failure)
	}
	if path := os.Getenv("CRUDE_AERON_VISUAL_RESULT"); path != "" {
		if err := os.WriteFile(path, []byte(result), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if model.failure != nil || model.phase != 4 {
		t.Fatal(result)
	}
}

type aeronVisualModel struct {
	*App
	release func()
	file    string
	phase   int
	failure error
}

func (m *aeronVisualModel) Init() tea.Cmd { return nil }

func (m *aeronVisualModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF3:
			m.release()
			return m, nil
		case tea.KeyF5:
			// Show Workspace without a pointer path the capture
			// cannot know in advance.
			m.toggleWorkspacePanel(termdock.PanelWorkspace)
			m.forceFrame()
			return m, nil
		case tea.KeyF4:
			ok := false
			draft := m.Input.Value() == "Main session draft stays here."
			// Aeron never takes the user's tab: it stays minimized and its
			// tab is not selected.
			aeron := m.workspaceAeronAgent()
			kept := m.AgentView.IsHidden(aeron) && m.Win.ActiveTab() != nil && m.Win.ActiveTab().Title != m.agentTabTitle(aeron)
			switch m.phase {
			case 0:
				// The painted tooltip, not only its state, must show the path.
				ok = m.overlayHost.IsOpen(ui.OverlayTooltip) && strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(tooltipText(m.App), "│", "")), ""), m.file)
			case 1:
				ok = m.overlayHost.IsOpen(ui.OverlayAgentPrompt) && len(m.agentPrompts) == 1 && !m.agentPrompts[0].inline &&
					kept && draft
			case 2:
				ok = len(m.agentPrompts) == 0 && !m.overlayHost.IsOpen(ui.OverlayAgentPrompt) && kept && draft
			case 3:
				ok = m.AgentView.ActiveID() == aeron && m.ActiveSession == m.ws.SessionsByAgent()[aeron] &&
					!strings.Contains(ansi.Strip(m.View().RenderContent()), "Give Aeron a task")
			}
			if !ok {
				m.failure = fmt.Errorf("phase %d: required state was not reached (prompt=%v prompts=%d active=%q kept=%v draft=%q)",
					m.phase, m.overlayHost.IsOpen(ui.OverlayAgentPrompt), len(m.agentPrompts), m.AgentView.ActiveID(), kept, m.Input.Value())
			} else {
				m.phase++
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(message)
	return m, command
}
