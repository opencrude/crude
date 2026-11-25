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
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/worktree"
	"crude/internal/logger"
	"crude/types/control"
	"crude/types/conversation"
	recoverytypes "crude/types/recovery"
	"crude/workspace/fileio"
)

func TestPlanModeRecoveryKeepsStartingPolicyAndOriginalInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		planning bool
	}{
		{"Plan turn with Plan mode now off", true},
		{"ordinary turn with Plan mode now on", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan planModeRequest, 4)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body planModeRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				attempt := calls.Add(1)
				requests <- body
				if attempt == 1 {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			nextRequest := func() planModeRequest {
				t.Helper()
				select {
				case request := <-requests:
					return request
				case <-time.After(5 * time.Second):
					t.Fatal("provider did not receive the request")
					return planModeRequest{}
				}
			}
			assertPolicy := func(request planModeRequest) {
				t.Helper()
				if !request.offers("write_file") ||
					!request.offers("open_plan_investigation") ||
					strings.Contains(request.text(0), "<plan_mode>") != test.planning {
					t.Fatalf("provider request did not use starting Plan mode %v", test.planning)
				}
			}
			home, directory, project := t.TempDir(), t.TempDir(), t.TempDir()
			cfg := mediaAttributionConfig(server.URL, "recovery-plan-mode", "openai")
			s, err := NewWithSessionDir("", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(project)
			if err := s.SetPlanMode(test.planning); err != nil {
				t.Fatal(err)
			}
			cycle, err := s.planInvestigationCycle(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			o, err := s.NewOrchestrator(newMockEventSender())
			if err != nil {
				t.Fatal(err)
			}
			input := provider.UserInput{InternalID: "original-input", Text: "Review the exact task.",
				ContextParts:  []string{"original context"},
				ControlTarget: &control.Target{SessionID: s.sessionID, WorkspaceID: project, ResourceID: "original-resource"}}
			intent, err := s.PrepareInputAdmission(o.ID, input, 1)
			if err != nil {
				t.Fatal(err)
			}
			if result := s.CommitInputAdmission(t.Context(), intent); !result.Accepted() {
				t.Fatalf("input was not accepted: %+v", result)
			}
			assertPolicy(nextRequest())
			if err := s.SetPlanMode(!test.planning); err != nil {
				t.Fatal(err)
			}
			checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID)
			if err != nil || !open || checkpoint.PlanningCycle == nil || *checkpoint.PlanningCycle != cycle ||
				!reflect.DeepEqual(checkpoint.PendingInput, input) {
				t.Fatalf("checkpoint did not save the starting policy separately from input: %+v %v", checkpoint, err)
			}
			// Take the durable image of an interrupted live turn, not a
			// graceful Stop (which would settle the unfinished incident).
			crashed := filepath.Join(t.TempDir(), "interrupted.db")
			if _, err := s.db.Exec(`VACUUM INTO ?`, crashed); err != nil {
				t.Fatal(err)
			}
			s.Close()
			resumed, err := ResumeSessionWithDir(crashed, cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(resumed.Close)
			resumed.SetWorkDir(project)
			if resumed.PlanMode() == test.planning || calls.Load() != 1 {
				t.Fatal("passive resume changed the next-turn setting or contacted the provider")
			}
			sender := newMockEventSender()
			owner, err := resumed.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			if err := resumed.RetryRecoveryFor(owner.ID, checkpoint.Status.ID, checkpoint.Status.Generation); err != nil {
				t.Fatal(err)
			}
			assertPolicy(nextRequest())
			waitTurnDone(t, sender, owner.ID)
			if inputs := genuineUserInputs(resumed); !slices.Equal(inputs, []string{input.Text}) {
				t.Fatalf("continuation replaced or duplicated the original prompt: %q", inputs)
			}
		})
	}
}

func startInvestigationTestRun(t *testing.T, s *Session, store *collaboration.Store, agentID string) (worktree.Owner, func(collaboration.RunState)) {
	t.Helper()
	root, err := s.registerCollaborator(t.Context(), agentID, "Root", s.sessionID, "", "default", s.db)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	return worktree.Owner{AgentID: root.ID, RunID: run.ID}, func(state collaboration.RunState) {
		if _, err := store.FinishRun(context.Background(), collaboration.FinishRunRequest{
			RunID: run.ID, AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, ExpectedVersion: run.Version, State: state,
		}); err != nil {
			t.Fatal(err)
		}
		s.reconcileRootExecutionPins()
	}
}

func TestPlanInvestigationRetainsExactSourceAndNeverRetargets(t *testing.T) {
	s, adapter, source := executionAdmissionFixture(t)
	store := attachDelegationTestCollaboration(t, s)
	t.Cleanup(s.Close)
	root := s.WorkDir()
	const baseline = "dirty bytes in selected checkout\n"
	if err := os.WriteFile(filepath.Join(source.Root, "baseline.txt"), []byte(baseline), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectExecution(t.Context(), source.ID, 0); err != nil {
		t.Fatal(err)
	}
	cycle, err := s.planInvestigationCycle(t.Context())
	if err != nil || cycle == "" {
		t.Fatalf("cycle %q: %v", cycle, err)
	}
	ctx := withPlanInvestigationCycle(t.Context(), cycle)
	agentID := s.sessionID + "_orch_1"
	owner, finish := startInvestigationTestRun(t, s, store, agentID)
	if _, err := s.activePlanInvestigation(ctx, source.ID, owner); !errors.Is(err, errPlanInvestigationNotOpen) || len(adapter.List()) != 1 {
		t.Fatalf("active lookup must not allocate: %v", err)
	}
	c, err := s.openPlanInvestigation(ctx, source.ID, owner)
	if err != nil || !c.Ready || c.Investigation == nil || c.Investigation.SessionID != s.sessionID || c.SourceContextID != source.ID || c.Root == source.Root {
		t.Fatalf("private checkout: %+v: %v", c, err)
	}
	assertBytes := func(dir, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "baseline.txt"))
		if err != nil || string(data) != want {
			t.Fatalf("bytes in %s = %q, expected %q: %v", dir, data, want, err)
		}
	}
	assertBytes(c.Root, baseline)
	const investigationEdit = "new test in investigation\n"
	if err := os.WriteFile(filepath.Join(c.Root, "baseline.txt"), []byte(investigationEdit), 0600); err != nil {
		t.Fatal(err)
	}
	assertBytes(source.Root, baseline)
	assertBytes(root, "preserved baseline\n")
	if _, err := s.SelectExecution(ctx, c.ID, 1); err == nil || s.ExecutionSelection().ID != source.ID {
		t.Fatal("investigation replaced selected execution")
	}
	input := provider.UserInput{ControlTarget: &control.Target{ExecutionID: c.ID}}
	if unlock, err := s.lockInputExecution(input); err == nil {
		unlock()
		t.Fatal("investigation accepted explicit ordinary input")
	}
	if _, err := s.pinRootExecution(ctx, c.ID, owner); err == nil {
		t.Fatal("ordinary execution pin bypassed investigation ownership")
	}
	if err := s.setInvestigationConsent(c.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	finish(collaboration.RunSucceeded)
	if _, err := s.activePlanInvestigation(ctx, source.ID, owner); !errors.Is(err, collaboration.ErrStale) {
		t.Fatalf("terminal owner retained writable lease: %v", err)
	}
	if err := ExecutionDatabaseRetention(s.db, c.ID); err == nil || !strings.Contains(err.Error(), "Plan mode") {
		t.Fatalf("between-turn retention lost: %v", err)
	}
	dir, home := filepath.Dir(s.dbPath), t.TempDir()
	forkID := logger.NewSessionID()
	if err := s.Fork(dir, forkID); err != nil {
		t.Fatal(err)
	}
	fork, err := ResumeSessionWithDir(filepath.Join(dir, forkID+".db"), testConfig(), home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	if _, err := fork.investigationConsent(c.ID); !fork.PlanMode() || !errors.Is(err, errPlanInvestigationNotOpen) {
		t.Fatalf("fork must keep only the Plan mode preference, not consent: %v", err)
	}
	if err := ExecutionDatabaseRetention(fork.db, c.ID); err != nil {
		t.Fatalf("fork retains source-owned writable checkout: %v", err)
	}
	path := s.dbPath
	// This fixture attached the durable collaborator without starting an
	// orchestrator. Release the registration the orchestrator normally owns.
	s.detachCollaborator(t.Context(), agentID)
	s.Close()
	resumed, err := ResumeSessionWithDir(path, testConfig(), home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.SetWorkDir(root)
	resumed.SetExecutionService(adapter)
	resumed.SetCollaboration(store, collaboration.NewInstanceID())
	now, err := resumed.planInvestigationCycle(t.Context())
	if err != nil || now != cycle {
		t.Fatalf("reopen lost cycle: %q: %v", now, err)
	}
	agentID = resumed.sessionID + "_orch_2"
	owner, finish = startInvestigationTestRun(t, resumed, store, agentID)
	reopened, err := resumed.activePlanInvestigation(ctx, source.ID, owner)
	if err != nil || reopened.ID != c.ID {
		t.Fatalf("reopen did not bind original investigation: %+v: %v", reopened, err)
	}
	if consent, err := resumed.investigationConsent(c.ID); err != nil || consent != "allow" {
		t.Fatalf("reopen lost consent: %q: %v", consent, err)
	}
	assertBytes(reopened.Root, investigationEdit)
	captured, release, err := resumed.capturePlanInvestigationTurn(t.Context(), nil, source.ID, "")
	if err != nil || captured != cycle {
		t.Fatalf("live turn did not capture its cycle: %q: %v", captured, err)
	}
	defer release()
	if err := resumed.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	if err := resumed.ExecutionRetention(c.ID, c.Root); err == nil || !strings.Contains(err.Error(), "running Plan mode turn") {
		t.Fatalf("toggle released turn before its recovery checkpoint: %v", err)
	}
	if old, err := resumed.openPlanInvestigation(ctx, source.ID, owner); err != nil || old.ID != c.ID {
		t.Fatalf("toggle moved the live turn: %+v: %v", old, err)
	}
	if err := resumed.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	fresh, err := resumed.planInvestigationCycle(t.Context())
	if err != nil || fresh == cycle || fresh == "" {
		t.Fatalf("re-enabled mode reused cycle: %q: %v", fresh, err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:        recoverytypes.Status{AgentID: agentID, ID: "parked-plan", Generation: 1, State: recoverytypes.NeedsAttention},
		PlanningCycle: &cycle,
		PendingInput:  provider.UserInput{ControlTarget: &control.Target{ExecutionID: source.ID}},
	}
	if err := sessiondb.SaveRecoveryCheckpoint(resumed.db, resumed.sessionID, checkpoint); err != nil {
		t.Fatal(err)
	}
	release()
	finish(collaboration.RunInterrupted)
	if err := ExecutionDatabaseRetention(resumed.db, c.ID); err == nil || !strings.Contains(err.Error(), "parked-plan") {
		t.Fatalf("closed cycle lost parked recovery retention: %v", err)
	}
	checkpoint.Status.State = recoverytypes.Cancelled
	if err := sessiondb.SaveRecoveryCheckpoint(resumed.db, resumed.sessionID, checkpoint); err != nil {
		t.Fatal(err)
	}
	owner, finish = startInvestigationTestRun(t, resumed, store, agentID)
	nextCtx := withPlanInvestigationCycle(t.Context(), fresh)
	if _, err := resumed.activePlanInvestigation(nextCtx, source.ID, owner); !errors.Is(err, errPlanInvestigationNotOpen) {
		t.Fatalf("fresh cycle inherited old checkout: %v", err)
	}
	next, err := resumed.openPlanInvestigation(nextCtx, source.ID, owner)
	if err != nil || next.ID == c.ID {
		t.Fatalf("fresh cycle did not create a distinct investigation: %+v %v", next, err)
	}
	if consent, err := resumed.investigationConsent(next.ID); err != nil || consent != "" {
		t.Fatalf("fresh cycle inherited old consent: %q %v", consent, err)
	}
	finish(collaboration.RunSucceeded)
	discard, err := adapter.ReviewDiscard(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{
		Discard: true, Digest: discard.DiscardDigest,
		Check: func(context.Context) error { return resumed.ExecutionRetention(c.ID, c.Root) },
	}); err != nil {
		t.Fatal(err)
	}
	archived, err := adapter.Inspect(c.ID)
	if err != nil || !archived.Removed || string(archived.ArchivedResult["baseline.txt"].Data) != investigationEdit {
		t.Fatalf("reviewed discard lost exact experiment: %+v: %v", archived, err)
	}
	assertBytes(source.Root, baseline)
	assertBytes(root, "preserved baseline\n")
}

// Pause the durable pin write, independently of baseline size and disk speed.
// All command receipts must already be final when the plan becomes approvable.
type pausedInvestigationUnpin struct {
	*executionAdmissionService
	id      string
	entered chan<- struct{}
	resume  <-chan struct{}
}

func (s *pausedInvestigationUnpin) Unpin(id string, owner worktree.Owner) error {
	if id == s.id {
		s.entered <- struct{}{}
		<-s.resume
	}
	return s.executionAdmissionService.Unpin(id, owner)
}

func TestPlanInvestigationApprovalWhileTerminalPinWriteIsPending(t *testing.T) {
	const plan = "# Repair the verified regression"
	s, sender := reviewFixture(t, "# Earlier revision")
	sourceSession, service, _ := executionAdmissionFixture(t)
	s.SetWorkDir(sourceSession.WorkDir())
	s.SetExecutionService(service)
	store := attachDelegationTestCollaboration(t, s)
	cycle, err := s.planInvestigationCycle(t.Context())
	if err != nil || cycle == "" {
		t.Fatalf("planning cycle: %q: %v", cycle, err)
	}
	ctx := withPlanInvestigationCycle(t.Context(), cycle)
	owner, _ := startInvestigationTestRun(t, s, store, s.sessionID+"_previous_root")
	c, err := s.openPlanInvestigation(ctx, "", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setInvestigationConsent(c.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	const handle = "approval-verification"
	if _, err := s.executePlanInvestigationTool(ctx, "", s.WorkDir(), owner, 2, "async_execute_command", map[string]any{
		"command": "echo verified", "handle": handle,
	}); err != nil {
		t.Fatal(err)
	}
	info, ok := s.asyncMgr.Check(handle)
	if !ok {
		t.Fatal("verification command is missing")
	}
	if result := waitInvestigationToolCommand(t, s, info.Ref()); result.Status != fileio.AsyncCompleted || result.ExitCode != 0 {
		t.Fatalf("verification command did not settle: %+v", result)
	}
	if err := s.recordProposedPlan(2, planReply(plan), cycle, ""); err != nil {
		t.Fatal(err)
	}
	revision := latestReview(t, s)
	if !revision.Open() || len(revision.Evidence) != 1 {
		t.Fatalf("pending proposal did not include settled evidence: %+v", revision)
	}

	run, err := store.Run(ctx, owner.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(ctx, collaboration.FinishRunRequest{
		RunID: run.ID, AgentID: owner.AgentID, RuntimeEpoch: run.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunSucceeded,
	}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	resume, release := executionAdmissionRelease(t)
	s.SetExecutionService(&pausedInvestigationUnpin{service, c.ID, entered, resume})
	defer s.SetExecutionService(service)
	reconciled := make(chan struct{})
	go func() {
		defer close(reconciled)
		s.reconcileRootExecutionPins()
	}()
	defer func() {
		release()
		awaitExecutionAdmission(t, reconciled)
	}()
	awaitExecutionAdmission(t, entered)
	out, err := s.SettlePlanReview(conversation.PlanReviewSettle{
		Revision: revision.Number, Text: plan, Action: conversation.PlanReviewImplement,
	})
	if err != nil || s.PlanMode() || latestReview(t, s).Outcome != conversation.PlanOutcomeImplemented {
		t.Fatalf("pending terminal pin write blocked approval: %v", err)
	}
	if !strings.Contains(out.Text, revision.Evidence[0].Digest) || strings.Contains(out.Text, c.Root) {
		t.Fatalf("approval lost exact evidence or exposed the checkout path: %q", out.Text)
	}
	release()
	awaitExecutionAdmission(t, reconciled)
	waitTurnDone(t, sender, s.liveOrchestrator().ID)
	if stored, err := service.InspectSummary(c.ID); err != nil || slices.Contains(stored.Pins, owner) || stored.Removed {
		t.Fatalf("terminal pin did not settle, or approval removed the checkout: %+v: %v", stored, err)
	}
}

type pausedInvestigationLaunch struct {
	*executionAdmissionService
	checkoutCalls int
	prepared      chan<- struct{}
	resume        <-chan struct{}
}

func (s *pausedInvestigationLaunch) PinInvestigation(ctx context.Context, id string, binding worktree.Investigation, source string, owner worktree.Owner) (worktree.Context, error) {
	c, err := s.executionAdmissionService.PinInvestigation(ctx, id, binding, source, owner)
	s.checkoutCalls++
	// The first lookup routes the command. The second is its actual launch
	// preparation under the operation lease, before the manager can list it.
	if err == nil && s.checkoutCalls == 2 {
		s.prepared <- struct{}{}
		<-s.resume
	}
	return c, err
}

func TestPlanInvestigationRetainsPrelaunchCommandWhenRunFinishes(t *testing.T) {
	s, service, source := executionAdmissionFixture(t)
	store := attachDelegationTestCollaboration(t, s)
	defer s.Close()
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	cycle, err := s.planInvestigationCycle(t.Context())
	if err != nil || cycle == "" {
		t.Fatalf("planning cycle: %q: %v", cycle, err)
	}
	ctx := withPlanInvestigationCycle(t.Context(), cycle)
	owner, finish := startInvestigationTestRun(t, s, store, s.sessionID+"_orch_1")
	c, err := s.openPlanInvestigation(ctx, source.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setInvestigationConsent(c.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	// Close the cycle for future turns. This turn already captured it.
	if err := s.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewDiscard(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared := make(chan struct{}, 1)
	resume, release := executionAdmissionRelease(t)
	s.SetExecutionService(&pausedInvestigationLaunch{executionAdmissionService: service, prepared: prepared, resume: resume})
	defer s.SetExecutionService(service)
	launched := make(chan error, 1)
	done := make(chan struct{})
	const handle = "prelaunch-verification"
	go func() {
		defer close(done)
		_, err := s.executePlanInvestigationTool(ctx, source.ID, source.Root, owner, 1, "async_execute_command", map[string]any{
			"command": "echo verified", "handle": handle,
		})
		launched <- err
	}()
	defer func() {
		release()
		awaitExecutionAdmission(t, done)
	}()
	awaitExecutionAdmission(t, prepared)
	if _, registered := s.asyncMgr.Check(handle); registered {
		t.Fatal("fixture did not stop before command registration")
	}
	finish(collaboration.RunSucceeded)
	if stored, err := service.InspectSummary(c.ID); err != nil || !slices.Contains(stored.Pins, owner) {
		t.Fatalf("terminal run released its in-flight command's pin: %+v: %v", stored, err)
	}
	opts := worktree.CleanupOptions{Discard: true, Digest: review.DiscardDigest,
		Check: func(context.Context) error { return s.ExecutionRetention(c.ID, c.Root) }}
	if err := service.Cleanup(ctx, c.ID, c.Owner, opts); err == nil || !strings.Contains(err.Error(), "evidence settlement is still running") {
		t.Fatalf("cleanup did not recognize the in-flight command: %v", err)
	}

	release()
	if err := awaitExecutionAdmission(t, launched); err != nil {
		t.Fatal(err)
	}
	info, ok := s.asyncMgr.Check(handle)
	if !ok {
		t.Fatal("verification command is missing")
	}
	if result := waitInvestigationToolCommand(t, s, info.Ref()); result.Status != fileio.AsyncCompleted || result.ExitCode != 0 {
		t.Fatalf("verification command did not settle: %+v", result)
	}
	s.reconcileRootExecutionPins()
	if err := service.Cleanup(ctx, c.ID, c.Owner, opts); err != nil {
		t.Fatalf("settled command retained the closed checkout: %v", err)
	}
}
