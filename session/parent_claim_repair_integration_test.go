package session

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/tools"
)

func TestParentClaimRepairToolProjectsOnlyCommittedNarrowScope(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	workDir := t.TempDir()
	s.SetWorkDir(workDir)
	broad := filepath.Join(workDir, "src")
	narrow := filepath.Join(broad, "owned.go")
	sibling := filepath.Join(broad, "sibling.go")
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: "assignment-tool-call", Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: child.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{broad},
	}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		ExpectedVersion: run.Version, State: collaboration.RunInterrupted, Outcome: "guidance blocked before effects",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{ToolCallID: "repair-call"})
	toolResult, handled, err := s.executeCollaborationTool(ctx, parent.ID, "agent_update_claims", map[string]any{
		"agent_id": child.ID, "runtime_epoch": int(child.RuntimeEpoch),
		"run_id": run.ID, "expected_run_version": int(finished.Version),
		"expected_versions": map[string]any{}, "paths": []any{narrow},
	})
	if err != nil || !handled {
		t.Fatalf("agent_update_claims: handled=%v result=%+v err=%v", handled, toolResult, err)
	}
	var repaired collaboration.UpdateClaimsResult
	if err := json.Unmarshal([]byte(toolResult.Content), &repaired); err != nil {
		t.Fatalf("decode repair result %q: %v", toolResult.Content, err)
	}
	if len(repaired.Claims) != 1 || repaired.Claims[0].Path != filepath.Clean(narrow) || repaired.Audit.ID == "" {
		t.Fatalf("repair result = %+v", repaired)
	}
	projection := agent.NewFileOwnership()
	if err := projection.PreClaim(agent.AgentID(child.ID), child.Name, []string{broad}); err != nil {
		t.Fatal(err)
	}
	if err := projection.ReplaceClaims(agent.AgentID(child.ID), child.Name, []string{repaired.Claims[0].Path}); err != nil {
		t.Fatal(err)
	}
	if err := projection.CheckOrClaim("other", "Other", sibling, true); err != nil {
		t.Fatalf("old broad scope survived repair projection: %v", err)
	}
	if err := projection.CheckOrClaim("other", "Other", narrow, true); err == nil {
		t.Fatal("committed narrow scope was not enforced")
	}
	if !projection.WriteScopeAllows(agent.AgentID(child.ID), narrow) || projection.WriteScopeAllows(agent.AgentID(child.ID), sibling) {
		t.Fatal("resumed tool guard did not enforce the committed narrow scope")
	}
	active, err := store.Claims(t.Context(), child.ID, true)
	if err != nil || len(active) != 1 || active[0].Path != filepath.Clean(narrow) || active[0].RunID != "" {
		t.Fatalf("durable repaired claims = %+v err=%v", active, err)
	}
	if _, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, RequestID: run.RequestID, Origin: collaboration.OriginPeer,
	}); err != nil {
		t.Fatalf("explicit Continue could not create a fresh run identity: %v", err)
	}
}

func TestActiveActorCanNarrowOwnClaimsWithoutEndingRun(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	workDir := t.TempDir()
	s.SetWorkDir(workDir)
	broad := filepath.Join(workDir, "src")
	narrow := filepath.Join(broad, "owned.go")
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-active-task", Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{broad},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FileOwnership().ReplaceClaims(agent.AgentID(parent.ID), parent.Name, []string{broad}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "narrow-call"})
	result, handled, err := s.executeCollaborationTool(ctx, parent.ID, "agent_narrow_claims", map[string]any{
		"runtime_epoch": int(parent.RuntimeEpoch), "run_id": run.ID, "expected_run_version": int(run.Version),
		"expected_versions": map[string]any{claims[0].ID: int(claims[0].Version)}, "paths": []any{narrow},
	})
	if err != nil || !handled {
		t.Fatalf("narrow claims: handled=%v result=%+v err=%v", handled, result, err)
	}
	if got := s.FileOwnership().WriteClaimsByAgent(agent.AgentID(parent.ID)); len(got) != 1 || got[0] != filepath.Clean(narrow) {
		t.Fatalf("local projection = %v", got)
	}
	active, err := store.Claims(t.Context(), parent.ID, true)
	if err != nil || len(active) != 1 || active[0].Path != filepath.Clean(narrow) || active[0].RunID != run.ID {
		t.Fatalf("durable claims = %+v err=%v", active, err)
	}
	actor, err := store.Agent(t.Context(), parent.ID)
	if err != nil || actor.CurrentRunID != run.ID {
		t.Fatalf("active run changed: %+v err=%v", actor, err)
	}
}

func TestClaimToolRejectsInexactNumericPreimages(t *testing.T) {
	for _, value := range []any{float64(1.5), float64(0), float64(-1), float64(1 << 54), "1"} {
		if _, err := exactClaimValue("version", value); err == nil {
			t.Fatalf("accepted invalid claim version %#v", value)
		}
	}
	if got, err := exactClaimValue("version", float64(7)); err != nil || got != 7 {
		t.Fatalf("exact integer parse = %d, %v", got, err)
	}
}

func TestClaimToolsRejectAmbiguousRelinquishment(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "retain-scope", Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "exact-narrow"})
	for _, raw := range []any{nil, "src", []any{1}, []any{"src/owned.go", 1}} {
		params := map[string]any{"runtime_epoch": parent.RuntimeEpoch, "run_id": run.ID, "expected_run_version": run.Version, "expected_versions": map[string]uint64{claims[0].ID: claims[0].Version}}
		if raw != nil {
			params["paths"] = raw
		}
		if _, handled, err := s.executeClaimsTool(ctx, parent.ID, "agent_narrow_claims", params); err == nil || !handled {
			t.Fatalf("ambiguous paths relinquished claims: paths=%#v handled=%v error=%v", raw, handled, err)
		}
	}
	remaining, err := store.Claims(t.Context(), parent.ID, true)
	if err != nil || !reflect.DeepEqual(remaining, claims) {
		t.Fatalf("invalid repair changed authoritative claims: %+v error=%v", remaining, err)
	}
}

// TestChildScopeRejectionKeepsLoopAliveAndParentWidensLiveScope reproduces the
// Harriet incident through the real provider/tool path: a scoped child writes a
// scratch file in its private temporary directory and a file outside its scope.
// The scratch write needs no claim, the outside write is an ordinary tool error,
// the run keeps going, the parent is told, and the parent widens the running
// child's scope so its retry succeeds. Nothing parks or needs a human.
func TestChildScopeRejectionKeepsLoopAliveAndParentWidensLiveScope(t *testing.T) {
	const rootModel, childModel = "claim-repair-root", "claim-repair-child"
	const task = "Create allowed.txt and outside.txt, then publish a final report to the parent."
	const content = "written by the child\n"
	type providerCall struct {
		request provider.ChatRequest
		reply   chan map[string]any
	}
	rootCalls, childCalls := make(chan providerCall, 8), make(chan providerCall, 8)
	var rootRequests, childRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := providerCall{request: request, reply: make(chan map[string]any, 1)}
		var calls chan providerCall
		switch request.Model {
		case rootModel:
			if rootRequests.Add(1) > 4 {
				// New addressed reports/outcomes may legitimately wake the idle root.
				call.reply <- map[string]any{"content": "Child evidence received."}
			} else {
				calls = rootCalls
			}
		case childModel:
			childRequests.Add(1)
			calls = childCalls
		default:
			t.Errorf("unexpected provider model %q", request.Model)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if calls != nil {
			select {
			case calls <- call:
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Second):
				t.Error("provider request was not observed within its bound")
				return
			}
		}
		select {
		case delta := <-call.reply:
			finish := "stop"
			if _, ok := delta["tool_calls"]; ok {
				finish = "tool_calls"
			}
			frame, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
			if err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			t.Error("scripted provider response was not released within its bound")
		}
	}))
	defer server.Close()

	home, project := t.TempDir(), t.TempDir()
	sessions := filepath.Join(home, "sessions")
	cfg := modelAttributionConfig(server.URL, rootModel)
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
	store := attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(context.Context, string, string) error { return nil }, nil)
	s.SetHistoryMutationHook(func(context.Context, HistoryMutation) error { return nil })
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !root.SubmitInput(provider.UserInput{InternalID: "claim-repair-human", Text: task}) {
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
	tool := func(index int, id, name string, params map[string]any) map[string]any {
		t.Helper()
		arguments, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}
	}
	toolMessage := func(request provider.ChatRequest, id string) provider.ChatMessage {
		t.Helper()
		var found []provider.ChatMessage
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolCallID == id {
				found = append(found, message)
			}
		}
		if len(found) != 1 {
			t.Fatalf("provider input did not contain exactly one result for %s: %+v", id, found)
		}
		return found[0]
	}
	result := func(request provider.ChatRequest, id string) provider.ChatMessage {
		t.Helper()
		message := toolMessage(request, id)
		if message.ToolError || agent.IsToolError(message.ContentString()) {
			t.Fatalf("tool %s failed: %s", id, message.ContentString())
		}
		return message
	}
	decode := func(message provider.ChatMessage, into any) {
		t.Helper()
		if err := json.Unmarshal([]byte(message.ContentString()), into); err != nil {
			t.Fatalf("decode real tool response %q: %v", message.ContentString(), err)
		}
	}
	noOpenIncident := func(phase string) {
		t.Helper()
		s.recoveryMu.Lock()
		open := len(s.pendingRecoveries)
		s.recoveryMu.Unlock()
		if open != 0 {
			t.Fatalf("%s: a recovery incident was opened for an ordinary tool failure", phase)
		}
	}

	first := next(rootCalls, "root launch")
	first.reply <- map[string]any{"tool_calls": []any{tool(0, "claim-launch", "agent_start", map[string]any{
		"description": task, "category": CategoryCrafter, "isolation": "shared", "file_ownership": []string{"allowed.txt"},
	})}}
	initialChild := next(childCalls, "child first request")
	second := next(rootCalls, "root receives allocated child")
	var launched struct {
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
		RunID   string `json:"run_id"`
	}
	decode(result(second.request, "claim-launch"), &launched)
	if launched.AgentID == "" || launched.RunID == "" {
		t.Fatalf("real launch did not return exact child/run identities: %+v", launched)
	}
	scratch := filepath.Join(s.TempDir(), "agents", launched.AgentID, "write_picker.ps1")
	outside := filepath.Join(project, "outside.txt")
	initialChild.reply <- map[string]any{"tool_calls": []any{
		tool(0, "scratch-write", "write_file", map[string]any{"path": scratch, "content": "scratch\n"}),
		tool(1, "outside-write", "write_file", map[string]any{"path": "outside.txt", "content": content}),
	}}
	// The loop is sacred: the child makes its next request with both results.
	childAfterRejection := next(childCalls, "child continues after its rejected write")
	result(childAfterRejection.request, "scratch-write")
	rejected := toolMessage(childAfterRejection.request, "outside-write")
	if !rejected.ToolError && !agent.IsToolError(rejected.ContentString()) ||
		!strings.Contains(rejected.ContentString(), "outside your file scope") || !strings.Contains(rejected.ContentString(), "your run continues") {
		t.Fatalf("outside write was not an actionable tool error: %q", rejected.ContentString())
	}
	if data, err := os.ReadFile(scratch); err != nil || string(data) != "scratch\n" {
		t.Fatalf("private scratch write did not succeed: %q %v", data, err)
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("rejected write changed the project: %v", err)
	}
	claims, err := store.Claims(t.Context(), launched.AgentID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		if strings.HasPrefix(claim.Path, s.TempDir()) {
			t.Fatalf("private scratch file entered the shared ownership ledger: %+v", claim)
		}
	}
	noOpenIncident("after rejected write")

	second.reply <- map[string]any{"tool_calls": []any{tool(0, "inspect-child", "agent_inspect", map[string]any{"agent_id": launched.AgentID})}}
	third := next(rootCalls, "root inspects its running child")
	var inspection struct {
		Agent    collaboration.Agent   `json:"agent"`
		Run      *collaboration.Run    `json:"run"`
		Recovery *agentRecoveryView    `json:"recovery"`
		Claims   []collaboration.Claim `json:"claims"`
	}
	decode(result(third.request, "inspect-child"), &inspection)
	if inspection.Run == nil || inspection.Run.ID != launched.RunID || inspection.Run.State.Terminal() || inspection.Recovery != nil {
		t.Fatalf("parent did not see a live, unparked child run: %+v", inspection)
	}
	notified := false
	for _, message := range third.request.Messages {
		text := html.UnescapeString(message.ContentString())
		notified = notified || strings.Contains(text, "was denied a write") && strings.Contains(text, outside) && strings.Contains(text, "still running")
	}
	if !notified {
		t.Fatal("parent was not told about its child's rejected write")
	}
	versions := make(map[string]uint64, len(inspection.Claims))
	for _, claim := range inspection.Claims {
		versions[claim.ID] = claim.Version
	}
	third.reply <- map[string]any{"tool_calls": []any{tool(0, "widen-child", "agent_update_claims", map[string]any{
		"agent_id": launched.AgentID, "runtime_epoch": inspection.Agent.RuntimeEpoch,
		"run_id": inspection.Run.ID, "expected_run_version": inspection.Run.Version,
		"expected_versions": versions, "paths": []string{"allowed.txt", "outside.txt"},
	})}}
	fourth := next(rootCalls, "root receives live scope update")
	var widened collaboration.UpdateClaimsResult
	decode(result(fourth.request, "widen-child"), &widened)
	if len(widened.Claims) != 2 || widened.Claims[0].RunID != launched.RunID {
		t.Fatalf("live scope update was not bound to the running attempt: %+v", widened)
	}
	if !s.FileOwnership().WriteScopeAllows(agent.AgentID(launched.AgentID), outside) {
		t.Fatal("live scope update did not reach the running child's write guard")
	}
	fourth.reply <- map[string]any{"content": "Scope widened; the child keeps working."}

	childAfterRejection.reply <- map[string]any{"tool_calls": []any{tool(0, "outside-retry", "write_file", map[string]any{"path": "outside.txt", "content": content})}}
	reportCall := next(childCalls, "child publishes required final report")
	result(reportCall.request, "outside-retry")
	if data, err := os.ReadFile(outside); err != nil || string(data) != content {
		t.Fatalf("retry inside the widened scope did not write: %q %v", data, err)
	}
	reportCall.reply <- map[string]any{"tool_calls": []any{tool(0, "final-report", "publish_report", map[string]any{
		"recipient_ids": []string{root.ID}, "run_id": launched.RunID, "request_id": "claim-launch", "kind": "final",
		"summary": "Wrote outside.txt after the parent widened scope.", "body": "The first outside write was rejected without effect; the retry succeeded.",
	})}}
	lastChild := next(childCalls, "child receives final report publication")
	result(lastChild.request, "final-report")
	lastChild.reply <- map[string]any{"content": "Done."}
	joined := make(chan struct{})
	go func() { s.SubAgentRunner().Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not settle")
	}
	finished, err := store.Run(t.Context(), launched.RunID)
	if err != nil || finished.State != collaboration.RunSucceeded {
		t.Fatalf("child did not complete its single original run: %+v err=%v", finished, err)
	}
	noOpenIncident("after completion")
	waitTurnDone(t, sender, root.ID)
}

// The common case states only the new scope: identities and claim versions
// default to the agent's current state and are still fenced by the store.
func TestClaimChangesNeedOnlyTheNewPaths(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	workDir := t.TempDir()
	s.SetWorkDir(workDir)
	broad := filepath.Join(workDir, "src")
	narrow := filepath.Join(broad, "owned.go")
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, RequestID: "parent-task", Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: parent.ID, RunID: run.ID, RequestID: run.RequestID, Paths: []string{broad},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FileOwnership().ReplaceClaims(agent.AgentID(parent.ID), parent.Name, []string{broad}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "narrow-paths-only"})
	result, handled, err := s.executeCollaborationTool(ctx, parent.ID, "agent_narrow_claims", map[string]any{"paths": []any{narrow}})
	if err != nil || !handled {
		t.Fatalf("paths-only narrow: handled=%v result=%+v err=%v", handled, result, err)
	}
	active, err := store.Claims(t.Context(), parent.ID, true)
	if err != nil || len(active) != 1 || active[0].Path != filepath.Clean(narrow) {
		t.Fatalf("narrowed claims = %+v err=%v", active, err)
	}
	stale := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: parent.ID, ToolCallID: "narrow-stale"})
	if _, _, err := s.executeCollaborationTool(stale, parent.ID, "agent_narrow_claims", map[string]any{
		"paths": []any{}, "expected_versions": map[string]any{active[0].ID: int(active[0].Version) + 1},
	}); err == nil {
		t.Fatal("an explicit stale claim version was not fenced")
	}
}
