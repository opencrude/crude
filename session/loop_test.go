package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

func TestScheduledLoopRunsInSessionAndCoalescesBusyOccurrences(t *testing.T) {
	requests := make(chan provider.ChatRequest, 8)
	replies := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		select {
		case <-replies:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"checked\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	cfg := modelAttributionConfig(server.URL, "loop-model")
	s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(_ context.Context, _, _ string) error { return nil }, nil)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := s.CreateLoop(time.Minute, "Check <deploy> and report.")
	if err != nil {
		t.Fatal(err)
	}
	nextRequest := func() provider.ChatRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(8 * time.Second):
			t.Fatal("scheduled iteration did not reach the provider")
			return provider.ChatRequest{}
		}
	}
	first := nextRequest()
	if text := goalRequestText(first); !strings.Contains(text, "<scheduled_loop>") ||
		!strings.Contains(text, "Check &lt;deploy&gt; and report.") {
		t.Fatalf("scheduled task was not attributed and escaped: %s", text)
	}
	var scheduled, running bool
	for _, row := range s.StatusRows() {
		scheduled = scheduled || strings.Contains(row.Content, "scheduled every") && strings.Contains(row.Content, loop.Prompt)
		running = running || strings.Contains(row.Content, "Loop "+loop.ID+" running.") && !strings.Contains(row.Content, loop.Prompt)
	}
	if !scheduled || !running {
		t.Fatal("the transcript did not identify the task once and its iteration without duplicating the prompt")
	}
	// Several missed times while the first turn is busy become one later
	// iteration, never concurrent requests or a replay of every missed time.
	s.loopMu.Lock()
	next := append([]types.Loop(nil), s.loops...)
	next[0].NextAt = time.Now().Add(-5 * time.Minute)
	err = s.saveLoopsLocked(next)
	s.loopMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
		t.Fatal("a second iteration ran while the agent was busy")
	case <-time.After(700 * time.Millisecond):
	}
	replies <- struct{}{}
	waitTurnDone(t, sender, o.ID)
	_ = nextRequest()
	if err := s.ChangeLoop("pause", loop.ID); err != nil {
		t.Fatal(err)
	}
	replies <- struct{}{}
	waitTurnDone(t, sender, o.ID)
	select {
	case <-requests:
		t.Fatal("paused loop ran again")
	case <-time.After(800 * time.Millisecond):
	}
	if got := s.Loops(); len(got) != 1 || !got[0].Paused || got[0].ID != loop.ID {
		t.Fatalf("loop state after pause = %+v", got)
	}
}

func TestScheduledLoopWaitsForPlanGoalAndPassiveReopen(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	cfg := testConfig()
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := s.CreateLoop(time.Minute, "check the release")
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator("root", s)
	o.RuntimeEpoch = 1
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if s.startLoopTurn(o, time.Now()) {
		t.Fatal("loop started in Plan mode")
	}
	if err := s.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	s.goalMu.Lock()
	s.goal.present = true
	s.goal.record.Goal.Status = types.GoalActive
	s.goalMu.Unlock()
	if s.startLoopTurn(o, time.Now()) {
		t.Fatal("loop competed with an active goal")
	}
	s.goalMu.Lock()
	s.goal.present = false
	s.goalMu.Unlock()
	if release, err := s.BeginIdleLease(t.Context()); err == nil {
		release()
		t.Fatal("idle retirement accepted an active loop")
	}
	if err := s.Fork(dir, "loop-fork"); err != nil {
		t.Fatal(err)
	}
	fork, err := ResumeSessionWithDir(filepath.Join(dir, "loop-fork.db"), cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fork.Loops(); len(got) != 0 {
		t.Fatalf("fork copied autonomous schedules: %+v", got)
	}
	fork.Close()
	path := s.dbPath
	s.Close()
	reopened, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	got := reopened.Loops()
	if len(got) != 1 || got[0].ID != loop.ID || !got[0].Paused || !strings.Contains(got[0].Reason, "reopened") {
		t.Fatalf("cold reopen did not preserve a passive loop: %+v", got)
	}
	o = NewOrchestrator("root", reopened)
	o.RuntimeEpoch = 1
	if reopened.startLoopTurn(o, time.Now().Add(2*time.Minute)) {
		t.Fatal("cold reopen ran a missed iteration")
	}
	if err := reopened.ChangeLoop("resume", loop.ID); err != nil {
		t.Fatal(err)
	}
	if next := reopened.Loops()[0]; next.Paused || !next.NextAt.After(time.Now()) {
		t.Fatalf("resume replayed the old deadline: %+v", next)
	}
	if err := reopened.ChangeLoop("clear", loop.ID); err != nil || len(reopened.Loops()) != 0 {
		t.Fatalf("clear left the schedule: %v %+v", err, reopened.Loops())
	}
}

func TestLoopStopAndExpiryRemainVisibleAndDoNotRestart(t *testing.T) {
	s, err := NewWithSessionDir("", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	loop, err := s.CreateLoop(time.Minute, "check status")
	if err != nil {
		t.Fatal(err)
	}
	s.loopTurnEnd(provider.UserInput{InternalID: loopInputPrefix + loop.ID + ":1"}, true, false, false, "")
	if got := s.Loops()[0]; !got.Paused || !strings.Contains(got.Reason, "stopped") {
		t.Fatalf("Stop failed to pause the loop: %+v", got)
	}
	if err := s.ChangeLoop("resume", loop.ID); err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator("root", s)
	o.RuntimeEpoch = 1
	if s.startLoopTurn(o, loop.ExpiresAt.Add(time.Second)) {
		t.Fatal("expired loop started a turn")
	}
	if got := s.Loops()[0]; !got.Paused || !strings.Contains(got.Reason, "Expired") {
		t.Fatalf("expiry was not recorded: %+v", got)
	}
}

func TestLoopStopCancelsTheRealTurnAndPausesSchedule(t *testing.T) {
	requests := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	home := t.TempDir()
	s, err := NewWithSessionDir("", modelAttributionConfig(server.URL, "loop-model"), home, filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	s.SetHistoryHooks(func(_ context.Context, _, _ string) error { return nil }, nil)
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 512)
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := s.CreateLoop(time.Minute, "Check the deployment.")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
	case <-time.After(8 * time.Second):
		t.Fatal("scheduled turn never reached the provider")
	}
	if cancelled, _ := s.CancelOrchestrator(o.ID); !cancelled {
		t.Fatal("Stop did not cancel the running scheduled turn")
	}
	waitTurnDone(t, sender, o.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.Loops(); len(got) == 1 && got[0].Paused && strings.Contains(got[0].Reason, "stopped") {
			if err := s.ChangeLoop("resume", loop.ID); err != nil {
				t.Fatal(err)
			}
			if !s.Loops()[0].NextAt.After(time.Now()) {
				t.Fatal("resume replayed the stopped iteration immediately")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stopped turn did not pause its schedule: %+v", s.Loops())
}

func TestLoopWaitsForOpenRecoveryWithoutAdvancing(t *testing.T) {
	s, err := NewWithSessionDir("", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	loop, err := s.CreateLoop(time.Minute, "Check status.")
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator("root", s)
	o.RuntimeEpoch = 1
	s.recoveryMu.Lock()
	s.pendingRecoveries[o.ID] = sessiondb.RecoveryCheckpoint{}
	s.recoveryMu.Unlock()
	if s.startLoopTurn(o, time.Now().Add(time.Minute)) {
		t.Fatal("loop started while recovery owned the agent")
	}
	if got := s.Loops()[0]; got.NextAt != loop.NextAt || got.Paused {
		t.Fatalf("waiting for recovery changed the schedule: %+v", got)
	}
	s.recoveryMu.Lock()
	delete(s.pendingRecoveries, o.ID)
	s.recoveryMu.Unlock()
}

func TestLoopRunsOldestDueScheduleFirst(t *testing.T) {
	s, err := NewWithSessionDir("", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for _, task := range []string{"first", "second", "third"} {
		if _, err := s.CreateLoop(time.Minute, task); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	s.loopMu.Lock()
	next := append([]types.Loop(nil), s.loops...)
	next[0].NextAt = now.Add(-time.Second)
	next[1].NextAt = now.Add(-2 * time.Second)
	next[2].NextAt = now.Add(-3 * time.Second)
	err = s.saveLoopsLocked(next)
	s.loopMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrchestrator("root", s)
	o.RuntimeEpoch = 1
	for _, want := range []string{"third", "second", "first"} {
		if !s.startLoopTurn(o, now) {
			t.Fatalf("due schedule %q was not admitted", want)
		}
		request := <-o.inputCh
		if !strings.Contains(request.input.Text, "<task>"+want+"</task>") {
			t.Fatalf("oldest due schedule was not selected: want %q, got %q", want, request.input.Text)
		}
		o.endTurn(request.generation)
	}
}

func TestLoopStopFailsClosedWhenStorageFails(t *testing.T) {
	for _, action := range []string{"iteration-stop", "pause", "clear", "expiry"} {
		t.Run(action, func(t *testing.T) {
			s, err := NewWithSessionDir("", testConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			loop, err := s.CreateLoop(time.Minute, "check status")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.db.Close(); err != nil {
				t.Fatal(err)
			}
			if action == "iteration-stop" {
				s.loopTurnEnd(provider.UserInput{InternalID: loopInputPrefix + loop.ID + ":1"}, true, false, false, "")
			} else if action == "expiry" {
				o := NewOrchestrator("root", s)
				o.RuntimeEpoch = 1
				if s.startLoopTurn(o, loop.ExpiresAt.Add(time.Second)) {
					t.Fatal("an expired loop started a turn")
				}
			} else if err := s.ChangeLoop(action, loop.ID); err == nil {
				t.Fatal("storage failure was not reported")
			}
			got := s.Loops()
			if len(got) != 1 || !got[0].Paused || !strings.Contains(got[0].Reason, "could not be saved") {
				t.Fatalf("storage failure allowed autonomous work: %+v", got)
			}
			o := NewOrchestrator("root", s)
			o.RuntimeEpoch = 1
			if s.startLoopTurn(o, time.Now().Add(2*time.Minute)) {
				t.Fatal("failed stop started another turn")
			}
		})
	}
}
