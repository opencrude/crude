package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/types/statuscenter"
)

// Dedicated scripts/tuishot.sh acceptance. Every backend, project and shell is
// test-owned; interaction enters the real connected composer and /compact path.
func TestCompactionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPACTION") != "1" {
		t.Skip("dedicated terminal capture")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	gate := providersim.NewGate()
	var normal, extraction atomic.Int32
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		text := "Initial work completed. Full source remains archived."
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("Retain accepted work and continue with queued human input.").WithGate(gate)
		} else if normal.Add(1) > 1 {
			text = "Queued prompt executed exactly once after compaction."
		}
		return simulatedAnswer(text)
	})
	runtime := remoteJourneyRuntime(t, server.BaseURL(providersim.Chat))
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	t.Cleanup(gate.Release)
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	runAppCommand(t, shell.app, shell.app.submitPreparedInput(id, provider.UserInput{
		Text:        "Retain the complete source. " + strings.Repeat("accepted source evidence ", 3_000),
		DisplayText: "Review the saved source evidence without deleting prior work.",
	}, shell.app.Input.DraftRevision()))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted })
	model := &compactionVisualModel{App: shell.app, t: t, id: id, normal: &normal, extraction: &extraction, release: gate.Release}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type compactionVisualModel struct {
	*App
	t                  *testing.T
	id                 string
	normal, extraction *atomic.Int32
	release            func()
}

func (m *compactionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			m.release()
			return m, nil
		case tea.KeyF9:
			if m.normal.Load() != 2 || m.extraction.Load() != 4 || m.Input.Value() != "Private draft remains" || m.ws.AgentMgr().Get(m.id).State.ActiveCompaction.Phase != "" {
				m.t.Errorf("visual journey did not settle: normal=%d extraction=%d draft=%q", m.normal.Load(), m.extraction.Load(), m.Input.Value())
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("compaction visual acceptance; failed=%v\n", m.t.Failed())), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
