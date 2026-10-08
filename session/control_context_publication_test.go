package session

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	recoverytypes "crude/types/recovery"
)

func TestRootControlReconciliationPreservesExactPublicationAndRejectsCorruption(t *testing.T) {
	for _, outcome := range []string{"late-control", "already-present", "changed-control", "changed-attribution", "duplicate-control", "conflicting-duplicate", "reordered-controls", "missing-input", "changed-input", "storage-failure"} {
		t.Run(outcome, func(t *testing.T) {
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			t.Cleanup(func() { _ = db.Close() })
			if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
				t.Fatal(err)
			}
			original := provider.NewTextMessage("user", "immutable original human input")
			original.InternalID = "original"
			prepared := provider.NewTextMessage("user", "immutable preparation")
			prepared.InternalID, prepared.Origin, prepared.Visibility = "original-context", provider.OriginRuntimeContext, provider.VisibilityContextOnly
			stamp := time.Unix(100, 0)
			initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
				SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
					contextDelta(original, sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: "authored display"}, 1, stamp, "root"),
					contextDelta(prepared, sessiondb.DisplayMeta{ContextOnly: true}, 1, stamp, "root"),
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			controls := []provider.ChatMessage{
				collaborationMessage(collaboration.PendingDelivery{Event: collaboration.Event{
					ID: "control-one", ThreadID: "control-one", SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
					Kind: collaboration.EventLifecycleControl, RunID: "stopped-run", Body: "Stop the exact first run.",
					References: []collaboration.Reference{{Kind: "control", ID: "cancel", Revision: 1}},
				}}),
				collaborationMessage(collaboration.PendingDelivery{Event: collaboration.Event{
					ID: "control-two", ThreadID: "control-two", SenderID: "parent", SenderName: "Parent", Origin: collaboration.OriginPeer,
					Kind: collaboration.EventLifecycleControl, RunID: "another-run", Body: "Pause the exact second run.",
					References: []collaboration.Reference{{Kind: "control", ID: "pause", Revision: 1}},
				}}),
			}
			controlDeltas := make([]sessiondb.ContextMessageDelta, 0, len(controls))
			for i, control := range controls {
				controlDeltas = append(controlDeltas, contextDelta(control, sessiondb.DisplayMeta{ContextOnly: true}, 0, stamp.Add(time.Duration(i+1)*time.Second), "root"))
			}
			current, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
				SessionID: "session", AgentID: "session", ExpectedRevision: initial.Revision, Mutation: sessiondb.ContextMutation{Append: controlDeltas},
			})
			if err != nil {
				t.Fatal(err)
			}
			beforeRows, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil || len(beforeRows) != 1 {
				t.Fatalf("control admission changed transcript: rows=%+v err=%v", beforeRows, err)
			}
			accepted := provider.NewTextMessage("assistant", "accepted output awaiting terminal mirror")
			accepted.InternalID, accepted.ReplayScope = "accepted", "exact provider replay scope"
			run := newRecoveryRunState(provider.UserInput{InternalID: original.InternalID, Text: original.ContentString()}, &sessiondb.RecoveryCheckpoint{
				Archive: initial.Messages, ActiveContext: initial.Messages, ContextOwnerID: "session", ContextRevision: initial.Revision,
				ContextGeneration: initial.Generation, ContextReason: initial.Reason, InputAdmitted: true,
			})
			snapshot := append(append([]provider.ChatMessage(nil), initial.Messages...), accepted)
			if outcome != "late-control" && outcome != "missing-input" && outcome != "changed-input" && outcome != "storage-failure" {
				snapshot = append(snapshot, canonicalContextMessages(controls)...)
			}
			switch outcome {
			case "changed-control":
				snapshot[3].Content = provider.TextContent("same identity, different control bytes")
			case "changed-attribution":
				attribution := *snapshot[3].Collaboration
				attribution.RunID = "replacement-run"
				snapshot[3].Collaboration = &attribution
			case "duplicate-control":
				snapshot = append(snapshot, snapshot[3])
			case "conflicting-duplicate":
				changed := snapshot[3]
				changed.Content = provider.TextContent("conflicting second copy")
				snapshot = append(snapshot, changed)
			case "reordered-controls":
				snapshot[3], snapshot[4] = snapshot[4], snapshot[3]
			case "missing-input":
				snapshot = snapshot[1:]
			case "changed-input":
				snapshot[0].Content = provider.TextContent("different original human bytes")
			case "storage-failure":
				if _, err := db.Exec(`CREATE TRIGGER reject_reconciled_checkpoint BEFORE INSERT ON agent_recoveries BEGIN SELECT RAISE(ABORT,'checkpoint storage unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			run.snapshot(snapshot, snapshot)
			if outcome == "changed-input" {
				// Snapshotting deliberately restores previously observed canonical
				// prefix bytes. Inject corruption at the publication boundary itself
				// to verify it still cannot admit an altered committed source.
				run.active[0].Content = provider.TextContent("corrupted submitted bytes")
				run.archive[0].Content = run.active[0].Content
			}
			s := &Session{db: db, sessionID: "session", activeContext: current.Messages, contextRevision: current.Revision, contextGeneration: current.Generation, contextReason: current.Reason}
			status := recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 2, State: recoverytypes.NeedsAttention, StartedAt: stamp}
			err = s.publishRootRecovery(run, status)
			valid := outcome == "late-control" || outcome == "already-present"
			if valid && err != nil || !valid && err == nil {
				t.Fatalf("publication valid=%v error=%v", valid, err)
			}
			if !valid {
				after, found, loadErr := sessiondb.LoadContextState(db, "session", "session")
				if loadErr != nil || !found || !reflect.DeepEqual(after, current) || !reflect.DeepEqual(s.activeContext, current.Messages) || s.contextRevision != current.Revision {
					t.Fatalf("rejected checkpoint partially changed authority: after=%+v err=%v", after, loadErr)
				}
				if _, open, loadErr := sessiondb.LoadRecoveryCheckpoint(db, "session", "root"); loadErr != nil || open {
					t.Fatalf("rejected publication installed a checkpoint: open=%v err=%v", open, loadErr)
				}
				afterRows, loadErr := sessiondb.LoadMessagesWithMeta(db)
				if loadErr != nil || !reflect.DeepEqual(afterRows, beforeRows) {
					t.Fatalf("rejected publication changed transcript: rows=%+v err=%v", afterRows, loadErr)
				}
				var acceptedIDs int
				if loadErr := db.QueryRow(`SELECT COUNT(*) FROM immutable_message_identities WHERE internal_id='accepted'`).Scan(&acceptedIDs); loadErr != nil || acceptedIDs != 0 {
					t.Fatalf("rejected publication registered output identity: count=%d err=%v", acceptedIDs, loadErr)
				}
				if outcome != "storage-failure" {
					return
				}
				if _, err := db.Exec(`DROP TRIGGER reject_reconciled_checkpoint`); err != nil {
					t.Fatal(err)
				}
				if err := s.publishRootRecovery(run, status); err != nil {
					t.Fatalf("checked persistence retry failed: %v", err)
				}
			}
			state, found, err := sessiondb.LoadContextState(db, "session", "session")
			if err != nil || !found || len(state.Messages) != 5 {
				t.Fatalf("reconciliation lost original, accepted output or controls: state=%+v err=%v", state, err)
			}
			archive, active := run.contexts()
			for _, messages := range [][]provider.ChatMessage{state.Messages, archive, active} {
				counts := make(map[string]int)
				for _, message := range messages {
					counts[message.InternalID]++
				}
				for _, expected := range canonicalContextMessages([]provider.ChatMessage{original, prepared, accepted, controls[0], controls[1]}) {
					if counts[expected.InternalID] != 1 {
						t.Fatalf("reconciliation lost/duplicated identity %q: %v", expected.InternalID, counts)
					}
					for _, message := range messages {
						if message.InternalID == expected.InternalID && !reflect.DeepEqual(canonicalContextMessage(message), expected) {
							t.Fatalf("reconciliation rewrote identity %q", expected.InternalID)
						}
					}
				}
			}
			checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
			if err != nil || !open || checkpoint.ContextRevision != state.Revision || !reflect.DeepEqual(checkpoint.ActiveContext, state.Messages) {
				t.Fatalf("checkpoint is not tied to exact published context: checkpoint=%+v err=%v", checkpoint, err)
			}
			if err := s.publishRootRecovery(run, status); err != nil {
				t.Fatalf("status-only retry replayed controls: %v", err)
			}
			repeated, _, err := sessiondb.LoadContextState(db, "session", "session")
			if err != nil || !reflect.DeepEqual(repeated.Messages, state.Messages) {
				t.Fatalf("status-only retry duplicated context: messages=%+v err=%v", repeated.Messages, err)
			}
			afterRows, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil || !reflect.DeepEqual(afterRows, beforeRows) {
				t.Fatalf("reconciliation restamped transcript metadata: after=%+v err=%v", afterRows, err)
			}
		})
	}
}

func TestCommittedRecoveryMergeRejectsDuplicateOrChangedIDsWithoutMutation(t *testing.T) {
	control := canonicalContextMessage(collaborationMessage(collaboration.PendingDelivery{Event: collaboration.Event{
		ID: "control", Origin: collaboration.OriginHuman, Kind: collaboration.EventLifecycleControl, RunID: "run", Body: "Stop",
	}}))
	for _, corruption := range []string{"bytes", "same-duplicate", "different-duplicate"} {
		t.Run(corruption, func(t *testing.T) {
			snapshot := []provider.ChatMessage{control}
			if corruption == "bytes" {
				snapshot[0].Content = provider.TextContent("changed")
			} else {
				snapshot = append(snapshot, control)
				if corruption == "different-duplicate" {
					snapshot[1].Content = provider.TextContent("changed")
				}
			}
			run := newRecoveryRunState(provider.UserInput{}, &sessiondb.RecoveryCheckpoint{
				Archive: snapshot, ActiveContext: snapshot, ContextOwnerID: "owner", ContextRevision: 1, ContextGeneration: 1,
			})
			before := run.checkpoint(recoverytypes.Status{})
			err := reconcileCommittedRecoveryContext(run, sessiondb.ContextState{AgentID: "owner", Revision: 2, Generation: 1, Messages: []provider.ChatMessage{control}})
			var conflict *sessiondb.ContextMessageConflictError
			if !errors.As(err, &conflict) || conflict.InternalID != control.InternalID || !reflect.DeepEqual(before, run.checkpoint(recoverytypes.Status{})) {
				t.Fatalf("changed/duplicate identity was accepted or mutated its source: error=%v", err)
			}
		})
	}
}

func TestChildCheckpointAdoptsLateControlWithoutDroppingAcceptedOutput(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%t", corrupt), func(t *testing.T) {
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := sessiondb.CreateSessionRow(db, "child"); err != nil {
				t.Fatal(err)
			}
			original := canonicalContextMessage(provider.NewTextMessage("user", "original assignment"))
			original.InternalID = "assignment"
			initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "child", AgentID: "child", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "test", Messages: []provider.ChatMessage{original}}}})
			if err != nil {
				t.Fatal(err)
			}
			run := newRecoveryRunState(provider.UserInput{}, &sessiondb.RecoveryCheckpoint{ContextOwnerID: "child", ContextRevision: initial.Revision, ContextGeneration: 1, ContextReason: "test", Archive: initial.Messages, ActiveContext: initial.Messages})
			accepted := provider.NewTextMessage("assistant", "accepted work")
			accepted.InternalID = "accepted"
			snapshot := append(append([]provider.ChatMessage(nil), initial.Messages...), accepted)
			run.snapshot(snapshot, snapshot)
			control := collaborationMessage(collaboration.PendingDelivery{Event: collaboration.Event{ID: "pause", Origin: collaboration.OriginHuman, Kind: collaboration.EventLifecycleControl, RunID: "exact-run", Body: "Pause", References: []collaboration.Reference{{Kind: "control", ID: "pause", Revision: 1}}}})
			current, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "child", AgentID: "child", ExpectedRevision: initial.Revision, Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{contextDelta(control, sessiondb.DisplayMeta{ContextOnly: true}, 0, time.Now(), "child")}}})
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				run.active[0].Content = provider.TextContent("changed assignment")
			}
			err = publishChildRecovery(db, "child", run, recoverytypes.Status{ID: "incident", AgentID: "child", Generation: 1})
			if corrupt {
				if err == nil {
					t.Fatal("corrupted original was accepted")
				}
				got, _, loadErr := sessiondb.LoadContextState(db, "child", "child")
				if loadErr != nil || !reflect.DeepEqual(got, current) {
					t.Fatal("rejected checkpoint changed authority")
				}
				return
			}
			if err != nil {
				t.Fatalf("late exact control wedged child publication: %v", err)
			}
			checkpoint, ok, err := sessiondb.LoadRecoveryCheckpoint(db, "child", "child")
			if err != nil || !ok {
				t.Fatal(err)
			}
			for _, messages := range [][]provider.ChatMessage{checkpoint.Archive, checkpoint.ActiveContext} {
				for _, id := range []string{"assignment", "accepted", "pause"} {
					count := 0
					for _, m := range messages {
						if m.InternalID == id {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("%s count=%d", id, count)
					}
				}
			}
		})
	}
}
