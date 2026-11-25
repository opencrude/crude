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
	"crude/agent/session"
	"crude/internal/providersim"
	"crude/types/statuscenter"
)

// Driven only through scripts/tuishot.sh in a dedicated terminal. The core,
// provider, history and session are isolated; no user session is modified.
func TestStopLifecycleVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_STOP_LIFECYCLE") != "1" {
		t.Skip("dedicated terminal capture")
	}
	t.Setenv("HOME", t.TempDir())
	var normal, extraction atomic.Int32
	var holdExtraction atomic.Bool
	var held atomic.Pointer[providersim.Gate]
	held.Store(providersim.NewGate())
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if string(request.ToolChoice) == `"none"` {
			extraction.Add(1)
			answer := simulatedAnswer("<<<intent>>>\nPreserve all accepted work.\n<<<work>>>\nStopped turns and corrections retained.\n<<<state>>>\nSafe checkpoint.\n<<<continuation>>>\nFollow the new input without replay.")
			if holdExtraction.Load() {
				answer = answer.WithGate(held.Load())
			}
			return answer
		}
		normal.Add(1)
		for i := len(request.Messages) - 1; i >= 0; i-- {
			message := request.Messages[i]
			if message.Role != "user" {
				continue
			}
			text := simulatedText(message.Parts)
			switch {
			case strings.HasPrefix(text, "Hold "):
				return providersim.Turn{}.WithTextDeltas("Accepted output before Stop.", " Model response finished.").WithoutUsage().WithGateAfter(chatFirstTextEvents, held.Load())
			case strings.Contains(text, `kind="human_steering"`):
				return simulatedAnswer("Your steering reached the model exactly once.")
			case strings.HasPrefix(text, "Follow "):
				return simulatedAnswer("Fresh turn completed. Accepted work remains intact.")
			}
		}
		return simulatedAnswer("Fresh turn completed. Accepted work remains intact.")
	})
	runtime := remoteJourneyRuntimeConfig(t, server.BaseURL(providersim.Chat), func(cfg *session.Config) {
		profile := cfg.Agents.Profiles["default"]
		category := profile.Categories[session.CategoryDefault]
		percentage := 65
		category.ContextCompactionPercent = &percentage
		category.ModelContextWindow = 80_000
		category.ModelMaxOutput = 2048
		profile.Categories[session.CategoryDefault] = category
		cfg.Agents.Profiles["default"] = profile
	})
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	runAppCommand(t, shell.app, shell.app.submitPreparedInput(id, provider.UserInput{
		Text: "Review the original evidence.\n" + strings.Repeat("accepted source evidence\n", 4_000), DisplayText: "Review the saved source evidence and preserve all completed work.",
	}, shell.app.Input.DraftRevision()))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted })
	model := &stopLifecycleVisualModel{App: shell.app, t: t, id: id, normal: &normal, extraction: &extraction, holdExtraction: &holdExtraction, release: func() { held.Swap(providersim.NewGate()).Release() }}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type stopLifecycleVisualModel struct {
	*App
	t                  *testing.T
	id                 string
	normal, extraction *atomic.Int32
	holdExtraction     *atomic.Bool
	release            func()
}

func (m *stopLifecycleVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF5:
			m.holdExtraction.Store(true)
			return m, nil
		case tea.KeyF6:
			m.holdExtraction.Store(false)
			m.release()
			return m, nil
		case tea.KeyF9:
			state := m.ws.AgentMgr().Get(m.id).State
			paint := ansi.Strip(m.View().RenderContent())
			if m.normal.Load() < 6 || m.extraction.Load() < 1 || m.Input.Value() != "Private draft remains" || state.Cancelling || state.RecoveryStatus().CanRetry() || state.ActiveCompaction.Phase != "" || strings.Contains(paint, "<turn_context>") || strings.Contains(paint, "<workspace_event") {
				m.t.Errorf("visual lifecycle not settled: normal=%d extraction=%d draft=%q cancelling=%v recovery=%+v compaction=%+v", m.normal.Load(), m.extraction.Load(), m.Input.Value(), state.Cancelling, state.RecoveryStatus(), state.ActiveCompaction)
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("stop/steering/compaction visual acceptance; failed=%v normal=%d extraction=%d\n", m.t.Failed(), m.normal.Load(), m.extraction.Load())), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
