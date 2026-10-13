package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/safety"
	"crude/types/reasoning"
)

func TestSubAgentClassifiesItsOwnFlattenedReasoningDuringToolLoop(t *testing.T) {
	var requests atomic.Int32
	trace := strings.Repeat("inspect the source carefully. ", 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "child-model" {
			t.Errorf("child used parent model: %q", body.Model)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		step := requests.Add(1)
		delta := map[string]any{"reasoning_content": trace}
		finish := "tool_calls"
		switch step {
		case 1, 2:
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprintf("read-%d", step), "type": "function", "function": map[string]string{"name": "list_directory", "arguments": `{"path":"."}`}}}
		case 3:
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": "report", "type": "function", "function": map[string]string{"name": "publish_report", "arguments": `{"recipient_ids":["parent"],"kind":"final","summary":"Findings recorded","body":"Inspected the source."}`}}}
		case 4:
			delta["content"] = "Finished."
			finish = "stop"
		default:
			t.Errorf("unexpected child request %d", step)
			return
		}
		payload, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}},
			"usage":   map[string]any{"prompt_tokens": 300, "completion_tokens": 260, "completion_tokens_details": map[string]int{"reasoning_tokens": 155}},
		})
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	defer server.Close()

	cfg := modelAttributionConfig(server.URL, "parent-model")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "child-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	home := t.TempDir()
	s, err := NewWithSessionDir("parent", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	if _, err := s.registerCollaborator(t.Context(), "parent", "parent", s.SessionID(), "", CategoryDefault, s.db); err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	s.subAgentSup = runner
	defer runner.SessionCancel()
	sender := &liveSubAgentSender{}
	runner.SetSender(sender)
	handle, err := runner.launchSubAgent(context.Background(), agent.SubAgentSpec{
		ID: "child", Name: "researcher", Category: CategoryResearcher, Isolation: "shared",
		Description: "Inspect the source", ParentID: "parent", Policy: safety.ReadOnlyPolicy(),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	if results := runner.Manager().CompletedResults(); len(results) != 1 || results[0].Status != agent.SubAgentCompleted || results[0].ReportID == "" {
		t.Fatalf("child did not complete with its real final report: %+v", results)
	}
	if requests.Load() != 4 || handle.Agent.ReasoningContentKind != reasoning.InferredRaw {
		t.Fatalf("child requests=%d inferred kind=%q", requests.Load(), handle.Agent.ReasoningContentKind)
	}
	sender.mu.Lock()
	events := append([]agentEvent(nil), sender.events...)
	sender.mu.Unlock()
	var kinds []reasoning.Kind
	for _, event := range events {
		if event.agentID != "child" || event.typ != "thinking" {
			continue
		}
		delta, ok := event.data.(reasoning.Delta)
		if ok {
			kinds = append(kinds, delta.Kind)
		}
	}
	if len(kinds) != 4 || kinds[0] != reasoning.Provisional || kinds[1] != reasoning.Provisional || kinds[2] != reasoning.InferredRaw || kinds[3] != reasoning.InferredRaw {
		t.Fatalf("child reasoning stream kinds = %v", kinds)
	}
	if got := s.inferredReasoningKind("parent", "openrouter", "account", "parent-model"); got != reasoning.Unknown {
		t.Fatalf("child classification leaked to parent: %q", got)
	}
}
