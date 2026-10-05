package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
)

func collaborationContractFixture(t *testing.T) (*Session, *collaboration.Store, collaboration.Agent, collaboration.Agent) {
	t.Helper()
	root := t.TempDir()
	db := sessiondb.OpenAndMigrateAt(root)
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "contract-session"); err != nil {
		t.Fatal(err)
	}
	store, err := collaboration.Attach(db, "/contract/project", filepath.Join(root, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: "contract-parent", Name: "Parent", SessionID: "contract-session", Category: "crafter", OwnerInstance: "contract-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: "contract-child", Name: "Child", SessionID: "child-session", ParentID: parent.ID, Category: "crafter", OwnerInstance: "contract-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{db: db, sessionID: "contract-session", collaborationStore: store,
		collaborationContext: make(map[string][]provider.ChatMessage)}
	return s, store, parent, child
}

func TestCollaborationControlCannotCancelAReplacementRun(t *testing.T) {
	s, store, parent, target := collaborationContractFixture(t)
	old, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "old-run", AgentID: target.ID, RuntimeEpoch: target.RuntimeEpoch, Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	actorCtx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{ToolCallID: "delayed-cancel"})
	if _, handled, err := s.executeCollaborationTool(actorCtx, parent.ID, "agent_control",
		map[string]any{"agent_id": target.ID, "run_id": old.ID, "expected_version": float64(old.Version), "operation": "cancel"}); err != nil || !handled {
		t.Fatalf("publish control: handled=%t error=%v", handled, err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: old.ID, AgentID: target.ID, RuntimeEpoch: target.RuntimeEpoch, ExpectedVersion: old.Version, State: collaboration.RunSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		ID: "replacement-run", AgentID: target.ID, RuntimeEpoch: target.RuntimeEpoch, Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator(target.ID, s)
	o.RuntimeEpoch = target.RuntimeEpoch
	o.setCollaborationRun(replacement)
	activeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	o.turnCancel = cancel
	s.orchestrators = map[string]*Orchestrator{target.ID: o}
	deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{
		RecipientID: target.ID, RuntimeEpoch: target.RuntimeEpoch, OnlyKinds: []collaboration.EventKind{collaboration.EventLifecycleControl},
	})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("reserve control: count=%d error=%v", len(deliveries), err)
	}
	s.applyCollaborationControl(t.Context(), store, target, deliveries[0])
	current, err := store.Run(t.Context(), replacement.ID)
	if err != nil || current.State != collaboration.RunRunning || activeCtx.Err() != nil {
		t.Fatalf("old control affected replacement: state=%s cancelled=%t error=%v", current.State, activeCtx.Err() != nil, err)
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{Kind: collaboration.EventControlOutcome})
	if err != nil || len(page.Events) != 1 || page.Events[0].RunID != old.ID || !strings.Contains(page.Events[0].Body, "rejected") {
		t.Fatalf("exact rejection outcome missing: %+v, %v", page, err)
	}
}

func TestCollaborationInterimCannotSatisfyFinalReportContract(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	t.Cleanup(runner.SessionCancel)
	s.subAgentSup = runner
	handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: agent.AgentID(child.ID), ParentID: agent.AgentID(parent.ID), Name: child.Name, Category: "crafter"},
		Agent: &agent.Agent{Ctx: t.Context()}, StartedAt: time.Now()}
	handle.SetRunIdentity(run.ID, run.Version)
	if err := runner.Manager().Register(handle); err != nil {
		t.Fatal(err)
	}
	actorCtx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{ToolCallID: "interim-report"})
	if _, _, err := s.executeCollaborationTool(actorCtx, child.ID, "publish_report",
		map[string]any{"recipient_ids": []any{parent.ID}, "kind": "interim", "summary": "Pending", "body": "No final report."}); err != nil {
		t.Fatal(err)
	}
	handle.SetTermReason("missing_final_report")
	result := runner.buildSubAgentResult(handle, nil)
	if handle.ReportDone() || result.Status != agent.SubAgentFailedPolicy {
		t.Fatalf("interim satisfied final: ReportDone=%t status=%s", handle.ReportDone(), result.Status)
	}
}

func TestCollaborationReportNotificationCarriesExactReadableArtifact(t *testing.T) {
	_, store, parent, child := collaborationContractFixture(t)
	report, err := store.PublishReport(t.Context(), collaboration.PublishReportRequest{
		AuthorID: parent.ID, RecipientIDs: []string{child.ID}, IdempotencyKey: "evidence",
		Kind: collaboration.ReportInterim, Summary: "Short evidence summary", Body: "Complete detailed evidence, not in summary.",
	})
	if err != nil {
		t.Fatal(err)
	}
	message := collaborationMessage(collaboration.PendingDelivery{Event: report.Publish.Event})
	if !strings.Contains(message.ContentString(), report.Artifact.ID) ||
		!strings.Contains(message.ContentString(), `revision="1"`) ||
		strings.Contains(message.ContentString(), report.Artifact.Body) ||
		message.EffectiveOrigin() == provider.OriginHuman {
		t.Fatalf("notification lacks exact bounded reference: %s", message.ContentString())
	}
}

func TestCollaborationResumeReconcilesLocalCommitWithoutProviderReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"New peer input handled.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	home, project := t.TempDir(), t.TempDir()
	cfg := testConfig()
	template := cfg.Providers["openrouter"]
	template.ProviderURL = server.URL + "/v1"
	cfg.Providers["openrouter"] = template
	before, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	before.SetWorkDir(project)
	store, err := collaboration.Attach(before.db, project, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	before.SetCollaboration(store, "before-crash")
	rootID := before.ID + "_orch_1"
	root, err := before.registerCollaborator(t.Context(), rootID, before.AgentName(), before.SessionID(), "", "default", before.db)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: "resume-peer", Name: "Peer", SessionID: "peer-session", OwnerInstance: "peer-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: rootID, RuntimeEpoch: root.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: peer.ID, IdempotencyKey: "old-note", RecipientIDs: []string{rootID},
		Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Already committed peer evidence.",
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: rootID, RuntimeEpoch: root.RuntimeEpoch})
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserve = %+v, %v", reserved, err)
	}
	before.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "Retain this session.")}
	active := append(append([]provider.ChatMessage(nil), before.msgs...), collaborationMessage(reserved[0]))
	admission := sessiondb.CollaborationAdmission{
		DeliveryID: reserved[0].Delivery.ID, EventID: old.Event.ID, SessionID: before.SessionID(), AgentID: rootID,
		RuntimeEpoch: root.RuntimeEpoch, DeliveryVersion: reserved[0].Delivery.Version,
		ContextCommitID: "durable-before-crash", RunID: run.ID,
	}
	// This is the actual crash window: local context committed, workspace receipt
	// not yet published. It must never produce a second model invocation.
	if err := sessiondb.SaveContextStateWithAdmissions(before.db, before.SessionID(), before.SessionID(), rootID,
		sessiondb.ContextState{Generation: 1, Messages: active}, []sessiondb.CollaborationAdmission{admission}); err != nil {
		t.Fatal(err)
	}
	path := before.dbPath
	before.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := ResumeSessionWithDir(path, cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	after.SetWorkDir(project)
	recoveredStore, err := collaboration.Attach(after.db, project, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer recoveredStore.Close()
	after.SetCollaboration(recoveredStore, "after-crash")
	sender := newMockEventSender()
	o, err := after.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(650 * time.Millisecond) // Cover the automatic idle-delivery cadence.
	if calls.Load() != 0 {
		t.Fatal("passive resume replayed provider work")
	}
	pending, err := sessiondb.PendingCollaborationAdmissions(after.db, rootID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("receipt was not reconciled: %+v, %v", pending, err)
	}
	after.msgsMu.Lock()
	context := append([]provider.ChatMessage(nil), after.activeContext...)
	after.msgsMu.Unlock()
	if len(context) != 2 || strings.Count(context[1].ContentString(), "Already committed peer evidence.") != 1 {
		t.Fatalf("committed context was lost or duplicated: %+v", context)
	}
	if recovered, err := recoveredStore.Run(t.Context(), run.ID); err != nil || recovered.State != collaboration.RunInterrupted {
		t.Fatalf("orphan run = %+v, %v", recovered, err)
	}
	if _, err := recoveredStore.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: peer.ID, IdempotencyKey: "new-note", RecipientIDs: []string{o.ID},
		Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Genuinely new peer input.",
	}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-sender.ch:
			if event.typ == "done" {
				if calls.Load() != 1 {
					t.Fatalf("new delivery invoked provider %d times", calls.Load())
				}
				return
			}
		case <-timer.C:
			t.Fatal("new peer delivery did not wake the resumed root")
		}
	}
}

func TestAgentStartSelectsExactConfiguredProfileAndRejectsInvalidSelections(t *testing.T) {
	var parentID atomic.Value
	var childCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if request.Model != "profile-model" || childCalls.Add(1) > 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Peer evidence received.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		arguments, _ := json.Marshal(map[string]any{
			"recipient_ids": []string{parentID.Load().(string)}, "kind": "final", "summary": "Profile used", "body": "The configured profile connection and instructions were selected.",
		})
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"profile-report\",\"type\":\"function\",\"function\":{\"name\":\"publish_report\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(arguments))
	}))
	defer server.Close()
	home := t.TempDir()
	cfg := testConfig()
	template := cfg.Providers["openrouter"]
	template.ProviderURL = server.URL + "/v1"
	cfg.Providers["openrouter"] = template
	cfg.Agents.AgentProfiles["configured"] = AgentProfileConfig{Category: "crafter", Model: "profile-model", AccountID: "account", Instructions: "Use exact profile evidence."}
	off := false
	cfg.Agents.AgentProfiles["disabled"] = AgentProfileConfig{Category: "crafter", Enabled: &off}
	s, err := NewWithSessionDir("profile-contract", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(t.TempDir())
	store, err := collaboration.Attach(s.db, s.WorkDir(), filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.SetCollaboration(store, "profile-owner")
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	parentID.Store(o.ID)
	if !s.initSubAgentRunner() {
		t.Fatal("runner unavailable")
	}
	runner := s.SubAgentRunner()
	for _, selection := range []struct{ profile, category string }{{"missing", "crafter"}, {"disabled", "crafter"}, {"configured", "researcher"}} {
		if _, err := runner.StartAgent(t.Context(), map[string]any{"description": "Validate profile selection", "category": selection.category, "profile": selection.profile}); err == nil {
			t.Fatalf("invalid profile selection accepted: %+v", selection)
		}
	}
	result, err := runner.StartAgent(t.Context(), map[string]any{
		"description": "Use configured profile", "category": "crafter", "profile": "configured",
		"allowed_tools": []any{"publish_report"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &started); err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	completed, ok := runner.Manager().GetCompleted(agent.AgentID(started.AgentID))
	if !ok || completed.Status != agent.SubAgentCompleted {
		t.Fatalf("configured profile did not complete: %+v", completed)
	}
	snapshot := s.CompletedSubAgentSnapshots()
	if len(snapshot) != 1 || snapshot[0].Model != "profile-model" || completed.Name == "configured" {
		t.Fatalf("profile and allocated identity are not separate: %+v", snapshot)
	}
}

func TestCollaborationPauseResumeAndHumanAuditUseExactOwners(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator(parent.ID, s)
	o.RuntimeEpoch = parent.RuntimeEpoch
	o.setCollaborationRun(run)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	o.turnCancel = cancel
	s.orchestrators = map[string]*Orchestrator{parent.ID: o}
	published, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: "human", SenderName: "Human", IdempotencyKey: "human-pause",
		RecipientIDs: []string{parent.ID}, Origin: collaboration.OriginHuman,
		Kind: collaboration.EventLifecycleControl, RunID: run.ID, Body: "Pause this exact run.",
		References: []collaboration.Reference{{Kind: "control", ID: "pause", Revision: int(run.Version)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{
		RecipientID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, OnlyKinds: []collaboration.EventKind{collaboration.EventLifecycleControl},
	})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("pause reserve = %+v, %v", deliveries, err)
	}
	s.applyCollaborationControl(t.Context(), store, parent, deliveries[0])
	current, err := store.Agent(t.Context(), parent.ID)
	if err != nil || current.State != collaboration.AgentSuspended || ctx.Err() == nil {
		t.Fatalf("pause not applied to owner: %+v, cancelled=%t, %v", current, ctx.Err() != nil, err)
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{Kind: collaboration.EventControlOutcome})
	if err != nil || len(page.Events) != 1 || page.Events[0].ReplyTo != published.Event.ID || len(page.Events[0].Recipients) != 0 {
		t.Fatalf("human outcome did not stay audit-only: %+v, %v", page, err)
	}
	if _, err := s.applyRuntimeControl(t.Context(), store, collaborationControlTarget{
		agentID: parent.ID, runtimeEpoch: parent.RuntimeEpoch, runID: run.ID,
	}, "resume", 0, parent.RuntimeEpoch); err == nil {
		t.Fatal("resume accepted before the interrupted execution settled")
	}
	if _, err := finishCollaborationRun(store, collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch,
		ExpectedVersion: run.Version, State: collaboration.RunCancelled,
	}); err != nil {
		t.Fatalf("a concurrent control stranded finalization: %v", err)
	}
	o.endTurn(0)
	target, ok := s.controlTarget(parent.ID)
	if !ok {
		t.Fatal("root owner vanished")
	}
	if _, err := s.applyRuntimeControl(t.Context(), store, target, "resume", 0, parent.RuntimeEpoch+1); !errors.Is(err, collaboration.ErrStale) {
		t.Fatalf("wrong epoch resume = %v", err)
	}
	if _, err := s.applyRuntimeControl(t.Context(), store, target, "resume", 0, parent.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	current, err = store.Agent(t.Context(), parent.ID)
	if err != nil || current.State != collaboration.AgentAvailable || current.CurrentRunID != "" || o.TurnActive() {
		t.Fatalf("resume restarted work instead of availability: %+v, %v", current, err)
	}
}

func TestCollaborationLeavesCannotAccidentallyRedelegate(t *testing.T) {
	s, _, _, child := collaborationContractFixture(t)
	for _, definition := range s.collaborationDefinitions(child.ID) {
		if definition.Function.Name == "agent_start" {
			t.Fatal("leaf advertises a broken nested launch path")
		}
	}
	if _, _, err := s.executeCollaborationTool(t.Context(), child.ID, "agent_start",
		map[string]any{"category": "crafter", "description": "Escape the bounded assignment"}); err == nil {
		t.Fatal("leaf bypassed launch exclusion")
	}
}

func TestCollaborationWaitRequiresAllExactConditions(t *testing.T) {
	s, store, parent, child := collaborationContractFixture(t)
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, Origin: collaboration.OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.PublishReport(t.Context(), collaboration.PublishReportRequest{
		AuthorID: parent.ID, RunID: run.ID, RecipientIDs: []string{child.ID}, IdempotencyKey: "wait-evidence",
		Kind: collaboration.ReportInterim, Summary: "Interim", Body: "Interim evidence.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: parent.ID, RuntimeEpoch: parent.RuntimeEpoch, ExpectedVersion: run.Version,
		State: collaboration.RunSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	if _, err := s.waitForCollaboration(ctx, store, map[string]any{
		"agent_id": parent.ID, "run_id": run.ID, "artifact_id": report.Artifact.ID, "after_revision": 1,
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("terminal run incorrectly satisfied a still-pending report condition: %v", err)
	}
	if _, err := s.waitForCollaboration(t.Context(), store, map[string]any{"agent_id": child.ID, "run_id": run.ID}); err == nil {
		t.Fatal("wait returned another agent's run")
	}
	if _, err := s.waitForCollaboration(t.Context(), store, map[string]any{
		"agent_id": parent.ID, "run_id": run.ID, "artifact_id": report.Artifact.ID, "after_revision": 0,
	}); err != nil {
		t.Fatalf("matching terminal run and exact artifact did not satisfy wait: %v", err)
	}
}

func TestCollaborationRecoversSettledChildAdmissionWithoutRelaunch(t *testing.T) {
	home := t.TempDir()
	s, err := NewWithSessionDir("", testConfig(), home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	store, err := collaboration.Attach(s.db, home, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.SetCollaboration(store, "recovered-parent")
	parentID := s.ID + "_orch_1"
	if _, err := s.registerCollaborator(t.Context(), parentID, s.AgentName(), s.SessionID(), "", "default", s.db); err != nil {
		t.Fatal(err)
	}
	runner := NewSubAgentRunner(s, 1)
	s.subAgentSup = runner
	const childID = "settled-recovery-child"
	path, err := runner.subAgentDBPath(childID)
	if err != nil {
		t.Fatal(err)
	}
	childDB, err := sessiondb.OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.CreateSessionRow(childDB, childID); err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: childID, Name: "Child", SessionID: childID, ParentID: parentID, Category: "crafter", OwnerInstance: "old-child",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
		AgentID: childID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: parentID, IdempotencyKey: "child-peer-evidence", RecipientIDs: []string{childID},
		Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Retain this child evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: childID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("reserve = %+v, %v", deliveries, err)
	}
	if err := sessiondb.SaveContextStateWithAdmissions(childDB, childID, childID, childID,
		sessiondb.ContextState{AgentID: childID, Generation: 1, Messages: []provider.ChatMessage{collaborationMessage(deliveries[0])}},
		[]sessiondb.CollaborationAdmission{{
			DeliveryID: deliveries[0].Delivery.ID, EventID: deliveries[0].Event.ID, SessionID: childID, AgentID: childID,
			RuntimeEpoch: child.RuntimeEpoch, DeliveryVersion: deliveries[0].Delivery.Version, ContextCommitID: "child-commit", RunID: run.ID,
		}}); err != nil {
		t.Fatal(err)
	}
	if err := childDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: childID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachAgent(t.Context(), childID, "old-child", child.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	runner.Manager().HydrateCompleted(agent.SubAgentResult{
		AgentID: childID, SessionID: childID, Name: child.Name, Category: "crafter", Status: agent.SubAgentCancelled,
	})
	if err := s.recoverChildCollaborators(t.Context(), parentID); err != nil {
		t.Fatal(err)
	}
	if unsettled, err := store.HasUnsettledAdmission(t.Context(), childID); err != nil || unsettled {
		t.Fatalf("settled child's committed delivery stayed stranded: %t, %v", unsettled, err)
	}
	recovered, err := store.Agent(t.Context(), childID)
	if err != nil || recovered.Presence != collaboration.RuntimeOffline || recovered.CurrentRunID != "" || runner.Manager().ActiveCount() != 0 {
		t.Fatalf("recovery relaunched the child: %+v, %v", recovered, err)
	}
	// An evidence DB must never be silently recreated if a later recovery needs
	// receipts: absence is an error, not proof that admission never committed.
	if _, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: parentID, IdempotencyKey: "missing-child-evidence", RecipientIDs: []string{childID},
		Origin: collaboration.OriginPeer, Kind: collaboration.EventPeerNote, Body: "Uncommitted evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	attached, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: childID, Name: child.Name, SessionID: childID, ParentID: parentID, Category: "crafter", OwnerInstance: "lost-child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: childID, RuntimeEpoch: attached.RuntimeEpoch}); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachAgent(t.Context(), childID, "lost-child", attached.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverChildCollaborators(t.Context(), parentID); err == nil {
		t.Fatal("missing child evidence was treated as an empty database")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery recreated the missing evidence file: %v", err)
	}
}
