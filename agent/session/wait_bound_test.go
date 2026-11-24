package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/tools"
	"crude/types/setting"
	"crude/workspace/fileio"
)

func TestWaitBoundFollowsSettingsAndAuto(t *testing.T) {
	s := &Session{cfg: &Config{}}
	if got, bounded := s.boundWait(time.Hour); !bounded || got != setting.DefaultMaxWaitMinutes*time.Minute {
		t.Fatalf("Auto bound = %v bounded=%v", got, bounded)
	}
	s.cfg.Agents.MaxWaitMinutes = 3
	if got, bounded := s.boundWait(50 * time.Minute); !bounded || got != 3*time.Minute {
		t.Fatalf("configured bound = %v bounded=%v", got, bounded)
	}
	if got, bounded := s.boundWait(30 * time.Second); bounded || got != 30*time.Second {
		t.Fatalf("a short wait must not change: %v bounded=%v", got, bounded)
	}
	if note := waitBoundNote(3 * time.Minute); !strings.Contains(note, "3 minutes") || !strings.Contains(note, "wait again") {
		t.Fatalf("bound note = %q", note)
	}
}

// A human message must reach the agent even when it is blocked in a
// synchronous command (a shell sleep, a slow build), including a message sent
// at the same instant the command started. The command keeps running in the
// background; only the tool call returns.
func TestHumanMessagePromotesBlockingCommandAtOnce(t *testing.T) {
	for _, timing := range []string{"before", "during"} {
		t.Run(timing, func(t *testing.T) {
			setPromotionThreshold(t, time.Minute)
			s, store, recipient, _ := collaborationContractFixture(t)
			mgr, lg := promotionTestManager(t)
			run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{
				AgentID: recipient.ID, RuntimeEpoch: recipient.RuntimeEpoch, Origin: collaboration.OriginHuman,
			})
			if err != nil {
				t.Fatal(err)
			}
			publish := func() {
				if _, err := store.Publish(context.Background(), collaboration.PublishRequest{
					SenderID: "human", Origin: collaboration.OriginHuman, Kind: collaboration.EventHumanSteering, RunID: run.ID,
					IdempotencyKey: "steer-" + timing, RecipientIDs: []string{recipient.ID}, Body: "Stop and read this.",
				}); err != nil {
					t.Error(err)
				}
			}
			if timing == "before" {
				publish()
			}
			uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 0)
			if err != nil {
				t.Fatal(err)
			}
			if timing == "during" {
				time.AfterFunc(150*time.Millisecond, publish)
			}
			observe, stop := context.WithCancel(t.Context())
			defer stop()
			started := time.Now()
			result, err := awaitCommandWithPromotion(t.Context(), mgr, uc, lg, "", s.humanInputWake(observe, recipient.ID))
			if err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("the message waited %v for the command", elapsed)
			}
			info, ok := mgr.Check(promotedHandle(t, result.Content))
			if !ok || info.Status != fileio.AsyncRunning {
				t.Fatalf("command was not handed back as a running background command: %+v", info)
			}
			page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: recipient.ID, Unadmitted: true})
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("observing must not consume the message: %+v %v", page, err)
			}
		})
	}
}

// Peer traffic does not cut short a command: only a waiting human does.
func TestPeerNoteDoesNotPromoteBlockingCommand(t *testing.T) {
	setPromotionThreshold(t, 400*time.Millisecond)
	s, store, recipient, sender := collaborationContractFixture(t)
	mgr, lg := promotionTestManager(t)
	collaborationWaitCommunication(t, store, recipient, sender, "note")()
	uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	observe, stop := context.WithCancel(t.Context())
	defer stop()
	started := time.Now()
	if _, err := awaitCommandWithPromotion(t.Context(), mgr, uc, lg, "", s.humanInputWake(observe, recipient.ID)); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 350*time.Millisecond {
		t.Fatal("a peer note promoted the command early")
	}
}

// /compact is authoritative at the next step boundary: a wait or a slow
// command must not hold it for its own timeout. The command keeps running in
// the background, and the next wait after the turn took the request is not
// cut short again.
func TestHumanCompactionEndsWaitsAtOnce(t *testing.T) {
	setPromotionThreshold(t, time.Minute)
	s, store, recipient, _ := collaborationContractFixture(t)
	o := NewOrchestrator(recipient.ID, s)
	s.orchestrators = map[string]*Orchestrator{recipient.ID: o}
	_, cancelTurn := context.WithCancel(t.Context())
	defer cancelTurn()
	o.turnGeneration, o.turnCancel = 4, cancelTurn
	ctx := context.WithValue(t.Context(), tools.ExecutionIdentityKey{}, tools.ExecutionIdentity{AgentID: recipient.ID})
	request := func() {
		if _, handed := o.requestCompaction("compact-now"); !handed {
			t.Error("the running turn refused the request")
		}
	}

	time.AfterFunc(150*time.Millisecond, request)
	started := time.Now()
	got, err := s.waitForCollaborationDuration(ctx, store, nil, time.Minute)
	if err != nil || got.Status != "compaction_requested" || time.Since(started) > 10*time.Second {
		t.Fatalf("wait = %+v after %v (%v)", got, time.Since(started), err)
	}
	if o.takeCompactionRequest(4) != "compact-now" {
		t.Fatal("the turn could not take the request")
	}
	if got, _ := s.waitForCollaborationDuration(ctx, store, nil, 300*time.Millisecond); got.Status != "timeout_no_match" {
		t.Fatalf("a taken request still ends waits: %+v", got)
	}

	mgr, lg := promotionTestManager(t)
	uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(150*time.Millisecond, request)
	observe, stop := context.WithCancel(t.Context())
	defer stop()
	started = time.Now()
	result, err := awaitCommandWithPromotion(t.Context(), mgr, uc, lg, "", s.humanInputWake(observe, recipient.ID))
	if err != nil || time.Since(started) > 10*time.Second {
		t.Fatalf("command held the compaction for %v: %v", time.Since(started), err)
	}
	if info, ok := mgr.Check(promotedHandle(t, result.Content)); !ok || info.Status != fileio.AsyncRunning {
		t.Fatalf("command was not handed back as a running background command: %+v", info)
	}
}
