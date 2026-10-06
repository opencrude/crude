package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
)

const liveOllamaSubAgentModel = "glm-5.3-flash:cloud"

type liveSubAgentSender struct {
	mu     sync.Mutex
	events []agentEvent
}

func (s *liveSubAgentSender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *liveSubAgentSender) SendAgentEventWithID(typ string, data any, agentID string) {
	s.mu.Lock()
	s.events = append(s.events, agentEvent{typ: typ, data: data, agentID: agentID})
	s.mu.Unlock()
}

func (s *liveSubAgentSender) has(typ string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.typ == typ {
			return true
		}
	}
	return false
}

func (s *liveSubAgentSender) firstData(typ string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.typ == typ {
			data, _ := event.data.(map[string]string)
			return data
		}
	}
	return nil
}

func newLiveOllamaSubAgentRunner(t *testing.T) (*Session, *SubAgentRunner, *liveSubAgentSender) {
	t.Helper()
	key := os.Getenv("CRUDE_LIVE_OLLAMA_KEY")
	if key == "" {
		t.Skip("CRUDE_LIVE_OLLAMA_KEY not set")
	}
	agents := DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	liveCategory := CategoryConfig{
		AccountID:          "account",
		Model:              liveOllamaSubAgentModel,
		Policy:             "read-only",
		AllowedTools:       []string{"publish_report"},
		ModelSupportsTools: true,
	}
	profile.Categories[CategoryDefault] = liveCategory
	profile.Categories[CategoryResearcher] = liveCategory
	agents.Profiles[agents.ActiveProfile] = profile
	cfg := &Config{

		Accounts: map[string]provider.Account{"account": {ID: "account", ProviderID: "ollama-cloud", APIKey: key,
			Connection: provider.DefaultTemplates()["ollama-cloud"]}},
		TurnLogsEnabled: true,
		Agents:          agents,
	}
	home := t.TempDir()
	s, err := NewWithSessionDir("live-subagent", cfg, home, filepath.Join(home, "session"))
	if err != nil {
		t.Fatalf("NewWithSessionDir: %v", err)
	}
	runner := s.SubAgentRunner()
	if runner == nil {
		runner = NewSubAgentRunner(s, 1)
		s.subAgentSup = runner
	}
	sender := &liveSubAgentSender{}
	runner.SetSender(sender)
	t.Cleanup(func() {
		runner.SessionCancel()
		runner.Wait()
		s.Close()
	})
	return s, runner, sender
}

func TestLiveOllamaAgentStartPublishesTerminalResult(t *testing.T) {
	s, runner, sender := newLiveOllamaSubAgentRunner(t)
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	result, err := runner.StartAgent(ctx, map[string]any{
		"name":          "live-wait",
		"category":      agent.CatResearcher,
		"description":   "Verify the synchronous child lifecycle without reading or changing files.",
		"instructions":  `Immediately call publish_report with kind "final", summary "live wait lifecycle ok", body "live wait lifecycle ok", and recipient_ids containing the exact parent_id from your assignment. Do not call any other tool.`,
		"allowed_tools": []string{"publish_report"},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if !strings.Contains(result.Content, `"status":"running"`) || !strings.Contains(result.Content, `"run_id"`) {
		t.Fatalf("start result = %s", result.Content)
	}
	runner.Wait()
	completed := runner.Manager().CompletedResults()
	if len(completed) != 1 || completed[0].Status != agent.SubAgentCompleted {
		t.Fatalf("completed results = %+v", completed)
	}
	snapshots := s.CompletedSubAgentSnapshots()
	if len(snapshots) != 1 || len(snapshots[0].Messages) == 0 || snapshots[0].Messages[0].Meta.DisplayRole != "assignment" {
		t.Fatalf("durable child review snapshot = %+v", snapshots)
	}
	childDBPath := s.completedSubAgentDBPath(completed[0].SessionID)
	if !strings.HasPrefix(childDBPath, filepath.Join(s.SessionDir(), "subagents")+string(filepath.Separator)) {
		t.Fatalf("live child DB escaped parent session: %q", childDBPath)
	}
	rows, err := sessiondb.ListSubAgentResults(s.DB(), s.SessionID())
	if err != nil || len(rows) != 1 {
		t.Fatalf("durable parent result rows = %+v err=%v", rows, err)
	}
	if !sender.has("sub_agent_started") || !sender.has("sub_agent_completed") {
		t.Fatalf("terminal event contract missing: %+v", sender.events)
	}
	started := sender.firstData("sub_agent_started")
	if !strings.Contains(started["prompt"], `Immediately call publish_report`) || strings.Contains(started["prompt"], "max_steps") {
		t.Fatalf("sub-agent assignment event = %#v", started)
	}
}

func TestLiveOllamaAsyncSubAgentSurvivesParentContextEnd(t *testing.T) {
	_, runner, _ := newLiveOllamaSubAgentRunner(t)
	parentCtx, cancelParent := context.WithCancel(t.Context())
	result, err := runner.StartAgent(parentCtx, map[string]any{
		"name":          "live-async",
		"category":      agent.CatResearcher,
		"description":   "Verify the asynchronous child lifecycle without reading or changing files.",
		"instructions":  `Immediately call publish_report with kind "final", summary "live async lifecycle ok", body "live async lifecycle ok", and recipient_ids containing the exact parent_id from your assignment. Do not call any other tool.`,
		"allowed_tools": []string{"publish_report"},
	})
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if !strings.Contains(result.Content, `"status":"running"`) {
		t.Fatalf("async launch result = %s", result.Content)
	}
	cancelParent()
	runner.Wait()
	completed := runner.Manager().CompletedResults()
	if len(completed) != 1 || completed[0].Status != agent.SubAgentCompleted {
		t.Fatalf("child inherited parent turn cancellation: %+v", completed)
	}
}
