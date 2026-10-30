package sessiondb

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"crude/types"
	"crude/types/handover"
)

func handoverDatabase(t *testing.T) (*DB, GoalRecord, GoalHandoverRecord) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "source"); err != nil {
		t.Fatal(err)
	}
	goal := GoalRecord{Goal: types.Goal{ID: "goal", Objective: "Keep all accepted work", Status: types.GoalActive, CreatedAt: time.Now()}}
	if err := SaveGoal(db, "source", goal); err != nil {
		t.Fatal(err)
	}
	record := GoalHandoverRecord{Reservation: handover.Reservation{OperationID: "operation", GoalID: "goal", TokenSHA256: "private-digest"},
		State: handover.State{OperationID: "operation", GoalID: "goal", Phase: handover.Reserved, LifecycleEpoch: 1}, GoalControl: GoalControl(goal)}
	if err := CommitGoalHandover(db, "source", 0, GoalControl(goal), record, nil); err != nil {
		t.Fatal(err)
	}
	return &DB{db: db, path: path}, goal, record
}

func TestGoalHandoverDatabaseWaitAndConsumeAreAtomic(t *testing.T) {
	d, goal, record := handoverDatabase(t)
	ready := record
	ready.State.Phase, ready.State.LifecycleEpoch = handover.Ready, 2
	goal.Goal.Waiting = true
	ready.GoalControl = GoalControl(goal)
	if _, err := d.db.Exec(`CREATE TRIGGER refuse_goal_wait BEFORE UPDATE ON session_goal BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := CommitGoalHandover(d.db, "source", 1, record.GoalControl, ready, &goal); err == nil {
		t.Fatal("failed goal write committed readiness")
	}
	stored, _, err := LoadGoalHandover(d.db, "source")
	if err != nil || stored.State.Phase != handover.Reserved || stored.State.LifecycleEpoch != 1 {
		t.Fatalf("partial ready: %+v %v", stored, err)
	}
	if _, err := d.db.Exec(`DROP TRIGGER refuse_goal_wait`); err != nil {
		t.Fatal(err)
	}
	if err := CommitGoalHandover(d.db, "source", 1, record.GoalControl, ready, &goal); err != nil {
		t.Fatal(err)
	}
	next := ready
	next.State.Phase, next.State.LifecycleEpoch = handover.Consumed, 3
	goal.Goal.Waiting = false
	if _, err := d.db.Exec(`CREATE TRIGGER refuse_goal_consume BEFORE UPDATE ON session_goal BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := CommitGoalHandover(d.db, "source", 2, ready.GoalControl, next, &goal); err == nil {
		t.Fatal("failed goal write consumed permit")
	}
	stored, _, err = LoadGoalHandover(d.db, "source")
	if err != nil || stored.State.Phase != handover.Ready {
		t.Fatalf("partial consume: %+v %v", stored, err)
	}
	actual, _, err := LoadGoal(d.db, "source")
	if err != nil || !actual.Goal.Waiting {
		t.Fatalf("failed consume released wait: %+v %v", actual, err)
	}
}

func TestGoalHandoverDatabaseConcurrentConsumeAndControlFence(t *testing.T) {
	d, goal, record := handoverDatabase(t)
	ready := record
	ready.State.Phase, ready.State.LifecycleEpoch = handover.Ready, 2
	goal.Goal.Waiting = true
	ready.GoalControl = GoalControl(goal)
	if err := CommitGoalHandover(d.db, "source", 1, record.GoalControl, ready, &goal); err != nil {
		t.Fatal(err)
	}
	// Accounting is not a control change.
	goal.Goal.TokensUsed = 9
	if err := SaveGoal(d.db, "source", goal); err != nil {
		t.Fatal(err)
	}
	next := ready
	next.State.Phase, next.State.LifecycleEpoch = handover.Consumed, 3
	goal.Goal.Waiting = false
	start := make(chan struct{})
	results := make(chan error, 16)
	other, err := Open(d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		target := d.db
		if i%2 == 1 {
			target = other
		}
		go func() {
			defer wg.Done()
			<-start
			results <- CommitGoalHandover(target, "source", 2, ready.GoalControl, next, &goal)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrHandoverConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("consume winners = %d, want exactly one", winners)
	}
	actual, _, err := LoadGoal(d.db, "source")
	if err != nil || actual.Goal.Waiting || actual.Goal.TokensUsed != 9 {
		t.Fatalf("lost atomic accounting: %+v %v", actual, err)
	}

	// A caller with a current epoch still cannot overwrite a newer user pause.
	actual.Goal.Status = types.GoalPaused
	if err := SaveGoal(d.db, "source", actual); err != nil {
		t.Fatal(err)
	}
	next.State.LifecycleEpoch = 4
	if err := CommitGoalHandover(d.db, "source", 3, ready.GoalControl, next, &goal); !errors.Is(err, ErrHandoverConflict) {
		t.Fatalf("changed goal accepted: %v", err)
	}
}

func TestGoalHandoverDatabaseReopenAndForkDropPermit(t *testing.T) {
	d, _, record := handoverDatabase(t)
	if err := d.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAndMigrate(d.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, ok, err := LoadGoalHandover(reopened, "source")
	if err != nil || !ok || actual.State != record.State {
		t.Fatalf("reopen lost exact permit: %+v %v", actual, err)
	}
	fork := filepath.Join(t.TempDir(), "fork.db")
	if err := Fork(reopened, fork, ForkIdentity{SessionID: "fork", AgentName: "Fork", CreatedAt: time.Now().UnixNano()}); err != nil {
		t.Fatal(err)
	}
	copy, err := OpenAndMigrate(fork)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if _, ok, err := LoadGoalHandover(copy, "fork"); err != nil || ok {
		t.Fatalf("fork copied live permit: %v %v", ok, err)
	}
	goal, ok, err := LoadGoal(copy, "fork")
	if err != nil || !ok || !goal.Goal.Waiting || goal.Goal.Objective != "Keep all accepted work" {
		t.Fatalf("fork lost passive goal: %+v %v", goal, err)
	}
}
