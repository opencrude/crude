package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/types"
	"crude/types/control"
	recoverytypes "crude/types/recovery"
	"crude/worktree"
)

func TestExecutionHygieneWarningsAreWorkspaceWideAndNeverBlockCreation(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if err := os.WriteFile(filepath.Join(root, "baseline"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	service, err := worktree.Open(root, t.TempDir(), worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := service.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "another-parents-child", RunID: "old-run"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{}
	s.SetExecutionService(&launchExecutionFixture{Service: service})
	one, zero := 1, 0
	settings := types.ExecutionHygieneSettings{RetainedCount: &one, AgeHours: &zero, StorageMB: &zero}
	notice, digest := s.executionHygieneNotice(t.Context(), settings, "")
	if notice == nil || !strings.Contains(notice.ContentString(), c.ID) || !strings.Contains(notice.ContentString(), "never a launch blocker") {
		t.Fatal("missing workspace warning")
	}
	if same, _ := s.executionHygieneNotice(t.Context(), types.CloneExecutionHygiene(settings), digest); same != nil {
		t.Fatal("unchanged warning repeated")
	}
	off := false
	settings.Warnings = &off
	if disabled, _ := s.executionHygieneNotice(t.Context(), settings, ""); disabled != nil {
		t.Fatal("disabled reminder emitted")
	}
	if _, err := service.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "new-reader", RunID: "new"}, Mode: worktree.Shared}); err != nil {
		t.Fatal("warning blocked launch", err)
	}
}

func TestExecutionRetentionProtectsRecoverySelectionAndQueue(t *testing.T) {
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "parent"); err != nil {
		t.Fatal(err)
	}
	id := "11111111111111111111111111111111"
	if err := ExecutionDatabaseRetention(db, id); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key   string
		value any
	}{{"execution_selection", ExecutionSelection{ID: id, Revision: 1}}, {"queued_inputs", []QueuedInput{{Input: provider.UserInput{ControlTarget: &control.Target{ExecutionID: id}}}}}} {
		raw, _ := json.Marshal(tc.value)
		if err := sessiondb.SaveRuntimeState(context.Background(), db, tc.key, raw); err != nil {
			t.Fatal(err)
		}
		if err := ExecutionDatabaseRetention(db, id); err == nil {
			t.Fatal("retained reference ignored", tc.key)
		}
		if _, err := db.Exec("DELETE FROM session_runtime_state WHERE key=?", tc.key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJanitorialSettlementUsesParkedRecoveryOwnerAndExactIncident(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	s.recoveryMu.Lock()
	cp := s.pendingRecoveries[f.childID]
	var owner childRecoveryOwner
	if err := json.Unmarshal(cp.OwnerData, &owner); err != nil {
		t.Fatal(err)
	}
	owner.Spec.ExecutionID = "11111111111111111111111111111111"
	cp.OwnerData, _ = json.Marshal(owner)
	s.pendingRecoveries[f.childID] = cp
	s.recoveryMu.Unlock()
	for _, incident := range []string{"stale", cp.Status.ID} {
		id := owner.Spec.ExecutionID
		if incident == cp.Status.ID {
			id = "wrong-execution"
		}
		if err := s.CancelExecutionRecovery(id, f.childID, incident, cp.Status.Generation); err == nil {
			t.Fatal("foreign incident/execution settled parked work")
		}
	}
	if err := s.CancelExecutionRecovery(owner.Spec.ExecutionID, f.childID, cp.Status.ID, cp.Status.Generation); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("settling obsolete work started effects")
	}
	records, err := sessiondb.LoadRecoveryRecords(s.db, s.sessionID)
	if err != nil || len(records) != 1 || records[0].State != recoverytypes.Cancelled {
		t.Fatal("settlement lost cancellation history", err)
	}
}

func TestJanitorialSettlementRecoversChildOnlyLaunchCheckpoint(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	store := attachDelegationTestCollaboration(t, s)
	parent, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: s.sessionID + "_orch_1", SessionID: s.sessionID, OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: f.childID, ParentID: parent.ID, SessionID: f.childID, OwnerInstance: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	cp := f.checkpoint
	var owner childRecoveryOwner
	if err := json.Unmarshal(cp.OwnerData, &owner); err != nil {
		t.Fatal(err)
	}
	const executionID = "11111111111111111111111111111111"
	owner.Spec.ExecutionID, owner.Spec.ExecutionRunID = executionID, run.ID
	owner.Spec.ParentID = agent.AgentID(parent.ID)
	cp.OwnerData, _ = json.Marshal(owner)
	cp.Status.State = "" // Initial durable launch, before parent publication.
	cp.Archive, cp.ActiveContext = nil, nil
	cp.PendingInput = provider.UserInput{}
	cp.ContextReason = "launch_before_assignment"
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Recreate the launch window faithfully: durable empty child context, no
	// transcript or parent mirror yet. This fixture normally parks after input.
	current, found, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || !found {
		t.Fatal("child context unavailable", err)
	}
	current, err = sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: f.childID, AgentID: f.childID, ExpectedRevision: current.Revision,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: cp.ContextReason}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM messages`); err != nil {
		t.Fatal(err)
	}
	cp.ContextRevision = current.Revision
	if err := sessiondb.SaveRecoveryCheckpoint(db, f.childID, cp); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.DeleteRecoveryCheckpoint(s.db, cp.Status.ID); err != nil {
		t.Fatal(err)
	}
	s.recoveryMu.Lock()
	delete(s.pendingRecoveries, f.childID)
	s.recoveryMu.Unlock()
	if err := s.CancelExecutionRecovery(executionID, f.childID, cp.Status.ID, cp.Status.Generation); err == nil {
		t.Fatal("unmirrored checkpoint adopted while its exact run is live")
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunInterrupted}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		execution, incident string
		generation          uint64
	}{
		{"wrong-execution", cp.Status.ID, cp.Status.Generation},
		{executionID, "stale-incident", cp.Status.Generation},
		{executionID, cp.Status.ID, cp.Status.Generation + 1},
	} {
		if err := s.CancelExecutionRecovery(request.execution, f.childID, request.incident, request.generation); err == nil {
			t.Fatal("stale or unrelated recovery was adopted")
		}
	}
	var mirrored int
	if err := s.db.QueryRow(`SELECT count(*) FROM agent_recoveries WHERE agent_id=?`, f.childID).Scan(&mirrored); err != nil || mirrored != 0 {
		t.Fatal("refused adoption wrote partial parent evidence", err)
	}
	historical := cp
	historical.ContextOwnerID = ""
	historical.Status.ID, historical.Status.State = "previous-settled-incident", recoverytypes.Cancelled
	if err := sessiondb.SaveRecoveryCheckpoint(s.db, s.sessionID, historical); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelExecutionRecovery(executionID, f.childID, cp.Status.ID, cp.Status.Generation); err != nil {
		t.Fatal("child-only launch checkpoint is permanently retained", err)
	}
	for _, authority := range []struct{ dbPath, session string }{{s.dbPath, s.sessionID}, {f.childPath, f.childID}} {
		check, err := sessiondb.OpenAndMigrate(authority.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		records, err := sessiondb.LoadRecoveryRecords(check, authority.session)
		_ = check.Close()
		expected := 1
		if authority.session == s.sessionID {
			expected = 2
		}
		found := false
		for _, record := range records {
			if record.ID == cp.Status.ID && record.State == recoverytypes.Cancelled {
				found = true
			}
		}
		if err != nil || len(records) != expected || !found {
			t.Fatal("exact cancellation history not preserved", authority.session, err, records)
		}
	}
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || len(rows) != 1 || rows[0].Msg.InternalID != "turn-stopped:"+cp.Status.ID {
		t.Fatal("unstarted launch invented work or lost its exact stop boundary", err)
	}
	if f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("adoption restarted obsolete work")
	}
}

func TestChildOutcomeHygieneIsModelOnlyAndKeepsExactEvidence(t *testing.T) {
	event := collaboration.Event{ID: "outcome", Kind: collaboration.EventRunOutcome, Origin: collaboration.OriginRuntime, SenderID: "child", SenderName: "Child", Body: "Child completed", RunID: "exact-run", Execution: &collaboration.ExecutionOutcome{ID: "exact-execution", Root: "/checkout", Mode: "worktree"}}
	message := collaborationMessage(collaboration.PendingDelivery{Event: event})
	if strings.Count(message.ContentString(), "<child_execution_disposition ") != 1 || !strings.Contains(message.ContentString(), "exact-execution") || !strings.Contains(message.ContentString(), "exact-run") {
		t.Fatal("runtime disposition missing/duplicated exact facts")
	}
	if strings.Contains(message.DisplayContent, "hygiene") || strings.Contains(message.DisplayContent, "checkout") || strings.Contains(message.DisplayContent, "execution_disposition") {
		t.Fatal("model guidance leaked into visible outcome")
	}
	event.Kind = collaboration.EventReportPublished
	if got := collaborationMessage(collaboration.PendingDelivery{Event: event}); strings.Contains(got.ContentString(), "child_execution_disposition") {
		t.Fatal("nonterminal report received outcome policy")
	}
}
