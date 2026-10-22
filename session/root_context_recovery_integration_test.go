package session

import (
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

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
)

func TestRootStopDuringFailedTerminalSettlementRetriesOnlyPersistence(t *testing.T) {
	for _, failure := range []string{"turn-ledger", "collaboration", "collaboration-restart"} {
		t.Run(failure, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			var requests, acknowledgements atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				switch requests.Add(1) {
				case 1:
					_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"root-settlement-read","type":"function","function":{"name":"list_directory","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
				case 2:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"root accepted final output\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				default:
					t.Error("stopped root persistence retry requested another provider answer")
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			t.Cleanup(server.Close)
			cfg := modelAttributionConfig(server.URL, "root-settlement-"+failure)
			s, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			s.SetWorkDir(project)
			s.SetYolo(true)
			store, err := collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			s.SetCollaboration(store, "root-terminal-fixture")
			trigger := `CREATE TRIGGER reject_root_settlement BEFORE UPDATE ON agent_turns WHEN NEW.state<>'running' BEGIN SELECT RAISE(ABORT, 'root ledger unavailable'); END`
			if strings.HasPrefix(failure, "collaboration") {
				trigger = `CREATE TRIGGER reject_root_settlement BEFORE UPDATE ON collaboration_runs WHEN NEW.state<>'running' BEGIN SELECT RAISE(ABORT, 'root collaboration unavailable'); END`
			}
			if _, err := s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = s.db.Exec(`DROP TRIGGER IF EXISTS reject_root_settlement`) })
			baseSender := newMockEventSender()
			sender := &sessionSupervisedJourneySender{mockEventSender: baseSender, observe: func(typ string, _ any, _ string) {
				if typ == "done" {
					acknowledgements.Add(1)
				}
			}}
			root, err := s.NewOrchestrator(sender)
			if err != nil {
				t.Fatal(err)
			}
			input := provider.UserInput{InternalID: "root-settlement-original", Text: "retain this exact original root input", DisplayText: "authored root display"}
			if !root.SubmitInput(input) {
				t.Fatal("real root input admission failed")
			}
			first := waitTerminalSettlementAttention(t, baseSender, root.ID)
			before, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil || !slices.ContainsFunc(before, func(row sessiondb.MessageWithMeta) bool {
				return row.Msg.ContentString() == "root accepted final output"
			}) {
				t.Fatalf("root settlement fault preceded context commit: rows=%+v err=%v", before, err)
			}
			actor, err := store.Agent(t.Context(), root.ID)
			if err != nil || actor.CurrentRunID == "" {
				t.Fatalf("real root run is unavailable: actor=%+v err=%v", actor, err)
			}
			if s.CancelRecoveryFor(root.ID, first.ID, first.Generation) != nil {
				t.Fatal("exact root Stop was rejected")
			}
			blocked := waitTerminalSettlementAttention(t, baseSender, root.ID)
			if blocked.ID != first.ID || blocked.Generation != first.Generation || acknowledgements.Load() != 0 {
				t.Fatal("root Stop falsely acknowledged failed ledger or collaboration settlement")
			}
			checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(s.db, s.sessionID, root.ID)
			if err != nil || !open || checkpoint.Status.State != recoverytypes.NeedsAttention || checkpoint.Status.Generation != first.Generation {
				t.Fatalf("stopped root lost its exact open envelope: open=%v checkpoint=%+v err=%v", open, checkpoint, err)
			}
			if failure == "collaboration-restart" {
				captured, err := terminalIntent(checkpoint)
				if err != nil || captured == nil || captured.Child != nil || captured.TurnState != sessiondb.TurnCompleted || captured.Run.RunID != actor.CurrentRunID || captured.Run.ContextCommitID != input.InternalID || !captured.Stopped {
					t.Fatalf("root restart has no typed captured terminal ownership: intent=%+v err=%v", captured, err)
				}
				path := s.dbPath
				s.Close()
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				resumed, resumeErr := ResumeSessionWithDir(path, cfg, home, filepath.Join(home, "sessions"))
				err = resumeErr
				if err != nil {
					t.Fatal(err)
				}
				s = resumed
				t.Cleanup(s.Close)
				s.SetWorkDir(project)
				store, err = collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				s.SetCollaboration(store, "root-terminal-restart")
				root, err = s.NewOrchestrator(sender)
				if err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 2 || acknowledgements.Load() != 0 || root.TurnActive() {
					t.Fatal("passive root resume executed terminal work")
				}
			}
			if _, err := s.db.Exec(`DROP TRIGGER reject_root_settlement`); err != nil {
				t.Fatal(err)
			}
			if s.RetryRecoveryFor(root.ID, blocked.ID, blocked.Generation) != nil {
				t.Fatal("exact root persistence retry was rejected")
			}
			waitTransitionTurn(t, baseSender, root)
			after, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil || len(after) != len(before)+1 || !reflect.DeepEqual(before, after[:len(before)]) || after[len(before)].Msg.EffectiveOrigin() != provider.OriginTurnStopped {
				t.Fatalf("root cancellation rewrote accepted metadata or duplicated its boundary: rows=%+v err=%v", after, err)
			}
			settled, err := store.Run(t.Context(), actor.CurrentRunID)
			if err != nil || settled.State != collaboration.RunSucceeded || settled.ContextCommitID != input.InternalID {
				t.Fatalf("root cancellation did not settle the exact original run: %+v err=%v", settled, err)
			}
			latest, _, err := sessiondb.LatestTurnStates(t.Context(), s.db, s.sessionID)
			if err != nil || latest == nil || latest.Generation != first.Generation || latest.State != sessiondb.TurnCompleted {
				t.Fatalf("root cancellation did not persist the original actor generation: turn=%+v err=%v", latest, err)
			}
			root.turnMu.Lock()
			generation := root.turnGeneration
			root.turnMu.Unlock()
			if requests.Load() != 2 || sender.toolStarts.Load() != 1 || sender.toolResults.Load() != 1 || acknowledgements.Load() != 1 || generation != first.Generation {
				t.Fatalf("root storage retry replayed work or invented finalization: requests=%d tools=%d/%d acknowledgements=%d generation=%d", requests.Load(), sender.toolStarts.Load(), sender.toolResults.Load(), acknowledgements.Load(), generation)
			}
		})
	}
}

func TestRecoveryStatusPublicationPreservesNewerRootContext(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	original := provider.NewTextMessage("user", "original accepted input")
	original.InternalID = "original"
	initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "initial", Messages: []provider.ChatMessage{original}}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:       recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 1, StartedAt: time.Now()},
		PendingInput: provider.UserInput{InternalID: original.InternalID, Text: original.ContentString()}, InputAdmitted: true,
		Archive: []provider.ChatMessage{original}, ActiveContext: []provider.ChatMessage{original},
		ContextOwnerID: "session", ContextRevision: initial.Revision, ContextGeneration: initial.Generation, ContextReason: initial.Reason,
	}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	state := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	newer := provider.NewTextMessage("user", "newer accepted fact")
	newer.InternalID = "newer"
	current, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", ExpectedRevision: checkpoint.ContextRevision, Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{{Message: newer, CreatedAt: time.Now().UnixNano(), AgentID: "root"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{db: db, sessionID: "session", activeContext: current.Messages, contextRevision: current.Revision, contextGeneration: current.Generation, contextReason: current.Reason}
	status := checkpoint.Status
	status.State = recoverytypes.NeedsAttention
	status.Summary = "still blocked"
	if err := s.publishRootRecovery(state, status); err != nil {
		t.Fatal(err)
	}
	got, found, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !found || len(got.Messages) != 2 || got.Messages[1].InternalID != newer.InternalID {
		t.Fatalf("status-only publication regressed current context: %+v found=%v err=%v", got, found, err)
	}
}

func TestFinalizationCannotOverwriteNewerInputAdmission(t *testing.T) {
	s, err := NewWithSessionDir("finalization-fence", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	o, err := s.NewOrchestrator(newMockEventSender())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.PrepareInputAdmission(o.ID, provider.PlainInput("newer submitted input"), 1)
	if err != nil {
		t.Fatal(err)
	}
	o.CancelTurn()
	if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionPreservationPending {
		t.Fatalf("admission = %+v", result)
	}
	before := s.contextRevision
	stale := []provider.ChatMessage{provider.NewTextMessage("assistant", "old terminal answer")}
	if err := s.finalizeTurn(stale, 0, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}, "", "old", ttypes.TurnIdentity{Generation: 1}, stale); err == nil {
		t.Fatal("stale finalization replaced a newer submitted source")
	}
	current, found, err := sessiondb.LoadContextState(s.db, s.sessionID, s.sessionID)
	if err != nil || !found || current.Revision != before || len(current.Messages) != 2 || current.Messages[0].InternalID != intent.InputID || current.Messages[1].InternalID != intent.InputID+"-context" || len(s.msgs) != 2 || s.msgs[0].InternalID != intent.InputID || s.msgs[1].InternalID != intent.InputID+"-context" {
		t.Fatalf("failed finalization erased admission: live=%+v durable=%+v found=%v error=%v", s.msgs, current, found, err)
	}
}

func TestRecoveryContinuationPublishesCanonicalSnapshotOnce(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", "accepted input")
	input.InternalID = "input"
	initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: "session", AgentID: "session", Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "initial", Messages: []provider.ChatMessage{input}}}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{Status: recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 1, StartedAt: time.Now()}, PendingInput: provider.UserInput{InternalID: input.InternalID, Text: input.ContentString()}, InputAdmitted: true, Archive: []provider.ChatMessage{input}, ActiveContext: []provider.ChatMessage{input}, ContextOwnerID: "session", ContextRevision: initial.Revision, ContextGeneration: initial.Generation, ContextReason: initial.Reason}
	if err := sessiondb.SaveRecoveryCheckpoint(db, "session", checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	state := newRecoveryRunState(checkpoint.PendingInput, &checkpoint)
	state.appendContinuation("interrupted")
	status := checkpoint.Status
	status.State = recoverytypes.NeedsAttention
	if err := state.publish(db, "session", status); err != nil {
		t.Fatal(err)
	}
	first, found, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !found || len(first.Messages) != 2 || first.Messages[1].EffectiveOrigin() != provider.OriginContinuation {
		t.Fatalf("continuation not published: %+v found=%v err=%v", first, found, err)
	}
	if err := state.publish(db, "session", status); err != nil {
		t.Fatal(err)
	}
	second, _, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || len(second.Messages) != 2 {
		t.Fatalf("status retry duplicated canonical continuation: %+v err=%v", second, err)
	}
}

func TestPassiveRootResumeReconcilesSemanticArchiveByID(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	home, directory := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
	cfg := modelAttributionConfig(server.URL, "passive-root-archive")
	s, err := NewWithSessionDir("passive-root-archive", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	project := t.TempDir()
	s.SetWorkDir(project)
	when := time.Unix(1_700_000_000, 123)
	var archive []provider.ChatMessage
	var originals []sessiondb.MessageWithMeta
	for turn := 1; turn <= 2; turn++ {
		input := provider.NewTextMessage("user", "original submitted input")
		input.InternalID = "input-first"
		if turn == 2 {
			input.InternalID, input.Content = "input-later", provider.TextContent("later interrupted input")
		}
		preparation := provider.NewTextMessage("user", "captured hidden preparation for "+input.InternalID)
		preparation.InternalID = input.InternalID + "-context"
		preparation.Origin, preparation.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
		meta := canonicalizeDisplayMeta(input, sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: "authored display", ReferenceLabels: []string{"selected-reference"}}, "", turn, when)
		deltas := []sessiondb.ContextMessageDelta{
			contextDelta(input, meta, turn, when, "old-root"),
			contextDelta(preparation, sessiondb.DisplayMeta{DisplayRole: "hidden_runtime_context", ContextOnly: true}, turn, when, "old-root"),
		}
		s.msgsMu.Lock()
		_, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: deltas}})
		s.msgsMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		archive = append(archive, deltas[0].Message, deltas[1].Message)
		answer := provider.NewTextMessage("assistant", "accepted output before interruption")
		answer.InternalID = "answer-first"
		answer.ReasoningDuration = 2 * time.Second
		answer.ReplayScope, answer.ReplayPrefixHash, answer.ReplaySourceModel = "exact-connection", "exact-prefix", "producing-model"
		answer.Refusal = "retained provider field"
		if turn == 1 {
			answerMeta := canonicalizeDisplayMeta(answer, sessiondb.DisplayMeta{DisplayRole: "assistant", TurnGeneration: 1}, "", 2, when.Add(time.Second))
			s.msgsMu.Lock()
			_, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{contextDelta(answer, answerMeta, turn, when.Add(time.Second), "old-root")}}})
			s.msgsMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			archive = append(archive, canonicalContextMessage(answer))
		} else {
			answer.InternalID = "answer-later"
			answer.ToolCalls = []provider.ToolCall{{ID: "never-replay", Type: "function", Function: provider.FunctionCall{Name: "write_file", Arguments: `{"path":"never-replay.txt","content":"must not execute"}`}}}
			archive = append(archive, canonicalContextMessage(answer))
		}
	}
	originals, err = sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil || len(originals) != 3 {
		t.Fatalf("seed transcript = %+v, error=%v", originals, err)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:       recoverytypes.Status{ID: "later-incident", AgentID: "old-root", Generation: 2, State: recoverytypes.Recovering, StartedAt: when, UpdatedAt: when.Add(2 * time.Second)},
		PendingInput: provider.UserInput{InternalID: "input-later", Text: "later interrupted input"}, InputAdmitted: true,
		Archive: archive, ActiveContext: archive, ContextOwnerID: s.sessionID,
	}
	if _, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{
		SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: "old-root", ExpectedRevision: s.contextRevision,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "accepted_output", Messages: archive}}, Recovery: &checkpoint,
	}); err != nil {
		t.Fatal(err)
	}
	path := s.dbPath
	s.Close()
	var restoredIDs []string
	for restart := 0; restart < 2; restart++ {
		resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
		if err != nil {
			t.Fatalf("passive restart %d: %v", restart, err)
		}
		rows, err := sessiondb.LoadMessagesWithMeta(resumed.db)
		if err != nil || len(rows) != 5 || !reflect.DeepEqual(rows[:3], originals) {
			resumed.Close()
			t.Fatalf("restart changed stored transcript metadata or duplicated rows: %+v, error=%v", rows, err)
		}
		current, found, err := sessiondb.LoadContextState(resumed.db, resumed.sessionID, resumed.sessionID)
		if err != nil || !found || len(current.Messages) != 7 {
			resumed.Close()
			t.Fatalf("restart lost semantic archive: %+v, found=%v error=%v", current, found, err)
		}
		ids := make([]string, 0, len(current.Messages))
		for i, message := range current.Messages {
			ids = append(ids, message.InternalID)
			if i < len(archive) && !reflect.DeepEqual(message, archive[i]) {
				resumed.Close()
				t.Fatalf("accepted fact %d changed: got=%+v want=%+v", i, message, archive[i])
			}
		}
		if restart == 0 {
			restoredIDs = ids
		} else if !reflect.DeepEqual(ids, restoredIDs) {
			resumed.Close()
			t.Fatalf("restart changed immutable IDs: %v -> %v", restoredIDs, ids)
		}
		// The transcript paints exactly the hydrated rows' snapshots. Hidden
		// preparation restored from the archive must not become a human row.
		for _, row := range resumed.MessagesWithMeta() {
			for _, snapshot := range row.Meta.Snapshots {
				if strings.Contains(snapshot.Content, "captured hidden preparation") {
					resumed.Close()
					t.Fatalf("restart %d painted context-only preparation as a %s row: %+v", restart, snapshot.Role, snapshot)
				}
			}
		}
		unknown := current.Messages[6]
		if unknown.InternalID == "" || unknown.ToolCallID != "never-replay" || !unknown.ToolError {
			resumed.Close()
			t.Fatalf("missing outcome was not durably reconciled: %+v", unknown)
		}
		if requests.Load() != 0 || resumed.SubAgentRunner() != nil {
			resumed.Close()
			t.Fatal("passive resume started execution")
		}
		if _, err := os.Stat(filepath.Join(project, "never-replay.txt")); !os.IsNotExist(err) {
			resumed.Close()
			t.Fatalf("historical tool executed: %v", err)
		}
		resumed.Close()
	}
}

func TestPassiveRootResumePreservesNewerCommittedContext(t *testing.T) {
	for _, mutation := range []string{"context-only", "compaction"} {
		t.Run(mutation, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			home, directory := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
			cfg := modelAttributionConfig(server.URL, "passive-newer-context")
			s, err := NewWithSessionDir("passive-newer-context", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			input := provider.NewTextMessage("user", "accepted original")
			input.InternalID = "original"
			preparation := provider.NewTextMessage("user", "original preparation")
			preparation.InternalID = "original-context"
			preparation.Origin, preparation.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			archive := canonicalContextMessages([]provider.ChatMessage{input, preparation})
			checkpoint := sessiondb.RecoveryCheckpoint{
				Status:       recoverytypes.Status{ID: "older-checkpoint", AgentID: "old-root", Generation: 1, State: recoverytypes.Recovering, StartedAt: time.Now()},
				PendingInput: provider.UserInput{InternalID: input.InternalID, Text: input.ContentString()}, InputAdmitted: true, Archive: archive, ActiveContext: archive, ContextOwnerID: s.sessionID,
			}
			initial, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{
				SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: "old-root", Recovery: &checkpoint,
				Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
					contextDelta(input, sessiondb.DisplayMeta{DisplayRole: "user"}, 1, time.Now(), "old-root"),
					contextDelta(preparation, sessiondb.DisplayMeta{ContextOnly: true}, 1, time.Now(), "old-root"),
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			newFact := provider.NewTextMessage("user", "newer committed context-only fact")
			newFact.InternalID = "new-fact"
			newFact.Origin, newFact.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			publication := sessiondb.ContextPublication{SessionID: s.sessionID, AgentID: s.sessionID, ExpectedRevision: initial.Revision}
			publication.Mutation.Append = []sessiondb.ContextMessageDelta{contextDelta(newFact, sessiondb.DisplayMeta{ContextOnly: true}, 1, time.Now(), "old-root")}
			if mutation == "compaction" {
				summary := provider.NewTextMessage("user", "completed immutable compaction checkpoint")
				summary.InternalID = "compacted-checkpoint"
				summary.Origin, summary.Visibility = provider.OriginCheckpoint, provider.VisibilityContextOnly
				publication.Mutation = sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 2, Reason: "automatic_compaction", Messages: canonicalContextMessages([]provider.ChatMessage{summary, newFact})}}
				publication.ArchivePrevious = true
			}
			newer, err := sessiondb.PublishContext(s.db, publication)
			if err != nil {
				t.Fatal(err)
			}
			before, err := sessiondb.LoadMessagesWithMeta(s.db)
			if err != nil || len(before) != 1 {
				t.Fatalf("transcript unexpectedly changed: %+v, error=%v", before, err)
			}
			path := s.dbPath
			s.Close()
			resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			current, found, err := sessiondb.LoadContextState(resumed.db, resumed.sessionID, resumed.sessionID)
			if err != nil || !found || current.Generation != newer.Generation || current.Reason != newer.Reason || !reflect.DeepEqual(current.Messages, newer.Messages) || !reflect.DeepEqual(resumed.activeContext, newer.Messages) {
				t.Fatalf("stale checkpoint replaced committed context: got=%+v want=%+v found=%v error=%v", current, newer, found, err)
			}
			after, err := sessiondb.LoadMessagesWithMeta(resumed.db)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("passive resume restamped transcript: got=%+v want=%+v error=%v", after, before, err)
			}
			retained := resumed.pendingRecoveries["old-root"]
			if retained.ContextRevision != current.Revision || retained.ContextGeneration != newer.Generation || !reflect.DeepEqual(retained.ActiveContext, newer.Messages) || !reflect.DeepEqual(retained.Archive, archive) {
				t.Fatalf("recovery envelope did not retain exact source ownership: %+v", retained)
			}
			if requests.Load() != 0 || resumed.SubAgentRunner() != nil {
				t.Fatal("passive resume started execution")
			}
			if mutation == "compaction" {
				var archived []byte
				if err := resumed.db.QueryRow(`SELECT messages_json FROM agent_context_generations WHERE session_id=? AND agent_id=? AND generation=1`, resumed.sessionID, resumed.sessionID).Scan(&archived); err != nil {
					t.Fatal(err)
				}
				var messages []provider.ChatMessage
				if err := json.Unmarshal(archived, &messages); err != nil || !reflect.DeepEqual(messages, archive) {
					t.Fatalf("archived generation changed: %+v, error=%v", messages, err)
				}
			}
		})
	}
}

func TestPassiveRootResumeRejectsIdentityAndTranscriptConflicts(t *testing.T) {
	for _, corrupt := range []string{"preparation", "replay-field", "missing-identity", "transcript-projection", "duplicate-identity"} {
		t.Run(corrupt, func(t *testing.T) {
			home, directory := t.TempDir(), filepath.Join(t.TempDir(), "sessions")
			cfg := testConfig()
			s, err := NewWithSessionDir("immutable-archive", cfg, home, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			input := provider.NewTextMessage("user", "accepted input")
			input.InternalID = "input"
			preparation := provider.NewTextMessage("user", "captured preparation")
			preparation.InternalID = "input-context"
			preparation.Origin, preparation.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
			answer := provider.NewTextMessage("assistant", "accepted answer")
			answer.InternalID, answer.ReplayScope = "answer", "immutable replay scope"
			archive := canonicalContextMessages([]provider.ChatMessage{input, preparation, answer})
			checkpoint := sessiondb.RecoveryCheckpoint{
				Status:  recoverytypes.Status{ID: "incident", AgentID: "old-root", Generation: 1, State: recoverytypes.Interrupted, StartedAt: time.Now()},
				Archive: archive, ActiveContext: archive, ContextOwnerID: s.sessionID,
			}
			initial, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{
				SessionID: s.sessionID, AgentID: s.sessionID, RuntimeAgentID: "old-root", Recovery: &checkpoint,
				Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "accepted", Messages: archive}},
				TranscriptAppend: []sessiondb.ContextMessageDelta{
					contextDelta(input, sessiondb.DisplayMeta{DisplayRole: "user"}, 1, time.Now(), "old-root"),
					contextDelta(answer, sessiondb.DisplayMeta{DisplayRole: "assistant"}, 1, time.Now(), "old-root"),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			newer, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{
				SessionID: s.sessionID, AgentID: s.sessionID, ExpectedRevision: initial.Revision,
				Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 2, Reason: "compacted", Messages: nil}}, ArchivePrevious: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			switch corrupt {
			case "preparation":
				archive[1].Content = provider.TextContent("changed selected bytes")
			case "replay-field":
				archive[2].ReplayScope = "changed field absent from transcript"
			case "missing-identity":
				archive[1].InternalID = "unregistered-context"
			case "transcript-projection":
				if _, err := s.db.Exec(`UPDATE messages SET content='changed persisted text' WHERE internal_id='input'`); err != nil {
					t.Fatal(err)
				}
			case "duplicate-identity":
				archive = append(archive, archive[1])
			}
			raw, err := json.Marshal(archive)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE agent_recoveries SET archive_json=? WHERE incident_id='incident'`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DELETE FROM agent_recovery_tail WHERE incident_id='incident' AND field='archive'`); err != nil {
				t.Fatal(err)
			}
			path, sessionID := s.dbPath, s.sessionID
			s.Close()
			resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
			if resumed != nil {
				resumed.Close()
			}
			var conflict *sessiondb.ContextMessageConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("changed full identity was not rejected: %v", err)
			}
			db, err := sessiondb.OpenAndMigrate(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			current, found, err := sessiondb.LoadContextState(db, sessionID, sessionID)
			if err != nil || !found || !reflect.DeepEqual(current, newer) {
				t.Fatalf("failed resume changed authoritative context: got=%+v want=%+v error=%v", current, newer, err)
			}
		})
	}
}

func TestPassiveRootResumeKeepsRepeatedAnonymousOutputDistinct(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	input := provider.NewTextMessage("user", "accepted input")
	input.InternalID = "input"
	answer := provider.NewTextMessage("assistant", "same accepted answer")
	answer.InternalID = "earlier-answer"
	initial, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: "session", AgentID: "session",
		Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{
			contextDelta(input, sessiondb.DisplayMeta{DisplayRole: "user"}, 1, time.Now(), "root"),
			contextDelta(answer, sessiondb.DisplayMeta{DisplayRole: "assistant"}, 1, time.Now(), "root"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	later := canonicalContextMessage(answer)
	later.InternalID = ""
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:  recoverytypes.Status{ID: "incident", AgentID: "root", Generation: 2, State: recoverytypes.Interrupted, StartedAt: time.Now()},
		Archive: canonicalContextMessages([]provider.ChatMessage{input, answer, later}), ActiveContext: canonicalContextMessages([]provider.ChatMessage{answer, later}), ContextOwnerID: "session",
	}
	current, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: "session", AgentID: "session", RuntimeAgentID: "root", ExpectedRevision: initial.Revision,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 2, Reason: "compacted_run", Messages: checkpoint.ActiveContext}}, Recovery: &checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err = sessiondb.LoadRecoveryCheckpoint(db, "session", "root")
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{db: db, sessionID: "session", contextRevision: current.Revision, activeContext: current.Messages, turnNumber: 2}
	if err := s.restoreRecoveryTranscript(&checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.ActiveContext) != 2 || checkpoint.ActiveContext[0].InternalID != answer.InternalID || checkpoint.ActiveContext[1].InternalID == "" || checkpoint.ActiveContext[1].InternalID == answer.InternalID || checkpoint.ActiveContext[1].InternalID != checkpoint.Archive[2].InternalID {
		t.Fatalf("repeated output reused an earlier immutable ID: %+v", checkpoint)
	}
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || len(rows) != 3 || rows[2].Msg.InternalID != checkpoint.ActiveContext[1].InternalID {
		t.Fatalf("repeated output was not durably mirrored exactly once: %+v, error=%v", rows, err)
	}
}
