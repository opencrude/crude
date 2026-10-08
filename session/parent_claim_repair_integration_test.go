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
	"crude/fileio"
	"crude/provider"
	"crude/tools"
	recoverytypes "crude/types/recovery"
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
		"terminal_run_id": run.ID, "expected_run_version": int(finished.Version),
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
	if !ownershipScopeAllows([]string{repaired.Claims[0].Path}, narrow) || ownershipScopeAllows([]string{repaired.Claims[0].Path}, sibling) {
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

func TestParentClaimRepairProviderJourneyRequiresExplicitContinuation(t *testing.T) {
	const rootModel, childModel = "claim-repair-root", "claim-repair-child"
	const task = "Create allowed.txt once, leave all other files untouched, and publish a final report to the parent."
	const accepted = "Retain this accepted child prefix across the ownership repair."
	const content = "written once after explicit continuation\n"
	type providerCall struct {
		request provider.ChatRequest
		reply   chan map[string]any
	}
	rootCalls, childCalls := make(chan providerCall, 8), make(chan providerCall, 8)
	var rootRequests, childRequests, prepared, committed atomic.Int32
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
	mutations := make(chan HistoryMutation, 8)
	s.SetHistoryMutationHook(func(_ context.Context, mutation HistoryMutation) error {
		if mutation.Committed == nil {
			prepared.Add(1)
		} else if *mutation.Committed {
			committed.Add(1)
		}
		mutations <- mutation
		return nil
	})
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
			t.Fatalf("%s: provider request missing (root=%d child=%d prepared=%d committed=%d)", phase, rootRequests.Load(), childRequests.Load(), prepared.Load(), committed.Load())
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
	result := func(request provider.ChatRequest, id string) provider.ChatMessage {
		t.Helper()
		var found []provider.ChatMessage
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolCallID == id {
				found = append(found, message)
			}
		}
		if len(found) != 1 || found[0].ToolError || agent.IsToolError(found[0].ContentString()) {
			t.Fatalf("provider input did not contain one successful result for %s: %+v", id, found)
		}
		return found[0]
	}
	decode := func(message provider.ChatMessage, into any) {
		t.Helper()
		if err := json.Unmarshal([]byte(message.ContentString()), into); err != nil {
			t.Fatalf("decode real tool response %q: %v", message.ContentString(), err)
		}
	}
	assertAbsent := func() {
		t.Helper()
		for _, name := range []string{"outside.txt", "allowed.txt", "unused.txt", "parent.txt", "parent-unused.txt"} {
			if _, err := os.Lstat(filepath.Join(project, name)); !os.IsNotExist(err) {
				t.Fatalf("unexpected file effect at %s: %v", name, err)
			}
		}
		if prepared.Load() != 0 || committed.Load() != 0 {
			t.Fatalf("blocked/repaired child reached the real mutation path: prepared=%d committed=%d", prepared.Load(), committed.Load())
		}
	}
	joinChild := func(runner *SubAgentRunner) {
		t.Helper()
		joined := make(chan struct{})
		go func() { runner.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Fatal("real child worker did not settle within its bound")
		}
	}

	first := next(rootCalls, "root launch")
	for _, name := range []string{"agent_start", "agent_inspect", "agent_wait", "agent_narrow_claims", "agent_update_claims"} {
		found := false
		for _, definition := range first.request.Tools {
			found = found || definition.Function.Name == name
		}
		if !found {
			t.Fatalf("actual root request did not offer public %s", name)
		}
	}
	parent, err := store.Agent(t.Context(), root.ID)
	if err != nil || parent.CurrentRunID == "" {
		t.Fatalf("actual root run has not begun: %+v err=%v", parent, err)
	}
	parentRun, err := store.Run(t.Context(), parent.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture-only unrelated ownership is seeded after the real root run begins.
	parentPaths := []string{filepath.Join(project, "parent.txt"), filepath.Join(project, "parent-unused.txt")}
	if _, err := store.AcquireClaims(t.Context(), collaboration.AcquireClaimsRequest{
		AgentID: parent.ID, RunID: parentRun.ID, RequestID: parentRun.RequestID, Paths: parentPaths,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.FileOwnership().ReplaceClaims(agent.AgentID(parent.ID), parent.Name, parentPaths); err != nil {
		t.Fatal(err)
	}
	first.reply <- map[string]any{"tool_calls": []any{tool(0, "claim-launch", "agent_start", map[string]any{
		"description": task, "category": CategoryCrafter, "file_ownership": []string{"allowed.txt", "unused.txt"},
	})}}
	initialChild := next(childCalls, "child ownership violation")
	second := next(rootCalls, "root receives allocated child")
	var launched struct {
		AgentID    string `json:"agent_id"`
		Name       string `json:"name"`
		RunID      string `json:"run_id"`
		RunVersion uint64 `json:"run_version"`
	}
	decode(result(second.request, "claim-launch"), &launched)
	if launched.AgentID == "" || launched.RunID == "" || launched.RunVersion == 0 {
		t.Fatalf("real launch did not return exact child/run identities: %+v", launched)
	}
	runner := s.SubAgentRunner()
	initialChild.reply <- map[string]any{"content": accepted, "tool_calls": []any{tool(0, "claim-outside-write", "write_file", map[string]any{
		"path": "outside.txt", "content": "must never be written",
	})}}
	joinChild(runner)
	s.recoveryMu.Lock()
	checkpoint, parked := s.pendingRecoveries[launched.AgentID]
	s.recoveryMu.Unlock()
	status := checkpoint.Status
	if !parked || status.State != recoverytypes.NeedsAttention || status.ID == "" || status.Generation == 0 || !status.RetryAt.IsZero() ||
		!strings.Contains(status.Detail, "outside this agent's committed ownership scope") || !strings.Contains(status.Action, "Continue") ||
		runner.Running() || runner.Manager().ActiveCount() != 0 || len(runner.Manager().CompletedResults()) != 0 || childRequests.Load() != 1 {
		t.Fatalf("structural child did not park its unfinished task: %+v", checkpoint)
	}
	assertAbsent()
	_, parkedRows, _, err := loadSubAgentSnapshotDB(s.completedSubAgentDBPath(launched.AgentID), launched.AgentID)
	if err != nil || len(parkedRows) < 3 || parkedRows[0].Msg.InternalID != checkpoint.PendingInput.InternalID || parkedRows[0].Meta.DisplayRole != "assignment" {
		t.Fatalf("structural child did not persist its original assignment/evidence: rows=%+v err=%v", parkedRows, err)
	}
	// Observe the terminal run through the offered public wait, not a fabricated
	// supervisor/claim result. Inspect currently exposes only active runs.
	second.reply <- map[string]any{"tool_calls": []any{
		tool(0, "claim-inspect-child", "agent_inspect", map[string]any{"agent_id": launched.AgentID}),
		tool(1, "claim-wait-child", "agent_wait", map[string]any{"agent_id": launched.AgentID, "run_id": launched.RunID, "timeout_seconds": 2}),
		tool(2, "claim-inspect-parent", "agent_inspect", map[string]any{"agent_id": root.ID}),
	}}
	third := next(rootCalls, "root receives incident and claim diagnostics")
	var childInspection, parentInspection struct {
		Agent  collaboration.Agent   `json:"agent"`
		Run    *collaboration.Run    `json:"run"`
		Claims []collaboration.Claim `json:"claims"`
	}
	decode(result(third.request, "claim-inspect-child"), &childInspection)
	decode(result(third.request, "claim-inspect-parent"), &parentInspection)
	var terminal collaborationWaitObservation
	decode(result(third.request, "claim-wait-child"), &terminal)
	if childInspection.Agent.ID != launched.AgentID || childInspection.Agent.ParentID != root.ID || childInspection.Agent.CurrentRunID != "" ||
		terminal.Run == nil || terminal.Run.ID != launched.RunID || terminal.Run.AgentID != launched.AgentID || terminal.Run.State != collaboration.RunInterrupted || terminal.Run.Version <= launched.RunVersion ||
		parentInspection.Run == nil || parentInspection.Run.ID != parentRun.ID || parentInspection.Run.Version != parentRun.Version || len(parentInspection.Claims) != 2 {
		t.Fatalf("provider diagnostics lost exact child/parent/run/claim preimages: child=%+v terminal=%+v parent=%+v", childInspection, terminal, parentInspection)
	}
	notices := 0
	for _, message := range third.request.Messages {
		text := html.UnescapeString(message.ContentString())
		if strings.Contains(text, status.ID) && strings.Contains(text, "needs attention") {
			for _, exact := range []string{`origin="runtime"`, `sender_id="` + launched.AgentID + `"`, launched.Name, fmt.Sprintf("generation %d", status.Generation), status.Detail, status.Action} {
				if !strings.Contains(text, exact) {
					t.Errorf("actual parent incident notification omitted %q: %s", exact, text)
				}
			}
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("actual root input has %d structural incident notifications, want one", notices)
	}
	versions := func(claims []collaboration.Claim) map[string]uint64 {
		values := make(map[string]uint64, len(claims))
		for _, claim := range claims {
			values[claim.ID] = claim.Version
		}
		return values
	}
	third.reply <- map[string]any{"tool_calls": []any{
		tool(0, "claim-narrow-parent", "agent_narrow_claims", map[string]any{
			"runtime_epoch": parentInspection.Agent.RuntimeEpoch, "run_id": parentInspection.Run.ID, "expected_run_version": parentInspection.Run.Version,
			"expected_versions": versions(parentInspection.Claims), "paths": parentPaths[:1],
		}),
		tool(1, "claim-repair-child", "agent_update_claims", map[string]any{
			"agent_id": childInspection.Agent.ID, "runtime_epoch": childInspection.Agent.RuntimeEpoch,
			"terminal_run_id": terminal.Run.ID, "expected_run_version": terminal.Run.Version,
			"expected_versions": versions(childInspection.Claims), "paths": []string{"allowed.txt"},
		}),
	}}
	fourth := next(rootCalls, "root receives public repair metadata")
	var narrowed collaboration.NarrowActiveClaimsResult
	var repaired collaboration.UpdateClaimsResult
	decode(result(fourth.request, "claim-narrow-parent"), &narrowed)
	decode(result(fourth.request, "claim-repair-child"), &repaired)
	allowed := filepath.Join(project, "allowed.txt")
	if len(narrowed.Released) != 2 || len(narrowed.Claims) != 1 || narrowed.Claims[0].Path != parentPaths[0] || narrowed.Claims[0].AgentID != root.ID || narrowed.Claims[0].RunID != parentRun.ID ||
		len(repaired.Claims) != 1 || repaired.Claims[0].Path != allowed || repaired.Claims[0].AgentID != launched.AgentID || repaired.Claims[0].RunID != "" ||
		repaired.Audit.ID == "" || repaired.Audit.SenderID != root.ID || repaired.Audit.RunID != launched.RunID || len(repaired.Audit.Recipients) != 1 || repaired.Audit.Recipients[0].ID != launched.AgentID {
		t.Fatalf("public repair did not return committed exact scope/audit metadata: narrowed=%+v repaired=%+v", narrowed, repaired)
	}
	if got := s.FileOwnership().WriteClaimsByAgent(agent.AgentID(root.ID)); !reflect.DeepEqual(got, parentPaths[:1]) {
		t.Fatalf("public self-narrow did not update the live guard: %v", got)
	}
	if got := s.FileOwnership().WriteClaimsByAgent(agent.AgentID(launched.AgentID)); !reflect.DeepEqual(got, []string{allowed}) {
		t.Fatalf("public child repair did not update the live guard: %v", got)
	}
	currentParent, err := store.Agent(t.Context(), root.ID)
	if err != nil || currentParent.CurrentRunID != parentRun.ID || currentParent.RuntimeEpoch != parent.RuntimeEpoch {
		t.Fatalf("self-narrow replaced or ended the owning root run: %+v err=%v", currentParent, err)
	}
	currentClaims, err := store.Claims(t.Context(), launched.AgentID, true)
	if err != nil || !reflect.DeepEqual(currentClaims, repaired.Claims) {
		t.Fatalf("public repair metadata differs from durable effective claims: %+v err=%v", currentClaims, err)
	}
	assertAbsent()
	if childRequests.Load() != 1 || runner.Running() {
		t.Fatal("public repair implicitly woke the child")
	}
	fourth.reply <- map[string]any{"content": "Claims repaired; explicit continuation is required before the child can write."}
	waitTurnDone(t, sender, root.ID)
	if childRequests.Load() != 1 || runner.Running() {
		t.Fatal("root completion implicitly continued repaired child work")
	}
	select {
	case <-childCalls:
		t.Fatal("claim repair or idle delivery implicitly continued the child")
	case <-time.After(650 * time.Millisecond):
	}
	assertAbsent()
	if !s.RetryRecoveryFor(launched.AgentID, status.ID, status.Generation) {
		s.recoveryMu.Lock()
		pending := s.pendingRecoveries[launched.AgentID]
		s.recoveryMu.Unlock()
		claims, claimErr := store.Claims(t.Context(), launched.AgentID, true)
		t.Fatalf("exact human Continue could not start the real repaired child: incident=%+v claims=%+v err=%v", pending.Status, claims, claimErr)
	}
	continued := next(childCalls, "explicit child continuation")
	handle, active := runner.Manager().GetActive(agent.AgentID(launched.AgentID))
	if !active {
		t.Fatal("continued child has no actual run owner")
	}
	newRunID, _ := handle.RunIdentity()
	newRun, err := store.Run(t.Context(), newRunID)
	if err != nil || newRunID == launched.RunID || newRun.AgentID != launched.AgentID || newRun.RuntimeEpoch != handle.RuntimeEpoch || newRun.State != collaboration.RunRunning || newRun.RequestID != "claim-launch" ||
		!reflect.DeepEqual(handle.Spec.FileOwnership, []string{allowed}) || handle.Spec.Description != task {
		t.Fatalf("Continue lost assignment, new attempt identity, or repaired scope: run=%+v spec=%+v err=%v", newRun, handle.Spec, err)
	}
	assignmentCount, prefixCount, rejectedCount := 0, 0, 0
	for _, message := range continued.request.Messages {
		if message.Role == "user" && message.ContentString() == checkpoint.PendingInput.Text {
			assignmentCount++
		}
		if message.Role == "assistant" && message.ContentString() == accepted {
			prefixCount++
		}
		if message.Role == "tool" && message.ToolCallID == "claim-outside-write" && strings.Contains(message.ContentString(), "outside this agent's committed ownership scope") {
			rejectedCount++
		}
	}
	if assignmentCount != 1 || prefixCount != 1 || rejectedCount != 1 {
		t.Fatalf("actual continued provider input lost/duplicated accepted assignment/prefix/blocked evidence: assignment=%d prefix=%d rejection=%d", assignmentCount, prefixCount, rejectedCount)
	}
	activeClaims, err := store.Claims(t.Context(), launched.AgentID, true)
	if err != nil || len(activeClaims) != 1 || activeClaims[0].Path != allowed || activeClaims[0].RunID != newRunID {
		t.Fatalf("replacement run does not own exactly the repaired narrow scope: %+v err=%v", activeClaims, err)
	}
	continued.reply <- map[string]any{"tool_calls": []any{tool(0, "claim-allowed-write", "write_file", map[string]any{"path": "allowed.txt", "content": content})}}
	reportCall := next(childCalls, "child publishes required final report")
	result(reportCall.request, "claim-allowed-write")
	if data, err := os.ReadFile(allowed); err != nil || string(data) != content || prepared.Load() != 1 || committed.Load() != 1 {
		t.Fatalf("continued provider did not perform exactly one successful real write: bytes=%q err=%v prepared=%d committed=%d", data, err, prepared.Load(), committed.Load())
	}
	nextMutation := func() HistoryMutation {
		t.Helper()
		select {
		case mutation := <-mutations:
			return mutation
		case <-time.After(5 * time.Second):
			t.Fatal("real mutation receipt/completion was not observed")
			return HistoryMutation{}
		}
	}
	mutation, completion := nextMutation(), nextMutation()
	if mutation.AgentID != launched.AgentID || mutation.SessionID != s.SessionID() || mutation.MessageID != "claim-repair-human" ||
		mutation.Receipt.OperationName != "write_file" || len(mutation.Receipt.Paths) != 1 || mutation.Receipt.Paths[0].Path != allowed || mutation.Receipt.Paths[0].Before.Kind != fileio.MutationAbsent || string(mutation.Receipt.Paths[0].After.Bytes) != content ||
		completion.Receipt.OperationID != mutation.Receipt.OperationID || completion.Committed == nil || !*completion.Committed {
		t.Fatalf("real write receipt lost exact owner/preimage/completion: mutation=%+v completion=%+v", mutation, completion)
	}
	reportCall.reply <- map[string]any{"tool_calls": []any{tool(0, "claim-final-report", "publish_report", map[string]any{
		"recipient_ids": []string{root.ID}, "run_id": newRunID, "request_id": "claim-launch", "kind": "final",
		"summary": "Wrote allowed.txt once after explicit Continue.", "body": "Only allowed.txt was written. The rejected outside.txt call had no file effect.",
	})}}
	lastChild := next(childCalls, "child receives final report publication")
	var published collaboration.PublishReportResult
	decode(result(lastChild.request, "claim-final-report"), &published)
	if published.Artifact.ID == "" || published.Artifact.Kind != collaboration.ReportFinal || published.Artifact.AuthorID != launched.AgentID || published.Artifact.RunID != newRunID ||
		len(published.Publish.Event.Recipients) != 1 || published.Publish.Event.Recipients[0].ID != root.ID {
		t.Fatalf("provider report did not belong to the real child/new run/exact parent: %+v", published)
	}
	lastChild.reply <- map[string]any{"content": "Completed the retained assignment and published the final report."}
	joinChild(runner)
	finished, err := store.Run(t.Context(), newRunID)
	if err != nil || finished.State != collaboration.RunSucceeded || !handle.ReportDone() || childRequests.Load() != 4 || prepared.Load() != 1 || committed.Load() != 1 {
		t.Fatalf("real repaired child did not complete exactly once with its required report: run=%+v report=%v requests=%d prepared=%d committed=%d err=%v", finished, handle.ReportDone(), childRequests.Load(), prepared.Load(), committed.Load(), err)
	}
	for _, name := range []string{"outside.txt", "unused.txt", "parent.txt", "parent-unused.txt"} {
		if _, err := os.Lstat(filepath.Join(project, name)); !os.IsNotExist(err) {
			t.Fatalf("narrow child scope permitted an unrelated file effect at %s: %v", name, err)
		}
	}
	before := s.CompletedSubAgentSnapshots()
	if len(before) != 1 || before[0].Result.Status != agent.SubAgentCompleted || before[0].Result.ReportID != published.Artifact.ID ||
		len(before[0].Messages) < len(parkedRows) || !reflect.DeepEqual(before[0].Messages[:len(parkedRows)], parkedRows) {
		t.Fatalf("completion changed the parked child's immutable prefix or lost its report: %+v", before)
	}
	path := s.dbPath
	s.Close()
	rootBefore := rootRequests.Load()
	resumed, err := ResumeSessionWithDir(path, cfg, home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.SetWorkDir(project)
	resumed.SetCollaboration(store, collaboration.NewInstanceID())
	if _, err := resumed.NewOrchestrator(newMockEventSender()); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(650 * time.Millisecond) // Cover the passive idle-delivery cadence.
	defer timer.Stop()
	select {
	case <-childCalls:
		t.Fatal("passive resume launched child provider work")
	case <-timer.C:
	}
	after := resumed.CompletedSubAgentSnapshots()
	if len(after) != 1 || !reflect.DeepEqual(before[0].Messages, after[0].Messages) || after[0].Result.ReportID != published.Artifact.ID ||
		resumed.SubAgentRunner().Running() || resumed.SubAgentRunner().Manager().ActiveCount() != 0 || len(resumed.PendingSubAgentSnapshots()) != 0 ||
		rootRequests.Load() != rootBefore || childRequests.Load() != 4 || prepared.Load() != 1 || committed.Load() != 1 {
		t.Fatalf("passive resume changed evidence or replayed provider/file work: root=%d/%d child=%d prepared=%d committed=%d snapshots=%+v", rootRequests.Load(), rootBefore, childRequests.Load(), prepared.Load(), committed.Load(), after)
	}
}
