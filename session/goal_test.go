package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

type goalProviderCall struct {
	request provider.ChatRequest
	reply   chan map[string]any
}

func goalRequestText(request provider.ChatRequest) string {
	var text strings.Builder
	for _, message := range request.Messages {
		text.WriteString(message.ContentString())
		text.WriteString("\n")
	}
	return text.String()
}

func goalRequestToolSchema(request provider.ChatRequest, name string) string {
	for _, tool := range request.Tools {
		if tool.Function.Name == name {
			raw, _ := json.Marshal(tool.Function.Parameters)
			return string(raw)
		}
	}
	return ""
}

func goalRequestOffers(request provider.ChatRequest, name string) bool {
	for _, tool := range request.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// A goal runs to completion through the real turn loop: the runtime starts
// the work without a human message, the worker's completion claim launches
// an independent verifier child that cannot edit (even with delegation off),
// the verifier's report verdict completes the goal, and the runtime stops.
func TestGoalContinuesUntilAnIndependentVerifierConfirms(t *testing.T) {
	const rootModel, verifierModel = "goal-root", "goal-verifier"
	rootCalls, verifierCalls := make(chan goalProviderCall, 16), make(chan goalProviderCall, 16)
	var continuations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		call := goalProviderCall{request: request, reply: make(chan map[string]any, 1)}
		calls := rootCalls
		if request.Model == verifierModel {
			calls = verifierCalls
		} else if n := int32(strings.Count(goalRequestText(request), "<goal_continuation>")); n > continuations.Load() {
			// History repeats earlier continuations; a new one raises the count.
			continuations.Store(n)
		}
		select {
		case calls <- call:
		case <-r.Context().Done():
			return
		}
		select {
		case delta := <-call.reply:
			finish := "stop"
			if _, ok := delta["tool_calls"]; ok {
				finish = "tool_calls"
			}
			frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	tool := func(id, name string, params map[string]any) map[string]any {
		arguments, _ := json.Marshal(params)
		return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}}}
	}
	next := func(calls <-chan goalProviderCall, phase string) goalProviderCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: provider request missing", phase)
			return goalProviderCall{}
		}
	}

	home, project := t.TempDir(), t.TempDir()
	cfg := modelAttributionConfig(server.URL, rootModel)
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryCrafter] = CategoryConfig{AccountID: "account", Model: verifierModel}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(project)
	s.SetYolo(true)
	store := attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(context.Context, string, string) error { return nil }, nil)
	s.SetHistoryMutationHook(func(context.Context, HistoryMutation) error { return nil })
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetGoal("Create done.txt containing ok", 0, false); err != nil {
		t.Fatal(err)
	}
	work := next(rootCalls, "goal starts without a human message")
	if text := goalRequestText(work.request); !strings.Contains(text, "<goal_continuation>") || !strings.Contains(text, "Create done.txt containing ok") {
		t.Fatalf("first goal turn lacks its continuation: %s", text)
	}
	if !goalRequestOffers(work.request, "update_goal") || !goalRequestOffers(work.request, "get_goal") {
		t.Fatal("the main agent was not offered its goal tools")
	}
	work.reply <- tool("write", "write_file", map[string]any{"path": "done.txt", "content": "ok"})
	claim := next(rootCalls, "worker claims completion")
	claim.reply <- tool("claim", "update_goal", map[string]any{"status": "complete", "evidence": "wrote done.txt"})
	audit := next(verifierCalls, "verifier starts")
	if text := goalRequestText(audit.request); !strings.Contains(text, `<category_contract name="goal_verifier">`) || !strings.Contains(text, "wrote done.txt") || strings.Contains(text, `<category_contract name="crafter">`) {
		t.Fatalf("verifier lacks its contract or assignment: %s", text)
	}
	if !strings.Contains(goalRequestToolSchema(audit.request, "publish_report"), `"verdict"`) {
		t.Fatalf("the verifier report does not require a typed verdict: %s", goalRequestToolSchema(audit.request, "publish_report"))
	}
	for _, denied := range []string{"write_file", "edit_file", "apply_patch", "agent_start"} {
		if goalRequestOffers(audit.request, denied) {
			t.Fatalf("verifier was offered %s", denied)
		}
	}
	if goal, _ := s.Goal(); !goal.Verifying || goal.Status != types.GoalActive {
		t.Fatalf("goal while verifying = %+v", goal)
	}
	afterClaim := next(rootCalls, "worker learns a verifier is checking")
	afterClaim.reply <- map[string]any{"content": "A verifier is checking."}
	audit.reply <- tool("look", "read_file", map[string]any{"path": filepath.Join(project, "done.txt")})
	verdict := next(verifierCalls, "verifier reports")
	verdict.reply <- tool("report", "publish_report", map[string]any{
		"recipient_ids": []string{root.ID}, "kind": "final", "summary": "done.txt contains ok.",
		"body": "Requirement: done.txt contains ok. Evidence: read_file returned ok.", "verdict": "achieved",
	})
	finish := next(verifierCalls, "verifier finishes")
	finish.reply <- map[string]any{"content": "Verified."}

	deadline := time.Now().Add(5 * time.Second)
	for {
		goal, _ := s.Goal()
		if goal.Status == types.GoalComplete {
			if goal.Verifying || !strings.Contains(goal.Reason, "done.txt contains ok") {
				t.Fatalf("completed goal = %+v", goal)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("goal never completed: %+v", goal)
		}
		select {
		case call := <-rootCalls:
			call.reply <- map[string]any{"content": "Noted."}
		case <-time.After(20 * time.Millisecond):
		}
	}
	if data, err := os.ReadFile(filepath.Join(project, "done.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("worker's file = %q %v", data, err)
	}
	// The report wakes the worker once; a complete goal never continues.
	quiet := time.After(1500 * time.Millisecond)
	for {
		select {
		case call := <-rootCalls:
			call.reply <- map[string]any{"content": "Goal confirmed."}
			continue
		case <-quiet:
		}
		break
	}
	if n := continuations.Load(); n != 1 {
		t.Fatalf("goal continuation turns = %d, want only the one that did the work", n)
	}
	stored, ok, err := sessiondb.LoadGoal(s.db, s.sessionID)
	if err != nil || !ok || stored.Goal.Status != types.GoalComplete || stored.VerifierID != "" {
		t.Fatalf("stored goal = %+v ok=%v err=%v", stored, ok, err)
	}
	_ = store
}

// Stop pauses the goal; resume continues it; a reopened session waits for
// the user instead of starting work by itself.
func TestGoalStopPausesAndReopenWaits(t *testing.T) {
	started, done := make(chan struct{}, 8), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer server.Close()
	defer close(done)
	home := t.TempDir()
	cfg := modelAttributionConfig(server.URL, "goal-root")
	dir := filepath.Join(home, "sessions")
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 256)
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoal("Keep working", 0, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("goal did not start")
	}
	root.CancelTurn()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if goal, _ := s.Goal(); goal.Status == types.GoalPaused {
			break
		}
		if time.Now().After(deadline) {
			goal, _ := s.Goal()
			t.Fatalf("Stop did not pause the goal: %+v", goal)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.ResumeGoal(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not continue the goal")
	}
	path, sessionID := s.dbPath, s.sessionID
	s.Close()
	db, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	record, ok, err := sessiondb.LoadGoal(db, sessionID)
	if err != nil || !ok || record.Goal.Status != types.GoalPaused {
		t.Fatalf("closing during a goal turn did not pause the goal: %+v %v", record, err)
	}
	// A crash leaves the goal active with no turn running.
	record.Goal.Status, record.Goal.Reason = types.GoalActive, ""
	if err := sessiondb.SaveGoal(db, sessionID, record); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	reopened, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	goal, ok := reopened.Goal()
	if !ok || goal.Status != types.GoalActive || !goal.Waiting {
		t.Fatalf("reopened goal = %+v", goal)
	}
	if _, ok := reopened.goalContinuation(); ok {
		t.Fatal("a reopened goal continued without the user")
	}
}

// A goal with a token budget stops once its turns use the budget: the
// running turn is told at its next step to wrap up, no further goal turn
// starts, and the goal cannot be resumed past its budget.
func TestGoalStopsAtItsTokenBudget(t *testing.T) {
	calls := make(chan goalProviderCall, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		call := goalProviderCall{request: request, reply: make(chan map[string]any, 1)}
		select {
		case calls <- call:
		case <-r.Context().Done():
			return
		}
		select {
		case delta := <-call.reply:
			finish := "stop"
			if _, ok := delta["tool_calls"]; ok {
				finish = "tool_calls"
			}
			frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}},
				"usage": map[string]any{"prompt_tokens": 150, "completion_tokens": 5}})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	next := func(phase string) goalProviderCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: provider request missing", phase)
			return goalProviderCall{}
		}
	}
	home, project := t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "goal-root"), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(project)
	s.SetYolo(true)
	attachDelegationTestCollaboration(t, s)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	if _, err := s.NewOrchestrator(sender); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoal("Write notes.txt", 100, false); err != nil {
		t.Fatal(err)
	}
	work := next("goal starts")
	if text := goalRequestText(work.request); !strings.Contains(text, "Token budget: 100. Tokens remaining: 100.") {
		t.Fatalf("continuation lacks the budget: %s", text)
	}
	arguments, _ := json.Marshal(map[string]any{"path": "notes.txt", "content": "draft"})
	work.reply <- map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "write", "type": "function",
		"function": map[string]any{"name": "write_file", "arguments": string(arguments)}}}}
	wrap := next("the turn's next step")
	if text := goalRequestText(wrap.request); !strings.Contains(text, "<goal_budget_limit>") {
		t.Fatalf("the running turn was not told to wrap up: %s", text)
	}
	wrap.reply <- map[string]any{"content": "Wrote a draft; the budget is used."}
	select {
	case call := <-calls:
		t.Fatalf("a goal turn started past its budget: %s", goalRequestText(call.request))
	case <-time.After(1500 * time.Millisecond):
	}
	goal, _ := s.Goal()
	if goal.Status != types.GoalBudgetLimited || goal.TokensUsed < 100 || !strings.Contains(goal.Reason, "token budget of 100") {
		t.Fatalf("goal after its budget = %+v", goal)
	}
	if _, err := s.ResumeGoal(); err == nil || !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("resume past the budget = %v", err)
	}
	stored, ok, err := sessiondb.LoadGoal(s.db, s.sessionID)
	if err != nil || !ok || stored.Goal.Status != types.GoalBudgetLimited || stored.Goal.TokenBudget != 100 {
		t.Fatalf("stored goal = %+v ok=%v err=%v", stored, ok, err)
	}
}

// A provider refusal is a genuine blocker no Retry can fix. The goal turn
// ends with its exact reason and the goal blocks, as Codex does for a
// non-retryable turn error; nothing waits for /retry and nothing repeats.
func TestGoalProviderRefusalBlocksWithExactReason(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request","code":"cyber_policy","message":"This content was flagged for possible cybersecurity risk."}}`))
	}))
	defer server.Close()
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "goal-root"), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 256)
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoal("Finish the protocol work", 0, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var goal types.Goal
	for {
		goal, _ = s.Goal()
		if goal.Status == types.GoalBlocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refused goal turn did not block the goal: %+v requests=%d", goal, requests.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(goal.Reason, "cyber_policy") || !strings.Contains(goal.Reason, "/goal resume") {
		t.Fatalf("blocked goal does not report the exact blocker: %q", goal.Reason)
	}
	time.Sleep(1200 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatalf("refusal was repeated: requests=%d", requests.Load())
	}
	if root.TurnActive() {
		t.Fatal("refused turn still owns the session; the next message would queue behind /retry")
	}
	checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, root.ID)
	if err != nil || found {
		t.Fatalf("refused turn left an open recovery incident: %+v found=%v err=%v", checkpoint.Status, found, err)
	}
	if _, ok := s.goalContinuation(); ok {
		t.Fatal("a blocked goal continued by itself")
	}
}
