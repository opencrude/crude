package viewmodel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
	ttypes "crude/tui/types"
	"crude/workspace"
)

type latencyEventSender struct {
	ctx    context.Context
	events chan ttypes.AgentEventMsg
}

func (s *latencyEventSender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *latencyEventSender) SendAgentEventWithID(typ string, data any, id string) {
	select {
	case s.events <- ttypes.AgentEventMsg{Type: typ, Data: data, AgentID: id}:
	case <-s.ctx.Done():
	}
}

// Explicit opt-in only: credentials are read privately, and the backend writes
// only to a temporary HOME/project. No saved conversations are sent to Ollama.
func TestLiveOllamaWaitLatency(t *testing.T) {
	if os.Getenv("CRUDE_TEST_LIVE_OLLAMA") != "1" {
		t.Skip("live Ollama test is opt-in")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".crude", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Accounts map[string]provider.Account `json:"accounts"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal("invalid provider config")
	}
	accountID := ""
	for id, account := range stored.Accounts {
		if account.ProviderID == "ollama-cloud" && account.APIKey != "" {
			accountID = id
			break
		}
	}
	if accountID == "" {
		t.Fatal("Ollama Cloud credentials unavailable")
	}
	off := false
	agents := session.DefaultAgentsConfig()
	agents.Enabled = false
	profile := agents.Profiles[agents.ActiveProfile]
	profile.MultiAgent = &off
	profile.Categories[session.CategoryDefault] = session.CategoryConfig{
		AccountID: accountID, Model: "glm-5.3", Policy: "read-only", AllowedTools: []string{"read_file"},
	}
	agents.Profiles[agents.ActiveProfile] = profile
	cfg := &session.Config{Accounts: stored.Accounts, Agents: agents}
	t.Setenv("HOME", t.TempDir())
	app := newFullTestApp(t)
	app.RootPath, app.WorkspaceDir = t.TempDir(), t.TempDir()
	app.Logger = &testLogger{}
	app.SessionsByID = make(map[string]*workspace.Session)
	app.SessionsByAgent = make(map[string]*workspace.Session)
	_, err = app.CreateSession(cfg, os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	sender := &latencyEventSender{ctx: ctx, events: make(chan ttypes.AgentEventMsg, 4096)}
	defer app.CloseAllSessions()
	defer cancel()
	orch, err := app.StartOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	app.AddOrchestrator(orch.ID)
	enableIdleTicks(t, app)
	app.hydrateActiveMessages(seedBudgetSession(1000, 4))
	app.paintShellFrame()
	prompt := "Latency measurement only. Do not use any tools. Ignore the synthetic repeated records below and reply with exactly ACK.\n" + strings.Repeat("Synthetic record: alpha beta gamma delta epsilon zeta eta theta.\n", 6000) + "\nReply with exactly ACK, no tools."
	start := time.Now()
	app.submitInput(orch.ID, prompt, nil)
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	var maxUpdate, maxPaint, maxGap time.Duration
	previous := time.Now()
	frames := 0
	for {
		finished := false
		var update time.Time
		select {
		case event := <-sender.events:
			update = time.Now()
			if event.Type == "phase" {
				if phase, ok := workspace.ParseAgentPhase(event.Data); ok {
					t.Logf("phase=%s elapsed=%s uploaded=%d/%d", phase.Phase, time.Since(start), phase.UploadedBytes, phase.TotalBytes)
				}
			}
			app.handleAgentEvent(event)
			if event.Type == "error" {
				t.Fatalf("live provider returned an error: %v", event.Data)
			}
			finished = event.Type == "done"
		case <-tick.C:
			update = time.Now()
			app.handleTick(ttypes.TickMsg(update))
		case <-ctx.Done():
			t.Fatal("live provider deadline expired")
		}
		maxUpdate = max(maxUpdate, time.Since(update))
		paint := time.Now()
		app.paintShellFrame()
		maxPaint = max(maxPaint, time.Since(paint))
		maxGap = max(maxGap, time.Since(previous))
		previous = time.Now()
		frames++
		if finished {
			break
		}
	}
	t.Logf("live glm-5.3: elapsed=%s frames=%d max_update=%s max_paint=%s max_frame_gap=%s", time.Since(start), frames, maxUpdate, maxPaint, maxGap)
	if maxUpdate > 500*time.Millisecond || maxPaint > 500*time.Millisecond {
		t.Fatal("live request blocked UI for over 500ms")
	}
}
