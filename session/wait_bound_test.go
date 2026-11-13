package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/fileio"
	ttypes "crude/types"
)

func TestWaitBoundFollowsSettingsAndAuto(t *testing.T) {
	s := &Session{cfg: &Config{}}
	if got, bounded := s.boundWait(time.Hour); !bounded || got != ttypes.DefaultMaxWaitMinutes*time.Minute {
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
