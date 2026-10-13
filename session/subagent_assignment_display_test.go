package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/tools"
)

type assignmentEventSender struct {
	mu      sync.Mutex
	started []map[string]string
}

func (s *assignmentEventSender) SendAgentEvent(typ string, data any) {
	s.SendAgentEventWithID(typ, data, "")
}

func (s *assignmentEventSender) SendAgentEventWithID(typ string, data any, _ string) {
	if values, ok := data.(map[string]string); ok && typ == "sub_agent_started" {
		s.mu.Lock()
		s.started = append(s.started, values)
		s.mu.Unlock()
	}
}

func TestChildAssignmentDisplayUsesTypedFields(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		description, instructions string
		want                      string
	}{
		{"both", " Audit parser ", "Check `parse()`\n- edge cases", "**Audit parser**\n\nCheck `parse()`\n- edge cases"},
		{"description only", "Audit parser", "  ", "Audit parser"},
		{"duplicate", "Audit parser", "Audit parser", "Audit parser"},
		{"multiline description stays literal", "Audit\nparser", "Report", "Audit\nparser\n\nReport"},
		{"emphasis in description stays literal", "Audit *all* parsers", "Report", "Audit *all* parsers\n\nReport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := childAssignmentDisplay(agent.SubAgentSpec{Description: tc.description, Instructions: tc.instructions}); got != tc.want {
				t.Fatalf("display = %q; want %q", got, tc.want)
			}
		})
	}
}

// The provider receives the escaped assignment envelope, while the live child
// tab and the persisted transcript show the parent's authored text unchanged.
func TestChildAssignmentChatShowsAuthoredTextNotProviderEnvelope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	cfg := modelAttributionConfig(server.URL, "assignment-model")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "assignment-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("assignment-parent", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	project := t.TempDir()
	s.SetWorkDir(project)
	store := attachDelegationTestCollaboration(t, s)
	parent, err := s.registerCollaborator(t.Context(), "assignment-parent", "parent", s.sessionID, "", CategoryDefault, s.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	// An unreadable guidance source parks the child before any provider request.
	if err := os.Mkdir(filepath.Join(project, "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	runner.orchestratorID = parent.ID
	s.subAgentSup = runner
	sender := &assignmentEventSender{}
	runner.SetSender(sender)
	runner.SetParentToolFn(func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{}, nil
	})
	const instructions = "Inspect <delegated_assignment> handling & report:\n- escaped tags\n- `code` spans"
	result, err := runner.StartAgent(t.Context(), map[string]any{
		"category": CategoryResearcher, "description": "Audit the parser", "instructions": instructions,
		"isolation": "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
		t.Fatalf("child launch identity = %s, err=%v", result.Content, err)
	}
	settled := make(chan struct{})
	go func() { runner.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not park")
	}
	want := "**Audit the parser**\n\n" + instructions

	sender.mu.Lock()
	started := append([]map[string]string(nil), sender.started...)
	sender.mu.Unlock()
	if len(started) != 1 || started[0]["prompt"] != want {
		t.Fatalf("live assignment = %+v; want %q", started, want)
	}

	_, rows, _, err := loadSubAgentSnapshotDB(s.completedSubAgentDBPath(identity.AgentID), identity.AgentID)
	if err != nil || len(rows) == 0 || len(rows[0].Meta.Snapshots) != 1 {
		t.Fatalf("persisted child transcript rows=%+v err=%v", rows, err)
	}
	if snapshot := rows[0].Meta.Snapshots[0]; snapshot.Role != "assignment" || snapshot.Content != want {
		t.Fatalf("persisted assignment snapshot = %+v; want %q", snapshot, want)
	}
	envelope := rows[0].Msg.ContentString()
	if !strings.HasPrefix(envelope, "<delegated_assignment ") || !strings.Contains(envelope, "&lt;delegated_assignment&gt;") {
		t.Fatalf("provider envelope was not retained escaped: %q", envelope)
	}

	s.recoveryMu.Lock()
	checkpoint := s.pendingRecoveries[identity.AgentID]
	s.recoveryMu.Unlock()
	if len(checkpoint.Archive) == 0 || checkpoint.Archive[0].DisplayContent != want || checkpoint.PendingInput.DisplayText != want {
		t.Fatalf("recovery checkpoint lost the display projection: %+v", checkpoint)
	}
}

func (s *assignmentEventSender) SendAgentEventEnvelope(event agent.AgentEvent) {
	s.SendAgentEventWithID(event.Type, event.Data, event.AgentID)
}
