package session

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/turnloop"
	"crude/internal/providersim"
)

// TestParentControlsStoppedChildWithoutHuman: a child whose provider rejects
// its credentials stops for attention (a genuine run-level fault). Its parent
// is told the exact incident, sees it through agent_wait and agent_inspect, and
// can continue or cancel the child itself. A stopped child is never a dead end
// that only a human Retry button can resolve.
func TestParentControlsStoppedChildWithoutHuman(t *testing.T) {
	for _, operation := range []string{"continue", "cancel"} {
		t.Run(operation, func(t *testing.T) { checkParentControlsStoppedChild(t, operation) })
	}
}

func checkParentControlsStoppedChild(t *testing.T, operation string) {
	const rootModel, childModel = "stopped-child-root", "stopped-child-model"
	type providerCall struct {
		request provider.ChatRequest
		reply   chan providersim.Turn
	}
	rootCalls, childCalls := make(chan providerCall, 8), make(chan providerCall, 8)
	var rootRequests, childRequests atomic.Int32
	server := providersim.New(t)
	server.Respond(func(wire providersim.Request) providersim.Turn {
		request := simDecodeRequest[provider.ChatRequest](t, wire)
		call := providerCall{request: request, reply: make(chan providersim.Turn, 1)}
		var calls chan providerCall
		switch request.Model {
		case rootModel:
			if rootRequests.Add(1) > 4 {
				call.reply <- providersim.Text("Noted.")
			} else {
				calls = rootCalls
			}
		case childModel:
			if childRequests.Add(1) == 1 {
				return providersim.HTTPError(providersim.Auth).WithFaultMessage("credentials rejected")
			}
			calls = childCalls
		default:
			return providersim.HTTPError(providersim.InvalidRequest)
		}
		if calls != nil {
			select {
			case calls <- call:
			case <-t.Context().Done():
				return providersim.HTTPError(providersim.InvalidRequest)
			}
		}
		return simReply(t, call.reply)
	})

	home, project := t.TempDir(), t.TempDir()
	sessions := filepath.Join(home, "sessions")
	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), rootModel)
	applySessionMultiAgent(cfg, true)
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryCrafter] = CategoryConfig{AccountID: "account", Model: childModel}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(project)
	s.SetYolo(true)
	attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(context.Context, string, string) error { return nil }, nil)
	s.SetHistoryMutationHook(func(context.Context, HistoryMutation) error { return nil })
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !root.SubmitInput(provider.UserInput{InternalID: "stopped-child-human", Text: "Delegate the report."}) {
		t.Fatal("real root input was rejected")
	}
	next := func(calls <-chan providerCall, phase string) providerCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-time.After(5 * time.Second):
			if data, err := os.ReadFile(filepath.Join(s.SessionDir(), "session.log")); err == nil {
				t.Logf("runtime diagnostic:\n%s", data)
			}
			t.Fatalf("%s: provider request missing (root=%d child=%d)", phase, rootRequests.Load(), childRequests.Load())
			return providerCall{}
		}
	}

	result := func(request provider.ChatRequest, id string) string {
		t.Helper()
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolCallID == id {
				if message.ToolError || turnloop.IsToolError(message.ContentString()) {
					t.Fatalf("tool %s failed: %s", id, message.ContentString())
				}
				return message.ContentString()
			}
		}
		t.Fatalf("no result for %s", id)
		return ""
	}

	first := next(rootCalls, "root launch")
	first.reply <- providersim.CallTools(simToolArgs("launch", "agent_start", map[string]any{
		"description": "Publish a short report.", "category": CategoryCrafter, "isolation": "shared",
	}))
	second := next(rootCalls, "root receives allocated child")
	var launched struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result(second.request, "launch")), &launched); err != nil || launched.AgentID == "" {
		t.Fatalf("launch result: %+v %v", launched, err)
	}
	// The parent waits on its child; the stopped child ends the wait with its
	// exact incident instead of a silent timeout.
	second.reply <- providersim.CallTools(simToolArgs("wait", "agent_wait", map[string]any{"agent_id": launched.AgentID, "timeout_seconds": 5}))
	third := next(rootCalls, "root observes the stopped child")
	var waited collaborationWaitObservation
	if err := json.Unmarshal([]byte(result(third.request, "wait")), &waited); err != nil {
		t.Fatal(err)
	}
	if waited.Status != "needs_attention" || waited.Recovery == nil || waited.Recovery.ID == "" || waited.Recovery.Generation == 0 {
		t.Fatalf("parent wait did not surface the stopped child: %+v", waited)
	}
	incident := waited.Recovery

	third.reply <- providersim.CallTools(simToolArgs("inspect", "agent_inspect", map[string]any{"agent_id": launched.AgentID}))
	fourth := next(rootCalls, "root inspects the stopped child")
	var inspection struct {
		Agent    collaboration.Agent `json:"agent"`
		Recovery *agentRecoveryView  `json:"recovery"`
	}
	if err := json.Unmarshal([]byte(result(fourth.request, "inspect")), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.Recovery == nil || inspection.Recovery.ID != incident.ID || inspection.Agent.Presence == collaboration.RuntimeAttached {
		t.Fatalf("inspect hid the parked child's incident: %+v", inspection)
	}
	notified := false
	for _, message := range fourth.request.Messages {
		text := html.UnescapeString(message.ContentString())
		notified = notified || strings.Contains(text, "stopped and needs attention") && strings.Contains(text, incident.ID) && strings.Contains(text, `operation "continue"`)
	}
	if !notified {
		t.Fatal("parent never received the stopped-child notice with its exact controls")
	}

	fourth.reply <- providersim.CallTools(simToolArgs("control", "agent_control", map[string]any{
		"agent_id": launched.AgentID, "operation": operation,
		"recovery_id": incident.ID, "recovery_generation": incident.Generation,
	}))
	if operation == "cancel" {
		waitTurnDone(t, sender, root.ID)
		s.recoveryMu.Lock()
		_, open := s.pendingRecoveries[launched.AgentID]
		s.recoveryMu.Unlock()
		if open || childRequests.Load() != 1 {
			t.Fatalf("parent cancel did not settle the stopped child: open=%v child requests=%d", open, childRequests.Load())
		}
		completed := s.CompletedSubAgentSnapshots()
		if len(completed) != 1 || completed[0].Result.Status != turnloop.SubAgentCancelled {
			t.Fatalf("cancelled child result = %+v", completed)
		}
		return
	}
	// The credentials were fixed; the parent's continue starts a new attempt.
	resumed := next(childCalls, "child resumes after the parent's continue")
	resumed.reply <- providersim.CallTools(simToolArgs("report", "publish_report", map[string]any{
		"recipient_ids": []string{root.ID}, "kind": "final", "summary": "Report published after the parent continued me.", "body": "done",
	}))
	last := next(childCalls, "child receives its report publication")
	result(last.request, "report")
	last.reply <- providersim.Text("Done.")
	joined := make(chan struct{})
	go func() { s.SubAgentRunner().Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("continued child did not settle")
	}
	completed := s.CompletedSubAgentSnapshots()
	if len(completed) != 1 || completed[0].Result.Status != turnloop.SubAgentCompleted {
		t.Fatalf("continued child did not complete: %+v", completed)
	}
	waitTurnDone(t, sender, root.ID)
}
