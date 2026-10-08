package agent

import (
	"errors"
	"testing"

	rootprompt "crude/prompt"
	"crude/provider"
)

func TestRuntimeNoticesCommitAsOneBoundaryBeforeCheckpoint(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "admitted", true: "rejected"}[denied], func(t *testing.T) {
			a := newTestAgent(t, provider.FromPreset("openai-chat", "key", "http://127.0.0.1:9"), "test")
			first, second := provider.NewTextMessage("user", "first note"), provider.NewTextMessage("user", "second note")
			first.InternalID, second.InternalID = "note-one", "note-two"
			a.PrepareRuntimeNotices = func() []provider.ChatMessage { return []provider.ChatMessage{first, second} }
			committed := false
			a.CommitRuntimeNotices = func(messages []provider.ChatMessage) error {
				if denied {
					return errors.New("receipt rejected")
				}
				committed = true
				return nil
			}
			checkpoints := 0
			a.Checkpoint = func(_, active []provider.ChatMessage) error {
				checkpoints++
				if !committed {
					t.Error("checkpoint preceded durable admission")
				}
				if len(active) != 2 || active[0].InternalID != first.InternalID || active[1].InternalID != second.InternalID {
					t.Error("checkpoint temporarily omitted an admitted sibling notice")
				}
				return errors.New("injected checkpoint storage failure")
			}
			a.Run(t.Context(), nil, "system", nil, nil, "session", nil)
			if denied && checkpoints != 0 {
				t.Fatalf("rejected notices were checkpointed %d times", checkpoints)
			}
			if !denied && checkpoints != 1 {
				t.Fatalf("admitted batch checkpoint count=%d", checkpoints)
			}
			if a.RunFailureKind() != RecoveryFailureStorage {
				t.Fatalf("failure kind=%s", a.RunFailureKind())
			}
		})
	}
}

func TestRecoveryBoundariesHaveOneStableArchiveAndContextIdentity(t *testing.T) {
	active, archive := appendRecoveryContinuation(nil, nil, rootprompt.RecoveryTransport)
	if active[0].InternalID == "" || active[0].InternalID != archive[0].InternalID {
		t.Fatal("continuation has split or anonymous identity")
	}
	archive, active = appendStoppedBoundary(archive, active)
	if active[1].InternalID == "" || active[1].InternalID != archive[1].InternalID {
		t.Fatal("stop has split or anonymous identity")
	}
	repeatedArchive, repeatedActive := appendStoppedBoundary(archive, active)
	if len(repeatedArchive) != 2 || len(repeatedActive) != 2 || repeatedActive[1].InternalID != active[1].InternalID {
		t.Fatal("repeated stop minted another identity")
	}
}
