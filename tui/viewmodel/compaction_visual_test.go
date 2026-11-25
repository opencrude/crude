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
	"crude/workspace"
)

// The capture driver sends /compact and the queued prompt through the composer.
// F6 releases the provider, F7 shuts down the first shell/core, and F9 verifies
// the cold-resumed session and exits. Each stage signals its painted evidence.
func TestCompactionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPACTION") != "1" {
		t.Skip("dedicated terminal capture")
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	gate := providersim.NewGate()
	var normal, extraction atomic.Int32
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		text := "Initial work completed. Full source remains archived."
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			return simulatedAnswer("<<<intent>>>\nKeep the request.\n<<<work>>>\nSource retained.\n<<<state>>>\nCheckpoint ready.\n<<<continuation>>>\nContinue with the next human input.").WithGate(gate)
		} else if normal.Add(1) > 1 {
			text = "Queued prompt executed exactly once after compaction."
		}
		return simulatedAnswer(text)
	})
	endpoint := server.BaseURL(providersim.Chat)
	runtime := remoteJourneyRuntime(t, endpoint, func(w *workspace.Workspace) { restrictFixture(t, w) })
	shell := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(gate.Release)
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	runAppCommand(t, shell.app, shell.app.submitPreparedInput(id, provider.UserInput{
		Text:        "Retain the complete source. " + strings.Repeat("accepted source evidence ", 3_000),
		DisplayText: "Review the saved source evidence without deleting prior work.",
	}, shell.app.Input.DraftRevision()))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted })
	evidence := os.Getenv("CRUDE_CAPTURE_EVIDENCE")
	live := &compactionVisualModel{App: shell.app, id: id, release: gate.Release, evidence: evidence}
	runCompactionVisualProgram(t, live)
	if live.phase != "settled" || normal.Load() != 2 || extraction.Load() != 1 || live.Input.Value() != "Private draft remains" || live.ws.AgentMgr().Get(id).State.ActiveCompaction.Phase != "" {
		t.Fatalf("live journey not settled: phase=%q normal=%d extraction=%d draft=%q", live.phase, normal.Load(), extraction.Load(), live.Input.Value())
	}

	resumed := coldResumeCompactionJourney(t, runtime, []remoteJourneyShell{shell}, root, endpoint)
	resumed.app.Tree.Visible = false
	revived := &compactionVisualModel{App: resumed.app, id: resumed.app.AgentView.ActiveID(), revived: true, evidence: evidence}
	runCompactionVisualProgram(t, revived)
	revived.StopAsyncIO()
	if err := revived.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	if revived.phase != "revived" || live.settledTitle != revived.settledTitle || normal.Load() != 2 || extraction.Load() != 1 {
		t.Fatalf("cold resume changed the checkpoint or replayed work: live=%q revived=%q normal=%d extraction=%d", live.settledTitle, revived.settledTitle, normal.Load(), extraction.Load())
	}
	if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
		proof := fmt.Sprintf("PASS compaction native\nlive grid: %dx%d\nactive: %s\nsettled: %s\nrevived grid: %dx%d\nrevived: %s\n", live.width, live.height, live.activeTitle, live.settledTitle, revived.width, revived.height, revived.settledTitle)
		if err := os.WriteFile(path, []byte(proof), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func runCompactionVisualProgram(t *testing.T, model *compactionVisualModel) {
	t.Helper()
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	model.SetProgram(program)
	defer model.SetProgram(nil)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if model.evidenceErr != nil {
		t.Fatal(model.evidenceErr)
	}
}

type compactionVisualModel struct {
	*App
	id, phase, evidence       string
	activeTitle, settledTitle string
	release                   func()
	revived, sawSize          bool
	evidenceErr               error
}

type compactionVisualEvidenceError struct{ err error }

func (m *compactionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case compactionVisualEvidenceError:
		m.evidenceErr = msg.err
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.sawSize = true
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyF6:
			if m.release != nil {
				m.release()
			}
			return m, nil
		case tea.KeyF7:
			if !m.revived {
				return m, tea.Quit
			}
		case tea.KeyF9:
			if m.revived {
				return m, tea.Quit
			}
		}
	}
	_, cmd := m.App.Update(msg)
	return m, tea.Batch(cmd, m.observePaint())
}

func (m *compactionVisualModel) observePaint() tea.Cmd {
	if !m.sawSize || m.mainSurf == nil || m.mainSurf.Width() != m.width || m.mainSurf.Height() != m.height || m.focusedPopup() != nil {
		return nil
	}
	settled := paintedCompactionTitle(m.App, "Compacted context (Manual) ")
	if m.revived {
		if m.phase == "" && settled != "" && paintedCompactionChatContains(m.App, "Queued prompt executed exactly once") {
			m.settledTitle = settled
			return m.mark("revived")
		}
		return nil
	}
	switch m.phase {
	case "":
		if paintedCompactionChatContains(m.App, "Initial work completed.") {
			return m.mark("ready")
		}
	case "ready":
		if active := paintedCompactionTitle(m.App, "Compacting context (Manual) "); active != "" {
			m.activeTitle = active
			return m.mark("active")
		}
	case "active":
		if m.ws.AgentMgr().Get(m.id).State.Messages.PendingSteeringCount() == 1 && paintedCompactionChatContains(m.App, "Queue this after compaction") {
			return m.mark("queued")
		}
	case "queued":
		if settled != "" && paintedCompactionChatContains(m.App, "Queued prompt executed exactly once") {
			m.settledTitle = settled
			return m.mark("settled")
		}
	}
	return nil
}

func paintedCompactionChatContains(app *App, text string) bool {
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H-1; y++ {
		if strings.Contains(shellBufferRow(app, y), text) {
			return true
		}
	}
	return false
}

func (m *compactionVisualModel) mark(phase string) tea.Cmd {
	m.phase = phase
	if m.evidence == "" {
		return nil
	}
	return func() tea.Msg {
		file, err := os.OpenFile(m.evidence, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return compactionVisualEvidenceError{err}
		}
		_, err = fmt.Fprintln(file, phase)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return compactionVisualEvidenceError{err}
		}
		return nil
	}
}
