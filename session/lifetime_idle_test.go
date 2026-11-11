package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/fileio"
	"crude/provider"
	"crude/types"
)

func idleSession() *Session {
	s := &Session{orchestrators: make(map[string]*Orchestrator)}
	s.orchestrators["root"] = NewOrchestrator("root", s)
	return s
}

func TestIdleLeaseExcludesEveryAdmissionAndReleases(t *testing.T) {
	s := idleSession()
	u, err := s.BeginIdleLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareInputAdmission("root", provider.UserInput{Text: "keep"}, 1); err == nil {
		t.Fatal("input consumed through fence")
	}
	if s.orchestrators["root"].SubmitInput(provider.UserInput{Text: "autonomous"}) {
		t.Fatal("autonomous turn crossed fence")
	}
	if _, ok := s.memoryJobs.begin(); ok {
		s.memoryJobs.done()
		t.Fatal("memory work crossed fence")
	}
	if _, ok := s.beginIdleWork(); ok {
		t.Fatal("control/child work crossed fence")
	}
	if _, result := s.PrepareCompaction("root"); result.Error == "" {
		t.Fatal("compaction crossed fence")
	}
	u()
	u()
	if err := s.ValidateHistoryAdmission(); err != nil {
		t.Fatal("history reservation leaked", err)
	}
	if !s.orchestrators["root"].SubmitInput(provider.UserInput{Text: "after rollback"}) {
		t.Fatal("turn admission not restored")
	}
	s.orchestrators["root"].endTurn(1)
	if _, ok := s.memoryJobs.begin(); !ok {
		t.Fatal("memory admission not restored")
	}
	s.memoryJobs.done()
}

func TestIdleLeaseRefusesPendingQueuedMemoryAndAutonomousWorkWithoutMutation(t *testing.T) {
	for _, mode := range []string{"pending", "queued", "memory", "control", "turn", "compaction"} {
		t.Run(mode, func(t *testing.T) {
			s := idleSession()
			var finish func()
			switch mode {
			case "pending":
				s.pendingAdmissions = map[*InputAdmissionIntent]struct{}{new(InputAdmissionIntent): {}}
				finish = func() { clear(s.pendingAdmissions) }
			case "queued":
				s.queued = []QueuedInput{{Input: provider.UserInput{Text: "retained"}}}
				finish = func() { s.queued = nil }
			case "memory":
				_, _ = s.memoryJobs.begin()
				finish = s.memoryJobs.done
			case "control":
				finish, _ = s.beginIdleWork()
			case "turn":
				s.orchestrators["root"].SubmitInput(provider.UserInput{Text: "keep"})
				finish = func() { s.orchestrators["root"].endTurn(1) }
			case "compaction":
				s.turnGate.Lock()
				finish = s.turnGate.Unlock
			}
			if u, err := s.BeginIdleLease(t.Context()); err == nil {
				u()
				t.Fatal("busy owner accepted")
			} else if (mode == "control" || mode == "compaction") && !errors.Is(err, types.ErrIdleSettling) {
				t.Fatalf("finishing admission cannot settle during an explicit stop: %v", err)
			}
			if s.idle.fenced || s.memoryJobs.fenced {
				t.Fatal("refusal leaked reservations")
			}
			if err := s.ValidateHistoryAdmission(); err != nil {
				t.Fatal(err)
			}
			if mode == "queued" && (len(s.queued) != 1 || s.queued[0].Input.Text != "retained") {
				t.Fatal("queued work mutated")
			}
			finish()
			u, err := s.BeginIdleLease(t.Context())
			if err != nil {
				t.Fatal("release did not restore idleness", err)
			}
			u()
		})
	}
}

func TestIdleLeaseRacesTurnAndMemoryReservations(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := idleSession()
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var stop func()
		var err error
		var turn bool
		go func() { defer wg.Done(); <-start; stop, err = s.BeginIdleLease(context.Background()) }()
		go func() {
			defer wg.Done()
			<-start
			turn = s.orchestrators["root"].SubmitInput(provider.UserInput{Text: "never lost"})
		}()
		close(start)
		wg.Wait()
		if err == nil && turn {
			t.Fatal("stop and turn both admitted")
		}
		if stop != nil {
			stop()
		}
		if turn {
			s.orchestrators["root"].endTurn(1)
		}
		if err != nil && !strings.Contains(err.Error(), "turn") && !strings.Contains(err.Error(), "active") {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		s := idleSession()
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var stop func()
		var err error
		var memory bool
		go func() { defer wg.Done(); <-start; stop, err = s.BeginIdleLease(t.Context()) }()
		go func() { defer wg.Done(); <-start; _, memory = s.memoryJobs.begin() }()
		close(start)
		wg.Wait()
		if err == nil && memory {
			t.Fatal("stop and memory job both admitted")
		}
		if stop != nil {
			stop()
		}
		if memory {
			s.memoryJobs.done()
		}
	}
}

func TestIdleLeaseRefusesAutonomousGoalAndChildWithoutStoppingThem(t *testing.T) {
	s := idleSession()
	s.goal.present = true
	s.goal.record.Goal = types.Goal{Status: types.GoalActive, Objective: "continue accepted work"}
	if u, err := s.BeginIdleLease(t.Context()); err == nil {
		u()
		t.Fatal("autonomous goal treated as idle")
	}
	if s.goal.record.Goal.Status != types.GoalActive {
		t.Fatal("stop paused the goal")
	}
	s.goal.present = false
	runner := NewSubAgentRunner(s, 1)
	defer runner.SessionCancel()
	s.subAgentSup = runner
	runner.beginTurnChild(1)
	if u, err := s.BeginIdleLease(t.Context()); err == nil {
		u()
		t.Fatal("live child treated as idle")
	}
	if !runner.Running() {
		t.Fatal("refusal stopped the child")
	}
	runner.finishTurnChild(1)
	u, err := s.BeginIdleLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	u()
}

func TestIdleLeaseIgnoresRetainedCommandResults(t *testing.T) {
	s := idleSession()
	manager := fileio.NewAsyncCommandManager()
	defer manager.CloseAndWait()
	s.asyncMgr = manager
	settled := make(chan fileio.AsyncCmdInfo, 3)
	manager.SetSettleObserver(func(info fileio.AsyncCmdInfo) { settled <- info })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, test := range []struct {
		handle, command string
		want            fileio.AsyncStatus
	}{
		{"completed", "exit 0", fileio.AsyncCompleted},
		{"failed", "exit 7", fileio.AsyncFailed},
		{"cancelled", promotionHelperCommand("sleep"), fileio.AsyncKilled},
	} {
		started, err := manager.Start(test.handle, test.command, t.TempDir(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.want == fileio.AsyncKilled {
			if s.Activity().AsyncCommands != 1 {
				t.Fatal("running command was not counted as active work")
			}
			if release, err := s.BeginIdleLease(ctx); err == nil {
				release()
				t.Fatal("running command did not protect its session")
			}
			if err := manager.KillRef(started.Ref()); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case result := <-settled:
			if result.Handle != test.handle || result.Status != test.want {
				t.Fatalf("unexpected command outcome: %+v", result)
			}
		case <-ctx.Done():
			t.Fatal("command did not settle:", ctx.Err())
		}
		if s.Activity().Any() {
			t.Fatalf("retained %s result still counts as active work: %+v", test.handle, s.Activity())
		}
		release, err := s.BeginIdleLease(ctx)
		if err != nil {
			t.Fatalf("retained %s result blocks shutdown: %v", test.handle, err)
		}
		release()
	}
	if len(manager.List()) != 3 {
		t.Fatal("idle reservation discarded retained command results")
	}
}

func TestIdleLeaseIgnoresInactiveGoalsAndPausedLoops(t *testing.T) {
	for _, status := range []types.GoalStatus{types.GoalPaused, types.GoalBlocked, types.GoalBudgetLimited, types.GoalComplete} {
		t.Run(string(status), func(t *testing.T) {
			s := idleSession()
			s.goal.present = true
			s.goal.record.Goal = types.Goal{Status: status, Objective: "keep saved work"}
			s.loops = []types.Loop{{ID: "paused", Paused: true}}
			if goals, loops := s.AutonomousWork(); goals != 0 || loops != 0 {
				t.Fatalf("inactive state counts as autonomous work: goals=%d loops=%d", goals, loops)
			}
			release, err := s.BeginIdleLease(t.Context())
			if err != nil {
				t.Fatal("inactive saved work blocked shutdown:", err)
			}
			release()
			if s.goal.record.Goal.Status != status || len(s.loops) != 1 || !s.loops[0].Paused {
				t.Fatal("shutdown reservation changed retained goal or schedule")
			}
		})
	}
}
