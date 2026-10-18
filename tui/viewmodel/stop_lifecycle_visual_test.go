package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/session"
	"crude/types"
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
	release := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		latest := ""
		for i := len(request.Messages) - 1; i >= 0; i-- {
			text := request.Messages[i].ContentString()
			if request.Messages[i].Role == "user" && (strings.HasPrefix(text, "Hold ") || strings.HasPrefix(text, "Follow ") || strings.Contains(text, `kind="human_steering"`)) {
				latest = text
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		text := "Fresh turn completed. Accepted work remains intact."
		if request.ToolChoice == "none" {
			extraction.Add(1)
			if holdExtraction.Load() {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			text = "<<<intent>>>\nPreserve all accepted work.\n<<<work>>>\nStopped turns and corrections retained.\n<<<state>>>\nSafe checkpoint.\n<<<continuation>>>\nFollow the new input without replay."
		} else {
			normal.Add(1)
			if strings.HasPrefix(latest, "Hold ") {
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Accepted output before Stop.\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				text = " Model response finished."
			} else if strings.Contains(latest, `kind="human_steering"`) {
				text = "Your steering reached the model exactly once."
			}
		}
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
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
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
	model := &stopLifecycleVisualModel{App: shell.app, t: t, id: id, normal: &normal, extraction: &extraction, holdExtraction: &holdExtraction, release: release}
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
	release            chan struct{}
}

func (m *stopLifecycleVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF5:
			m.holdExtraction.Store(true)
			return m, nil
		case tea.KeyF6:
			m.holdExtraction.Store(false)
			m.release <- struct{}{}
			return m, nil
		case tea.KeyF9:
			state := m.ws.AgentMgr().Get(m.id).State
			paint := ansi.Strip(m.View().Content)
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
