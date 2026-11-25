package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent/safety"
	"crude/agent/turnloop"
	"crude/internal/providersim"
	"crude/types/reasoning"
)

func TestSubAgentClassifiesItsOwnFlattenedReasoningDuringToolLoop(t *testing.T) {
	trace := strings.Repeat("inspect the source carefully. ", 30)
	server := providersim.New(t, providersim.WithChatGateway("child-model", providersim.DeepSeek))
	server.Respond(func(request providersim.Request) providersim.Turn {
		if request.Model != "child-model" {
			t.Errorf("child used parent model: %q", request.Model)
			return providersim.HTTPError(providersim.InvalidRequest)
		}
		var turn providersim.Turn
		switch request.Index {
		case 1, 2:
			turn = simTool(fmt.Sprintf("read-%d", request.Index), "list_directory", `{"path":"."}`)
		case 3:
			turn = simTool("report", "publish_report", `{"recipient_ids":["parent"],"kind":"final","summary":"Findings recorded","body":"Inspected the source."}`)
		case 4:
			turn = providersim.Text("Finished.")
		default:
			t.Errorf("unexpected child request %d", request.Index)
			return providersim.HTTPError(providersim.InvalidRequest)
		}
		return turn.WithReasoning(trace, "", "").WithUsage(providersim.Usage{Input: 300, Output: 260, Reasoning: 155})
	})

	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "parent-model")
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
	handle, err := runner.launchSubAgent(context.Background(), turnloop.SubAgentSpec{
		ID: "child", Name: "researcher", Category: CategoryResearcher, Isolation: "shared",
		Description: "Inspect the source", ParentID: "parent", Policy: safety.ReadOnlyPolicy(),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	if results := runner.Manager().CompletedResults(); len(results) != 1 || results[0].Status != turnloop.SubAgentCompleted || results[0].ReportID == "" {
		t.Fatalf("child did not complete with its real final report: %+v", results)
	}
	if len(server.Requests()) != 4 || handle.Agent.ReasoningContentKind != reasoning.InferredRaw {
		t.Fatalf("child requests=%d inferred kind=%q", len(server.Requests()), handle.Agent.ReasoningContentKind)
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
