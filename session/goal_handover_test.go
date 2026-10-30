package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/types"
	"crude/types/control"
	"crude/types/handover"
	recoverytypes "crude/types/recovery"
)

const handoverTestToken = "opaque-test-nonce-with-at-least-128-bits"

type handoverFixture struct {
	s         *Session
	o         *Orchestrator
	store     *collaboration.Store
	run       collaboration.Run
	r         handover.Reservation
	home, dir string
	cfg       *Config
}

func newHandoverFixture(t *testing.T) handoverFixture {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	cfg := testConfig()
	s, err := NewWithSessionDir("handover", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	store := attachDelegationTestCollaboration(t, s)
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	g, err := s.SetGoal("Preserve the objective and continue exactly once", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator(s.sessionID, s)
	reg, err := s.registerCollaborator(t.Context(), o.ID, "Root", s.sessionID, "", "default", s.db)
	if err != nil {
		t.Fatal(err)
	}
	o.RuntimeEpoch = reg.RuntimeEpoch
	s.orchestrators[o.ID] = o
	request, ok := o.reserveTurn(provider.PlainInput("start"), nil, nil)
	if !ok {
		t.Fatal("fixture root did not reserve")
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: o.ID, RuntimeEpoch: o.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	o.setCollaborationRun(run)
	target := control.Target{WorkspaceID: "workspace", SessionID: s.sessionID, Shell: &control.ShellOwner{ClientID: "original-shell", Epoch: 7}}
	s.goalHandoverSource = goalHandoverSource{agentID: o.ID, runID: run.ID, inputID: "original-input", epoch: o.RuntimeEpoch, generation: request.generation, target: target}
	sum := sha256.Sum256([]byte(handoverTestToken))
	r := handover.Reservation{OperationID: "handover-operation", GoalID: g.ID, TokenSHA256: hex.EncodeToString(sum[:]),
		SuccessorCohort: "successor", SuccessorSchema: "schema", ExpiresAt: time.Now().Add(10 * time.Minute),
		SourceAgentID: o.ID, SourceRunID: run.ID, SourceRuntimeEpoch: o.RuntimeEpoch, SourceCoreOwner: "old-core", SourceShell: *target.Shell}
	return handoverFixture{s: s, o: o, store: store, run: run, r: r, home: home, dir: dir, cfg: cfg}
}

func (f handoverFixture) reserve(t *testing.T) handover.State {
	t.Helper()
	state, err := f.s.ReserveGoalHandover(f.r)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != handover.Reserved || state.SourceTurnGeneration == 0 {
		t.Fatalf("bad reservation: %+v", state)
	}
	return state
}

func (f handoverFixture) ready(t *testing.T) handover.State {
	t.Helper()
	f.reserve(t)
	settled, err := f.store.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: f.run.ID, AgentID: f.o.ID, RuntimeEpoch: f.o.RuntimeEpoch, ExpectedVersion: f.run.Version, State: collaboration.RunSucceeded, Outcome: "completed", ContextCommitID: "original-input"})
	if err != nil {
		t.Fatal(err)
	}
	f.o.endTurn(f.s.goalHandoverSource.generation)
	message := provider.NewTextMessage("user", "The original accepted input")
	message.InternalID = f.s.goalHandoverSource.inputID
	f.s.msgsMu.Lock()
	published, err := f.s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{
		Append: []sessiondb.ContextMessageDelta{contextDelta(message, sessiondb.DisplayMeta{DisplayRole: "user"}, 1, time.Now(), f.o.ID)},
	}})
	f.s.msgsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.s.readyGoalHandover(f.s.goalHandoverSource, settled, published.Revision, true)
	state, err := f.s.GoalHandover(f.r.OperationID)
	if err != nil || state.Phase != handover.Ready {
		t.Fatalf("not ready: %+v %v", state, err)
	}
	return state
}

func (f handoverFixture) target() control.Target {
	return control.Target{WorkspaceID: "workspace", SessionID: f.s.sessionID, Shell: &control.ShellOwner{ClientID: "original-shell", Epoch: 19}}
}

func TestGoalHandoverOwnerProvenanceAndSecretProjection(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*handoverFixture)
	}{
		{"foreign-agent", func(f *handoverFixture) { f.r.SourceAgentID = "other" }},
		{"foreign-run", func(f *handoverFixture) { f.r.SourceRunID = "other" }},
		{"wrong-epoch", func(f *handoverFixture) { f.r.SourceRuntimeEpoch++ }},
		{"wrong-generation", func(f *handoverFixture) { f.r.SourceTurnGeneration = 999 }},
		{"replacement-shell", func(f *handoverFixture) { f.r.SourceShell.ClientID = "focused-replacement" }},
		{"unbound-source", func(f *handoverFixture) { f.s.goalHandoverSource.target = control.Target{} }},
		{"stopped-source", func(f *handoverFixture) { f.o.CancelTurn() }},
		{"queued-input", func(f *handoverFixture) { f.s.queued = []QueuedInput{{Input: provider.PlainInput("human")}} }},
		{"parked-recovery", func(f *handoverFixture) {
			f.s.pendingRecoveries = map[string]sessiondb.RecoveryCheckpoint{"parked": {}}
		}},
		{"plan", func(f *handoverFixture) { f.s.planMode.Store(true) }},
		{"budget", func(f *handoverFixture) { f.s.goal.record.Goal.TokenBudget = 1; f.s.goal.record.Goal.TokensUsed = 1 }},
		{"strike-limit", func(f *handoverFixture) { f.s.goal.emptyTurns = goalStrikeLimit }},
		{"loop", func(f *handoverFixture) { f.s.loops = []types.Loop{{ID: "scheduled", Paused: false}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newHandoverFixture(t)
			test.change(&f)
			if _, err := f.s.ReserveGoalHandover(f.r); err == nil {
				t.Fatal("invalid source or work state was accepted")
			}
		})
	}
	f := newHandoverFixture(t)
	state := f.reserve(t)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), handoverTestToken) || strings.Contains(string(raw), f.r.TokenSHA256) || strings.Contains(string(raw), "token_sha256") {
		t.Fatalf("observable state leaked secret material: %s", raw)
	}
	if _, ok := f.s.goalContinuation(); ok {
		t.Fatal("reservation started another automatic turn")
	}
	if !f.o.TurnActive() {
		t.Fatal("reservation cancelled its live source")
	}
	if goals, _ := f.s.AutonomousWork(); goals != 1 {
		t.Fatal("reserved source lost active-goal protection")
	}
}

func TestGoalHandoverOwnerReadyConsumeDestinationAndConcurrentRepeat(t *testing.T) {
	f := newHandoverFixture(t)
	ready := f.ready(t)
	if goal, _ := f.s.Goal(); !goal.Waiting {
		t.Fatal("ready did not own a waiting goal")
	}
	if goals, _ := f.s.AutonomousWork(); goals != 0 {
		t.Fatal("exact Ready still blocked global idle lease")
	}
	lease, err := f.s.BeginIdleLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target()); err == nil {
		t.Fatal("continue crossed final idle exclusion")
	}
	lease()
	for _, test := range []struct {
		name   string
		change func(*control.Target)
	}{
		{"session", func(t *control.Target) { t.SessionID = "foreign" }},
		{"workspace", func(t *control.Target) { t.WorkspaceID = "foreign" }},
		{"execution", func(t *control.Target) { t.ExecutionID = "foreign" }},
		{"resource", func(t *control.Target) { t.ResourceID = "foreign" }},
		{"shell-client", func(t *control.Target) { t.Shell.ClientID = "replacement" }},
		{"unbound", func(t *control.Target) { t.Shell = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := f.target()
			test.change(&target)
			if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", target); err == nil {
				t.Fatal("replacement destination was accepted")
			}
		})
	}
	if _, err := f.s.ContinueGoalHandover(f.r.OperationID, "wrong-token-with-enough-length", "successor", "schema", f.target()); err == nil {
		t.Fatal("wrong nonce accepted")
	}
	if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "wrong-build", "schema", f.target()); err == nil {
		t.Fatal("wrong successor accepted")
	}
	var wg sync.WaitGroup
	results := make(chan handover.State, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target())
			if err != nil {
				t.Error(err)
			}
			results <- state
		}()
	}
	wg.Wait()
	close(results)
	for state := range results {
		if state.Phase != handover.Consumed || state.LifecycleEpoch != ready.LifecycleEpoch+1 {
			t.Fatalf("repeat mutated consumption: %+v", state)
		}
	}
	if f.s.AgentTurnGeneration(f.o.ID) != 1 || f.o.TurnActive() {
		t.Fatal("consumption directly launched or resumed a run")
	}
	input, ok := f.s.goalContinuation()
	if !ok || input.ControlTarget == nil || input.ControlTarget.Shell == nil || *input.ControlTarget.Shell != *f.target().Shell {
		t.Fatalf("standard continuation lost current authorized shell: %+v", input)
	}
	if !strings.Contains(input.Text, "Preserve the objective and continue exactly once") || input.Origin != provider.OriginRuntimeContext {
		t.Fatalf("handover invented a human input: %+v", input)
	}
}

func TestGoalHandoverOwnerInvalidationExpiryAndCancelPreserveUserControls(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, handoverFixture)
	}{
		{"pause", func(t *testing.T, f handoverFixture) {
			_, err := f.s.PauseGoal("user pause")
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"edit", func(t *testing.T, f handoverFixture) {
			_, err := f.s.EditGoal("new objective")
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"plan", func(t *testing.T, f handoverFixture) {
			if err := f.s.SetPlanMode(true); err != nil {
				t.Fatal(err)
			}
		}},
		{"settings", func(t *testing.T, f handoverFixture) {
			if _, err := f.s.ToggleMultiAgent(); err != nil {
				t.Fatal(err)
			}
		}},
		{"yolo", func(t *testing.T, f handoverFixture) { f.s.SetYolo(true) }},
		{"capability", func(t *testing.T, f handoverFixture) {
			if _, err := f.s.SetCapabilityEnabled("test-capability", false); err != nil {
				t.Fatal(err)
			}
		}},
		{"loop", func(t *testing.T, f handoverFixture) {
			loop, err := f.s.CreateLoop(time.Minute, "User-owned scheduled task")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.ChangeLoop("pause", loop.ID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newHandoverFixture(t)
			f.ready(t)
			test.mutate(t, f)
			state, err := f.s.GoalHandover(f.r.OperationID)
			if err != nil || state.Phase != handover.Invalidated {
				t.Fatalf("mutation left permit live: %+v %v", state, err)
			}
			if err := f.s.CancelGoalHandover(f.r.OperationID, handoverTestToken); err == nil {
				t.Fatal("cancel overrode changed user controls")
			}
			if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target()); err == nil {
				t.Fatal("changed controls continued")
			}
			if test.name == "pause" {
				if g, _ := f.s.Goal(); g.Status != types.GoalPaused || g.Reason != "user pause" {
					t.Fatalf("pause lost: %+v", g)
				}
			}
		})
	}
	t.Run("expiry", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.ready(t)
		f.s.goalMu.Lock()
		next := f.s.goalHandover
		next.State.ExpiresAt = time.Now().Add(-time.Second)
		err := f.s.commitHandoverLocked(next, "", nil)
		f.s.goalMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if goals, _ := f.s.AutonomousWork(); goals != 1 {
			t.Fatal("expired permit silently abandoned active goal")
		}
		if lease, err := f.s.BeginIdleLease(t.Context()); err == nil {
			lease()
			t.Fatal("expired permit admitted idle handover")
		}
		state, err := f.s.GoalHandover(f.r.OperationID)
		if err != nil || state.Phase != handover.Expired {
			t.Fatalf("expiry = %+v %v", state, err)
		}
		if goal, _ := f.s.Goal(); !goal.Waiting {
			t.Fatal("expiry silently resumed goal")
		}
	})
	t.Run("cancel-owned-wait", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.ready(t)
		f.s.goal.emptyTurns = 2
		if err := f.s.CancelGoalHandover(f.r.OperationID, handoverTestToken); err != nil {
			t.Fatal(err)
		}
		if g, _ := f.s.Goal(); g.Waiting {
			t.Fatal("cancel did not release its own unchanged wait")
		}
		if f.s.goal.emptyTurns != 2 {
			t.Fatal("cancel reset strike rules")
		}
	})
	t.Run("cancel-newer-durable-control", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.ready(t)
		later, _, err := sessiondb.LoadGoal(f.s.db, f.s.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		later.Goal.Status, later.Goal.Reason = types.GoalPaused, "newer durable user pause"
		if err := sessiondb.SaveGoal(f.s.db, f.s.sessionID, later); err != nil {
			t.Fatal(err)
		}
		if err := f.s.CancelGoalHandover(f.r.OperationID, handoverTestToken); err == nil {
			t.Fatal("cancel overwrote a newer durable control preimage")
		}
		actual, _, err := sessiondb.LoadGoal(f.s.db, f.s.sessionID)
		if err != nil || actual.Goal.Status != types.GoalPaused || actual.Goal.Reason != "newer durable user pause" {
			t.Fatalf("newer durable pause lost: %+v %v", actual, err)
		}
	})
}

func TestGoalHandoverOwnerQueuedSteeringExecutionAndIdleControls(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.reserve(t)
		f.s.compactionMu.Lock()
		f.s.compactionCancel, f.s.compactionAgent, f.s.compactionID = func() {}, f.o.ID, "test-owned-compaction"
		f.s.compactionStarted = true
		f.s.compactionMu.Unlock()
		intent, err := f.s.PrepareInputAdmission(f.o.ID, provider.PlainInput("queued human correction"), 1)
		f.s.compactionMu.Lock()
		f.s.compactionCancel, f.s.compactionAgent, f.s.compactionID, f.s.compactionStarted = nil, "", "", false
		f.s.compactionMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		result := f.s.CommitInputAdmission(t.Context(), intent)
		if result.Status != InputAdmissionQueued {
			t.Fatalf("queued human not accepted: %+v", result)
		}
		state, err := f.s.GoalHandover(f.r.OperationID)
		if err != nil || state.Phase != handover.Invalidated {
			t.Fatalf("queued correction left permit: %+v %v", state, err)
		}
	})
	t.Run("steering", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.reserve(t)
		_, err := f.s.PublishHumanSteering(t.Context(), f.o.ID, collaboration.PublishRequest{SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
			Kind: collaboration.EventHumanSteering, IdempotencyKey: "handover-human-steering", RecipientIDs: []string{f.o.ID}, RunID: f.run.ID, Body: "Steer this exact run."})
		if err != nil {
			t.Fatal(err)
		}
		state, err := f.s.GoalHandover(f.r.OperationID)
		if err != nil || state.Phase != handover.Invalidated {
			t.Fatalf("steering left permit: %+v %v", state, err)
		}
	})
	t.Run("execution", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.s.executionSelection = "original-checkout"
		f.s.goalHandoverSource.target.ExecutionID = "original-checkout"
		f.ready(t)
		if _, err := f.s.SelectExecution(t.Context(), "", 0); err != nil {
			t.Fatal(err)
		}
		state, err := f.s.GoalHandover(f.r.OperationID)
		if err != nil || state.Phase != handover.Invalidated {
			t.Fatalf("checkout selection left permit: %+v %v", state, err)
		}
	})
	t.Run("lifetime-fence", func(t *testing.T) {
		f := newHandoverFixture(t)
		f.ready(t)
		lease, err := f.s.BeginIdleLease(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer lease()
		if err := f.s.CancelGoalHandover(f.r.OperationID, handoverTestToken); err == nil {
			t.Fatal("cancel crossed accepted lifetime exclusion")
		}
		if _, err := f.s.PauseGoal("late pause"); err == nil {
			t.Fatal("goal control crossed accepted lifetime exclusion")
		}
		if err := f.s.SetPlanMode(true); err == nil {
			t.Fatal("plan control crossed accepted lifetime exclusion")
		}
		if _, err := f.s.ToggleMultiAgent(); err == nil {
			t.Fatal("settings crossed accepted lifetime exclusion")
		}
		f.s.SetYolo(true)
		if f.s.YoloEnabled() {
			t.Fatal("policy crossed accepted lifetime exclusion")
		}
	})
}

func TestGoalHandoverOwnerMemoryJoinRetainsExactBoundary(t *testing.T) {
	f := newHandoverFixture(t)
	f.reserve(t)
	source := f.s.goalHandoverSource
	f.o.endTurn(source.generation)
	f.s.contextRevision = 17
	settled := collaboration.Run{ID: f.run.ID, AgentID: f.o.ID, RuntimeEpoch: f.o.RuntimeEpoch, State: collaboration.RunSucceeded, ContextCommitID: source.inputID}
	f.s.memoryJobs.maintaining.Store(true)
	f.s.readyGoalHandover(source, settled, 17, true)
	state, err := f.s.GoalHandover(f.r.OperationID)
	if err != nil || state.Phase != handover.Reserved || f.s.goalHandoverBoundary == nil {
		t.Fatalf("maintenance destroyed exact terminal evidence: %+v %v", state, err)
	}
	if goals, _ := f.s.AutonomousWork(); goals != 1 {
		t.Fatal("pending join discarded active goal protection")
	}
	if _, ok := f.s.goalContinuation(); ok {
		t.Fatal("pending maintenance boundary started an unauthorized continuation")
	}
	f.s.memoryJobs.maintaining.Store(false)
	f.s.reconsiderGoalHandover()
	state, err = f.s.GoalHandover(f.r.OperationID)
	if err != nil || state.Phase != handover.Ready {
		t.Fatalf("joined maintenance did not release exact boundary: %+v %v", state, err)
	}
}

func TestGoalHandoverOwnerUnsuccessfulBoundaryCannotBecomeReady(t *testing.T) {
	for _, mode := range []string{"failed", "stopped", "parked", "timeout", "missing-context", "foreign-receipt", "queued", "recovery", "plan", "budget", "strike"} {
		t.Run(mode, func(t *testing.T) {
			f := newHandoverFixture(t)
			f.reserve(t)
			source := f.s.goalHandoverSource
			f.o.endTurn(source.generation)
			settled := collaboration.Run{ID: f.run.ID, AgentID: f.o.ID, RuntimeEpoch: f.o.RuntimeEpoch, State: collaboration.RunSucceeded, ContextCommitID: source.inputID}
			revision, natural := uint64(17), true
			switch mode {
			case "failed":
				settled.State = collaboration.RunFailed
			case "stopped", "parked":
				natural = false
			case "missing-context":
				revision = 0
			case "foreign-receipt":
				settled.ContextCommitID = "another-input"
			case "queued":
				f.s.queued = []QueuedInput{{Input: provider.PlainInput("new human")}}
			case "recovery":
				f.s.recoverySlots = map[string]*recoverySlot{"root": {status: recoverytypes.Status{ID: "recovery", State: recoverytypes.NeedsAttention}}}
			case "timeout":
				f.s.recoverySlots = map[string]*recoverySlot{"root": {status: recoverytypes.Status{ID: "timeout-recovery",
					State: recoverytypes.Recovering, Attempt: 1, RetryAt: time.Now().Add(time.Minute)}}}
			case "plan":
				f.s.planMode.Store(true)
			case "budget":
				f.s.goal.record.Goal.TokenBudget = 1
				f.s.goal.record.Goal.TokensUsed = 1
			case "strike":
				f.s.goal.failingTurns = goalStrikeLimit
			}
			f.s.readyGoalHandover(source, settled, revision, natural)
			state, err := f.s.GoalHandover(f.r.OperationID)
			if err != nil || state.Phase == handover.Ready {
				t.Fatalf("unsafe readiness: %+v %v", state, err)
			}
		})
	}
}

func TestGoalHandoverOwnerColdReopenIsPassiveAfterConsumedCrash(t *testing.T) {
	f := newHandoverFixture(t)
	f.ready(t)
	if _, err := f.s.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target()); err != nil {
		t.Fatal(err)
	}
	path := f.s.dbPath
	f.s.Close()
	resumed, err := ResumeSessionWithDir(path, f.cfg, f.home, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	if goal, _ := resumed.Goal(); !goal.Waiting || goal.Status != types.GoalActive {
		t.Fatalf("cold reopen restarted goal: %+v", goal)
	}
	if _, ok := resumed.goalContinuation(); ok {
		t.Fatal("consumed-before-admission crash replayed automatically")
	}
	target := f.target()
	state, err := resumed.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", target)
	if err != nil || state.Phase != handover.Consumed || !strings.Contains(state.Reason, "manual recovery") {
		t.Fatalf("lost partial/manual result: %+v %v", state, err)
	}
	if resumed.goalHandoverTarget != nil {
		t.Fatal("repeat reconstructed one-use target")
	}
}

func TestGoalHandoverOwnerReadyColdReopenRequiresExplicitPermit(t *testing.T) {
	f := newHandoverFixture(t)
	ready := f.ready(t)
	path := f.s.dbPath
	f.s.Close()
	resumed, err := ResumeSessionWithDir(path, f.cfg, f.home, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	if _, ok := resumed.goalContinuation(); ok {
		t.Fatal("ready cold reopen ran before authorization")
	}
	state, err := resumed.GoalHandover(f.r.OperationID)
	if err != nil || state.Phase != handover.Ready || state.LifecycleEpoch != ready.LifecycleEpoch {
		t.Fatalf("cold reopen changed exact ready permit: %+v %v", state, err)
	}
	if _, err := resumed.ContinueGoalHandover(f.r.OperationID, handoverTestToken, "successor", "schema", f.target()); err != nil {
		t.Fatal(err)
	}
	input, ok := resumed.goalContinuation()
	if !ok || input.ControlTarget == nil || *input.ControlTarget.Shell != *f.target().Shell {
		t.Fatalf("explicit permit failed after cold reopen: %+v", input)
	}
}

func TestGoalHandoverNaturalTerminalRealTurnAndNoDuplicateTurn(t *testing.T) {
	calls := make(chan goalProviderCall, 8)
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
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "stop"}}})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	cfg := modelAttributionConfig(server.URL, "handover-model")
	s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	attachDelegationTestCollaboration(t, s)
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	s.SetHistoryHooks(func(context.Context, string, string) error { return nil }, nil)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	target := control.Target{WorkspaceID: "workspace", SessionID: s.sessionID, Shell: &control.ShellOwner{ClientID: "original", Epoch: 3}}
	if !o.SubmitInput(provider.UserInput{Text: "Begin the requested work", ControlTarget: &target}) {
		t.Fatal("source not submitted")
	}
	nextCall := func() goalProviderCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-time.After(8 * time.Second):
			t.Fatal("provider request missing")
			return goalProviderCall{}
		}
	}
	first := nextCall()
	goal, err := s.SetGoal("Continue the accepted work", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	s.goalMu.Lock()
	source := s.goalHandoverSource
	s.goalMu.Unlock()
	sum := sha256.Sum256([]byte(handoverTestToken))
	r := handover.Reservation{OperationID: "real-operation", GoalID: goal.ID, TokenSHA256: hex.EncodeToString(sum[:]), SuccessorCohort: "next", SuccessorSchema: "schema",
		ExpiresAt: time.Now().Add(time.Minute), SourceAgentID: o.ID, SourceRunID: source.runID, SourceRuntimeEpoch: o.RuntimeEpoch, SourceCoreOwner: "old", SourceShell: *target.Shell}
	if _, err := s.ReserveGoalHandover(r); err != nil {
		t.Fatal(err)
	}
	// Setting the goal after the turn started is intentional: the handover
	// owner still needs the root's natural durable boundary, not goalTurnBegin.
	first.reply <- map[string]any{"content": "source completed naturally"}
	deadline := time.After(8 * time.Second)
	for {
		select {
		case event := <-sender.ch:
			if event.typ == "goal" {
				if g, ok := event.data.(types.Goal); ok && g.Waiting {
					goto ready
				}
			}
		case <-deadline:
			t.Fatal("natural terminal boundary did not publish waiting goal")
		}
	}
ready:
	state, err := s.GoalHandover(r.OperationID)
	if err != nil || state.Phase != handover.Ready || state.ReadyContextRevision == 0 {
		t.Fatalf("natural readiness absent: %+v %v", state, err)
	}
	select {
	case <-calls:
		t.Fatal("reserved source admitted another goal turn")
	case <-time.After(100 * time.Millisecond):
	}
	target.Shell.Epoch = 11
	if _, err := s.ContinueGoalHandover(r.OperationID, handoverTestToken, "next", "schema", target); err != nil {
		t.Fatal(err)
	}
	continuation := nextCall()
	if text := goalRequestText(continuation.request); !strings.Contains(text, "<goal_continuation>") || !strings.Contains(text, "source completed naturally") {
		t.Fatalf("continuation lost committed source or standard goal input: %s", text)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.ContinueGoalHandover(r.OperationID, handoverTestToken, "next", "schema", target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PauseGoal("test final pause"); err != nil {
		t.Fatal(err)
	}
	continuation.reply <- map[string]any{"content": "continued once"}
	waitTurnDone(t, sender, o.ID)
	select {
	case <-calls:
		t.Fatal("repeated consumed operation created fresh turn")
	case <-time.After(150 * time.Millisecond):
	}
}
