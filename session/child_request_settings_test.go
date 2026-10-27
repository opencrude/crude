package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	ttypes "crude/types"
)

// childWireHarness runs a real root that launches real children against a
// scripted provider, exposing every child request body as sent on the wire.
type childWireHarness struct {
	t         *testing.T
	session   *Session
	root      *Orchestrator
	sender    *mockEventSender
	rootCalls chan wireCall
	children  chan wireCall
}

type wireCall struct {
	body  map[string]any
	reply chan map[string]any
}

const wireRootModel, wireChildModel = "wire-root", "wire-child"

func newChildWireHarness(t *testing.T, role CategoryConfig) *childWireHarness {
	h := &childWireHarness{t: t, rootCalls: make(chan wireCall, 8), children: make(chan wireCall, 8)}
	var rootRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := wireCall{body: body, reply: make(chan map[string]any, 1)}
		target := h.children
		if body["model"] == wireRootModel {
			if rootRequests.Add(1) > 2 {
				call.reply <- map[string]any{"content": "Noted."}
				target = nil
			} else {
				target = h.rootCalls
			}
		}
		if target != nil {
			select {
			case target <- call:
			case <-r.Context().Done():
				return
			}
		}
		var delta map[string]any
		select {
		case delta = <-call.reply:
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			t.Error("scripted provider reply was not released")
			return
		}
		if status, ok := delta["status"].(int); ok {
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"error":{"type":"authentication_error","message":"credentials rejected"}}`)
			return
		}
		finish := "stop"
		if _, ok := delta["tool_calls"]; ok {
			finish = "tool_calls"
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	t.Cleanup(server.Close)

	home, project := t.TempDir(), t.TempDir()
	cfg := modelAttributionConfig(server.URL, wireRootModel)
	applySessionMultiAgent(cfg, true)
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryCrafter] = role
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	s.SetYolo(true)
	attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(context.Context, string, string) error { return nil }, nil)
	s.SetHistoryMutationHook(func(context.Context, HistoryMutation) error { return nil })
	h.sender = newMockEventSender()
	h.sender.ch = make(chan agentEvent, 512)
	root, err := s.NewOrchestrator(h.sender)
	if err != nil {
		t.Fatal(err)
	}
	h.session, h.root = s, root
	if !root.SubmitInput(provider.UserInput{InternalID: "wire-human", Text: "Delegate."}) {
		t.Fatal("root input rejected")
	}
	return h
}

func (h *childWireHarness) next(calls <-chan wireCall, phase string) wireCall {
	h.t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(5 * time.Second):
		if data, err := os.ReadFile(filepath.Join(h.session.SessionDir(), "session.log")); err == nil {
			h.t.Logf("runtime diagnostic:\n%s", data)
		}
		h.t.Fatalf("%s: request missing", phase)
		return wireCall{}
	}
}

func wireTool(id, name string, params map[string]any) map[string]any {
	arguments, _ := json.Marshal(params)
	return map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}
}

// launch starts one child from the real root and returns its agent ID.
func (h *childWireHarness) launch(params map[string]any) string {
	h.t.Helper()
	params["description"], params["category"], params["isolation"] = "Report a fact.", CategoryCrafter, "shared"
	first := h.next(h.rootCalls, "root launch")
	first.reply <- map[string]any{"tool_calls": []any{wireTool("launch", "agent_start", params)}}
	second := h.next(h.rootCalls, "root receives launch")
	var launched struct {
		AgentID string `json:"agent_id"`
	}
	for _, raw := range second.body["messages"].([]any) {
		message := raw.(map[string]any)
		if message["role"] == "tool" && message["tool_call_id"] == "launch" {
			content, _ := message["content"].(string)
			_ = json.Unmarshal([]byte(content), &launched)
		}
	}
	if launched.AgentID == "" {
		h.t.Fatal("launch returned no agent id")
	}
	second.reply <- map[string]any{"content": "Launched."}
	return launched.AgentID
}

func (h *childWireHarness) finish(call wireCall, rootID string) {
	h.t.Helper()
	call.reply <- map[string]any{"tool_calls": []any{wireTool("report", "publish_report", map[string]any{
		"recipient_ids": []string{rootID}, "kind": "final", "summary": "Done.", "body": "Done.",
	})}}
	last := h.next(h.children, "child receives its report")
	last.reply <- map[string]any{"content": "Done."}
	joined := make(chan struct{})
	go func() { h.session.SubAgentRunner().Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		h.t.Fatal("child did not settle")
	}
}

// requireSettings asserts the request settings a child request carried.
func requireSettings(t *testing.T, phase string, body map[string]any, model, effort string, temperature any) {
	t.Helper()
	if body["model"] != model || body["reasoning_effort"] != effort || body["temperature"] != temperature {
		t.Fatalf("%s: child request sent model=%v reasoning_effort=%v temperature=%v; want %s/%s/%v",
			phase, body["model"], body["reasoning_effort"], body["temperature"], model, effort, temperature)
	}
}

// The configured role thinking effort and sampling reach the wire on every
// path that builds a child provider: launch and the first supervised attempt,
// a parent's continue after a stop, and a parent's request-boundary model
// switch. Children once silently sent provider defaults on all of them.
func TestChildRequestsCarryConfiguredThinkingAndSamplingOnEveryPath(t *testing.T) {
	temperature := 0.25
	h := newChildWireHarness(t, CategoryConfig{AccountID: "account", Model: wireChildModel, ThinkingEffort: "high", ModelTemperature: &temperature})
	child := h.launch(map[string]any{})

	first := h.next(h.children, "child first request")
	requireSettings(t, "first request", first.body, wireChildModel, "high", 0.25)
	first.reply <- map[string]any{"status": http.StatusUnauthorized}
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.session.recoveryMu.Lock()
		checkpoint, parked := h.session.pendingRecoveries[child]
		h.session.recoveryMu.Unlock()
		if parked {
			if err := h.session.RetryRecoveryFor(child, checkpoint.Status.ID, checkpoint.Status.Generation); err != nil {
				t.Fatalf("parent continue: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not stop on rejected credentials")
		}
		time.Sleep(10 * time.Millisecond)
	}

	continued := h.next(h.children, "child request after continue")
	requireSettings(t, "continued request", continued.body, wireChildModel, "high", 0.25)
	ctx := context.Background()
	if _, err := h.session.configureChild(ctx, h.root.ID, map[string]any{"agent_id": child, "model": "wire-child-2", "thinking_effort": "low"}); err != nil {
		t.Fatalf("parent configure: %v", err)
	}
	continued.reply <- map[string]any{"tool_calls": []any{wireTool("look", "read_file", map[string]any{"path": "missing.txt"})}}
	switched := h.next(h.children, "child request after model switch")
	// A different model resolves its own sampling; the role's applied to the old one.
	requireSettings(t, "switched request", switched.body, "wire-child-2", "low", nil)
	found := false
	for len(h.sender.ch) > 0 {
		event := <-h.sender.ch
		if identity, ok := event.data.(ttypes.TurnIdentity); ok && event.typ == "connection_changed" && event.agentID == child && identity.ModelID == "wire-child-2" {
			found = true
		}
	}
	if !found {
		t.Fatal("the UI was not told which model produces the child's output")
	}
	h.finish(switched, h.root.ID)
	completed := h.session.CompletedSubAgentSnapshots()
	if len(completed) != 1 || completed[0].Result.Status != agent.SubAgentCompleted {
		t.Fatalf("child did not complete after the switch: %+v", completed)
	}
}

// A parent's explicit launch choice wins over the role's model and effort.
func TestAgentStartExplicitModelAndEffortReachTheWire(t *testing.T) {
	temperature := 0.25
	h := newChildWireHarness(t, CategoryConfig{AccountID: "account", Model: wireChildModel, ThinkingEffort: "high", ModelTemperature: &temperature})
	h.launch(map[string]any{"model": "chosen-model", "thinking_effort": "max"})
	first := h.next(h.children, "child first request")
	requireSettings(t, "explicit launch", first.body, "chosen-model", "max", nil)
	h.finish(first, h.root.ID)
}

func TestParkedChildIsActionableOnlyAfterAttemptTeardown(t *testing.T) {
	h := newChildWireHarness(t, CategoryConfig{AccountID: "account", Model: wireChildModel})
	child := h.launch(map[string]any{})
	first := h.next(h.children, "child first request")
	runner := h.session.SubAgentRunner()
	// Retain an ordinary write lease to stop deferred claim release. This
	// exposes the real teardown window deterministically, without a sleep in
	// production or a test-only runtime callback.
	release, err := runner.FileOwnership().BeginWrite(agent.AgentID(child), nil)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	unblock := func() { once.Do(release) }
	defer unblock()
	first.reply <- map[string]any{"status": http.StatusUnauthorized}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, active := runner.mgr.GetActive(agent.AgentID(child)); !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not enter attempt teardown")
		}
		time.Sleep(time.Millisecond)
	}
	h.session.recoveryMu.Lock()
	_, prematurelyParked := h.session.pendingRecoveries[child]
	h.session.recoveryMu.Unlock()
	if prematurelyParked || h.session.agentRecovery(child) != nil {
		t.Fatal("Continue was exposed while the old attempt still owned cleanup")
	}
	unblock()
	for h.session.agentRecovery(child) == nil {
		if time.Now().After(deadline) {
			t.Fatal("finished teardown did not expose recovery")
		}
		time.Sleep(time.Millisecond)
	}
	incident := h.session.agentRecovery(child)
	old, err := h.session.CollaborationStore().Agent(t.Context(), child)
	if err != nil || old.CurrentRunID != "" || old.Presence != collaboration.RuntimeOffline {
		t.Fatalf("actionable recovery retained the old attachment/run: %+v %v", old, err)
	}
	if err := h.session.RetryRecoveryFor(child, incident.ID, incident.Generation); err != nil {
		t.Fatalf("immediate Continue failed: %v", err)
	}
	continued := h.next(h.children, "replacement child request")
	current, err := h.session.CollaborationStore().Agent(t.Context(), child)
	if err != nil || current.Presence != collaboration.RuntimeAttached || current.CurrentRunID == "" || current.RuntimeEpoch <= old.RuntimeEpoch {
		t.Fatalf("replacement lost its attachment: %+v %v", current, err)
	}
	h.finish(continued, h.root.ID)
}
