package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/fileio"
	"crude/provider"
	"crude/sessiondb"
	"crude/types/control"
	"crude/worktree"
)

// Inspect exposes the real read-before-admission boundary without replacing the
// artifact, persistence, selection, input-admission or cleanup implementations.
type executionAdmissionService struct {
	*worktree.Service
	inspect func(worktree.Context)
}

func (s *executionAdmissionService) Inspect(id string) (worktree.Context, error) {
	c, err := s.Service.Inspect(id)
	if err == nil && s.inspect != nil {
		s.inspect(c)
	}
	return c, err
}

func (*executionAdmissionService) Apply(context.Context, worktree.PlanDelta) (worktree.Receipt, error) {
	return worktree.Receipt{}, errors.New("admission fixture does not own integration")
}

func executionAdmissionFixture(t *testing.T) (*Session, *executionAdmissionService, worktree.Context) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "baseline.txt"), []byte("preserved baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "baseline"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	service, err := worktree.Open(root, t.TempDir(), worktree.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := service.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "settled-owner", RunID: "settled-run"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Unpin(c.ID, c.Owner); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithSessionDir("execution-admission", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(root)
	adapter := &executionAdmissionService{Service: service}
	s.SetExecutionService(adapter)
	return s, adapter, c
}

func awaitExecutionAdmission[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("execution admission/cleanup did not reach its channel boundary")
		var zero T
		return zero
	}
}

func executionAdmissionRelease(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return release, unblock
}

func assertExecutionAdmissionLeaseHeld(t *testing.T) {
	t.Helper()
	if unlock, available := fileio.TryLockMutation(); available {
		unlock()
		t.Fatal("admission/cleanup boundary did not hold the shared mutation lease")
	}
}

func assertExecutionAdmissionArtifact(t *testing.T, service *worktree.Service, c worktree.Context, removed bool) {
	t.Helper()
	got, err := service.Inspect(c.ID)
	if err != nil || got.Removed != removed {
		t.Fatalf("artifact disposition=%+v err=%v; want removed=%t", got, err, removed)
	}
	if removed {
		if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed checkout is still present: %v", err)
		}
		return
	}
	text, err := os.ReadFile(filepath.Join(c.Root, "baseline.txt"))
	if err != nil || string(text) != "preserved baseline\n" {
		t.Fatalf("retained checkout lost its actual content: %q %v", text, err)
	}
}

func TestExecutionSelectionAdmissionVsCleanup(t *testing.T) {
	t.Run("selection_wins", func(t *testing.T) {
		s, adapter, c := executionAdmissionFixture(t)
		inspected := make(chan struct{})
		release, unblock := executionAdmissionRelease(t)
		adapter.inspect = func(worktree.Context) {
			close(inspected)
			<-release
		}
		selected := make(chan error, 1)
		go func() { _, err := s.SelectExecution(t.Context(), c.ID, 0); selected <- err }()
		awaitExecutionAdmission(t, inspected)
		assertExecutionAdmissionLeaseHeld(t)
		cleanupStarted, cleaned := make(chan struct{}), make(chan error, 1)
		go func() {
			close(cleanupStarted)
			cleaned <- adapter.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
				return s.ExecutionRetention(c.ID, c.Root)
			}})
		}()
		awaitExecutionAdmission(t, cleanupStarted)
		unblock()
		if err := awaitExecutionAdmission(t, selected); err != nil {
			t.Fatal(err)
		}
		if err := awaitExecutionAdmission(t, cleaned); err == nil || !strings.Contains(err.Error(), "selects execution") {
			t.Fatalf("cleanup ignored admitted selection: %v", err)
		}
		raw, err := sessiondb.LoadRuntimeState(t.Context(), s.db, executionSelectionKey)
		var saved ExecutionSelection
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.ID != c.ID || saved.Revision != 1 || s.ExecutionSelection() != saved {
			t.Fatalf("selection was not durably acknowledged: %s %v", raw, err)
		}
		assertExecutionAdmissionArtifact(t, adapter.Service, c, false)
	})

	t.Run("cleanup_wins", func(t *testing.T) {
		s, adapter, c := executionAdmissionFixture(t)
		checked := make(chan struct{})
		release, unblock := executionAdmissionRelease(t)
		cleaned := make(chan error, 1)
		go func() {
			cleaned <- adapter.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
				if err := s.ExecutionRetention(c.ID, c.Root); err != nil {
					return err
				}
				close(checked)
				<-release
				return nil
			}})
		}()
		awaitExecutionAdmission(t, checked)
		assertExecutionAdmissionLeaseHeld(t)
		started, selected := make(chan struct{}), make(chan error, 1)
		go func() {
			close(started)
			_, err := s.SelectExecution(t.Context(), c.ID, 0)
			selected <- err
		}()
		awaitExecutionAdmission(t, started)
		unblock()
		if err := awaitExecutionAdmission(t, cleaned); err != nil {
			t.Fatal(err)
		}
		if err := awaitExecutionAdmission(t, selected); err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("selection acknowledged a removed checkout: %v", err)
		}
		if got := s.ExecutionSelection(); got != (ExecutionSelection{}) {
			t.Fatalf("rejected selection changed live state: %+v", got)
		}
		raw, err := sessiondb.LoadRuntimeState(t.Context(), s.db, executionSelectionKey)
		if err != nil || len(raw) != 0 {
			t.Fatalf("rejected selection was persisted: %s %v", raw, err)
		}
		assertExecutionAdmissionArtifact(t, adapter.Service, c, true)
	})
}

func executionAdmissionOwner(t *testing.T, s *Session, queued bool) *Orchestrator {
	t.Helper()
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		if err := sessiondb.SaveContextState(s.db, s.sessionID, s.sessionID, sessiondb.ContextState{AgentID: s.sessionID, Generation: 1, Reason: "fixture", Messages: []provider.ChatMessage{provider.NewTextMessage("user", "prior context")}}); err != nil {
			t.Fatal(err)
		}
		// This is the real unstarted compaction owner. No fabricated compacting
		// flags or provider calls are needed to exercise durable queue admission.
		prepared, early := s.PrepareCompaction(o.ID)
		if prepared.Run == nil || early.Error != "" {
			t.Fatalf("prepare queue owner: %+v", early)
		}
	}
	return o
}

func prepareExecutionAdmission(t *testing.T, s *Session, o *Orchestrator, id string, queued bool) *InputAdmissionIntent {
	t.Helper()
	input := provider.PlainInput("preserve this exact checkout input")
	input.ControlTarget = &control.Target{ExecutionID: id}
	intent, err := s.PrepareInputAdmission(o.ID, input, 7)
	if err != nil {
		t.Fatal(err)
	}
	if intent.queued != queued {
		t.Fatalf("wrong input entry point: queued=%t want=%t", intent.queued, queued)
	}
	if !queued {
		// Stop cancels dispatch, not a durable local send. The production commit
		// still records recovery, and no network/model activity can mask retention.
		if stopped, _ := o.CancelTurn(); !stopped {
			t.Fatal("could not stop reserved provider dispatch")
		}
	}
	return intent
}

func assertExecutionAdmissionInput(t *testing.T, s *Session, o *Orchestrator, intent *InputAdmissionIntent, id string, queued bool) {
	t.Helper()
	if queued {
		raw, err := sessiondb.LoadRuntimeState(t.Context(), s.db, queuedInputsStateKey)
		var saved []QueuedInput
		if err != nil || json.Unmarshal(raw, &saved) != nil || len(saved) != 1 || saved[0].Input.InternalID != intent.InputID ||
			saved[0].Input.ControlTarget == nil || saved[0].Input.ControlTarget.ExecutionID != id || saved[0].Input.Text != "preserve this exact checkout input" {
			t.Fatalf("admitted queue lost input or execution identity: %s %v", raw, err)
		}
		return
	}
	checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID)
	if err != nil || !found || !checkpoint.InputAdmitted || checkpoint.PendingInput.InternalID != intent.InputID ||
		checkpoint.PendingInput.ControlTarget == nil || checkpoint.PendingInput.ControlTarget.ExecutionID != id || checkpoint.PendingInput.Text != "preserve this exact checkout input" {
		t.Fatalf("accepted input lost durable recovery: %+v found=%t err=%v", checkpoint, found, err)
	}
	state, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found || len(state.Messages) == 0 || state.Messages[0].InternalID != intent.InputID {
		t.Fatalf("accepted input missing from rendered-history source: %+v found=%t err=%v", state, found, err)
	}
}

func TestExecutionInputAdmissionVsCleanup(t *testing.T) {
	for _, mode := range []struct {
		name   string
		queued bool
	}{{"immediate", false}, {"queued", true}} {
		t.Run(mode.name+"/input_wins", func(t *testing.T) {
			s, adapter, c := executionAdmissionFixture(t)
			o := executionAdmissionOwner(t, s, mode.queued)
			intent := prepareExecutionAdmission(t, s, o, c.ID, mode.queued)
			inspected := make(chan struct{})
			release, unblock := executionAdmissionRelease(t)
			adapter.inspect = func(worktree.Context) { close(inspected); <-release }
			admitted := make(chan InputAdmissionResult, 1)
			go func() { admitted <- s.CommitInputAdmission(t.Context(), intent) }()
			awaitExecutionAdmission(t, inspected)
			assertExecutionAdmissionLeaseHeld(t)
			started, cleaned := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				cleaned <- adapter.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
					return s.ExecutionRetention(c.ID, c.Root)
				}})
			}()
			awaitExecutionAdmission(t, started)
			unblock()
			result := awaitExecutionAdmission(t, admitted)
			want := InputAdmissionPreservationPending
			if mode.queued {
				want = InputAdmissionQueued
			}
			if result.Status != want || result.Receipt.InputID != intent.InputID || result.Receipt.DraftRevision != 7 {
				t.Fatalf("input was not durably admitted: %+v", result)
			}
			if err := awaitExecutionAdmission(t, cleaned); err == nil {
				t.Fatal("cleanup removed checkout retained by an acknowledged input")
			}
			assertExecutionAdmissionInput(t, s, o, intent, c.ID, mode.queued)
			if got := s.ExecutionSelection(); got.ID != "" {
				t.Fatalf("selection accidentally masked explicit input retention: %+v", got)
			}
			assertExecutionAdmissionArtifact(t, adapter.Service, c, false)
		})

		t.Run(mode.name+"/cleanup_wins", func(t *testing.T) {
			s, adapter, c := executionAdmissionFixture(t)
			o := executionAdmissionOwner(t, s, mode.queued)
			checked := make(chan struct{})
			release, unblock := executionAdmissionRelease(t)
			cleaned := make(chan error, 1)
			go func() {
				cleaned <- adapter.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
					if err := s.ExecutionRetention(c.ID, c.Root); err != nil {
						return err
					}
					close(checked)
					<-release
					return nil
				}})
			}()
			awaitExecutionAdmission(t, checked)
			assertExecutionAdmissionLeaseHeld(t)
			// Prepare is deliberately permitted after the final retained-use check.
			// Commit must wait for removal and refuse before durable acknowledgement.
			intent := prepareExecutionAdmission(t, s, o, c.ID, mode.queued)
			started, admitted := make(chan struct{}), make(chan InputAdmissionResult, 1)
			go func() { close(started); admitted <- s.CommitInputAdmission(t.Context(), intent) }()
			awaitExecutionAdmission(t, started)
			unblock()
			if err := awaitExecutionAdmission(t, cleaned); err != nil {
				t.Fatal(err)
			}
			result := awaitExecutionAdmission(t, admitted)
			if result.Status != InputAdmissionRejected || result.ErrorKind != InputAdmissionPreLocalRejection || result.Err == nil || result.Receipt.InputID != "" {
				t.Fatalf("input acknowledged a removed checkout: %+v", result)
			}
			if o.TurnActive() || len(s.QueuedInputs()) != 0 {
				t.Fatal("rejected input left provider dispatch or a runnable queue")
			}
			if _, found, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, o.ID); err != nil || found {
				t.Fatalf("rejected input created recovery: found=%t err=%v", found, err)
			}
			state, _, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range state.Messages {
				if message.InternalID == intent.InputID {
					t.Fatal("rejected checkout input leaked into accepted history")
				}
			}
			assertExecutionAdmissionArtifact(t, adapter.Service, c, true)
		})
	}
}
