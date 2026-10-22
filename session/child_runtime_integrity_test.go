package session

import (
	"bytes"
	"context"
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

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	recoverytypes "crude/types/recovery"
)

func TestChildTerminalPublicationCommitsContextTranscriptAndRecoveryTogether(t *testing.T) {
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "child"); err != nil {
		t.Fatal(err)
	}
	handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{
		ID: "child", Provider: "provider", AccountID: "account", Model: "model",
	}}
	assignment := provider.NewTextMessage("user", "keep the original assignment")
	assistant := provider.NewTextMessage("assistant", "accepted partial work")
	handle.DB = db
	archive, active, deltas, err := childTerminalPublication(handle,
		[]provider.ChatMessage{assignment, assistant}, canonicalContextMessages([]provider.ChatMessage{assignment, assistant}), time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if archive[0].InternalID == "" || archive[1].InternalID == "" ||
		active[0].InternalID != archive[0].InternalID || active[1].InternalID != archive[1].InternalID {
		t.Fatalf("publication identities are not stable: archive=%+v active=%+v", archive, active)
	}
	checkpoint := sessiondb.RecoveryCheckpoint{
		Status:        recoverytypes.Status{ID: "incident", AgentID: "child", Generation: 1, State: recoverytypes.NeedsAttention},
		PendingInput:  provider.UserInput{InternalID: "assignment-child", Text: "keep the original assignment"},
		InputAdmitted: true, Archive: archive, ActiveContext: active,
	}
	state, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: "child", AgentID: "child", RuntimeAgentID: "child",
		Mutation:         sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "child_terminal", Messages: active}},
		TranscriptAppend: deltas, Recovery: &checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := sessiondb.LoadRecoveryCheckpoint(db, "child", "child")
	if err != nil || !ok {
		t.Fatalf("LoadRecoveryCheckpoint: ok=%v err=%v", ok, err)
	}
	if loaded.ContextRevision != state.Revision || loaded.ContextOwnerID != "child" ||
		len(loaded.ActiveContext) != 2 || loaded.ActiveContext[1].InternalID != archive[1].InternalID {
		t.Fatalf("recovery did not reference committed context: %+v state=%+v", loaded, state)
	}
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Msg.InternalID != archive[0].InternalID || rows[1].Msg.InternalID != archive[1].InternalID {
		t.Fatalf("transcript tail = %+v", rows)
	}
	if len(rows[1].Meta.Snapshots) == 0 || rows[1].Meta.Snapshots[0].Time.UnixNano() != time.Unix(100, 1).UnixNano() {
		t.Fatalf("canonical metadata timestamp was not source-stable: %+v", rows[1].Meta)
	}
	continued := provider.NewTextMessage("assistant", "continued after explicit repair")
	continuedArchive, continuedActive, continuedDeltas, err := childTerminalPublication(handle,
		append(append([]provider.ChatMessage(nil), archive...), continued), append(append([]provider.ChatMessage(nil), active...), continued), time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(continuedDeltas) != 1 || continuedDeltas[0].Message.InternalID == "" {
		t.Fatalf("explicit Continue should append only its new tail: %+v", continuedDeltas)
	}
	checkpoint.Archive, checkpoint.ActiveContext = continuedArchive, continuedActive
	replay, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: "child", AgentID: "child", RuntimeAgentID: "child", ExpectedRevision: state.Revision,
		Mutation:         sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "child_terminal", Messages: continuedActive}},
		TranscriptAppend: continuedDeltas, Recovery: &checkpoint,
	})
	if err != nil || replay.Revision != state.Revision+1 {
		t.Fatalf("exact retry lost stable publication identity: state=%+v err=%v", replay, err)
	}
	rows, err = sessiondb.LoadMessagesWithMeta(db)
	if err != nil || len(rows) != 3 {
		t.Fatalf("Continue rewrote or duplicated transcript: rows=%d err=%v", len(rows), err)
	}
	if rows[0].CreatedAt != time.Unix(100, 0).UnixNano() || rows[1].CreatedAt != time.Unix(100, 1).UnixNano() ||
		rows[0].Meta.DisplayRole != "assignment" || rows[2].Meta.DisplayRole == "assignment" {
		t.Fatalf("Continue relabeled or restamped accepted prefix: %+v", rows)
	}
}

type parkedChildFixture struct {
	session    *Session
	cfg        *Config
	home       string
	sessions   string
	childID    string
	childPath  string
	checkpoint sessiondb.RecoveryCheckpoint
	requests   atomic.Int32
	effects    atomic.Int32
}

func TestChildCommittedContextAdoptionPreservesAcceptedOutputAndOrder(t *testing.T) {
	message := func(id, role string) provider.ChatMessage {
		m := provider.NewTextMessage(role, id)
		m.InternalID = id
		return canonicalContextMessage(m)
	}
	first, accepted, anchor := message("assignment", "user"), message("accepted-output", "assistant"), message("anchor", "user")
	note, late := message("new-note", "user"), message("late-steering", "user")
	run := newRecoveryRunState(provider.UserInput{}, &sessiondb.RecoveryCheckpoint{
		Archive: []provider.ChatMessage{first, accepted, anchor}, ActiveContext: []provider.ChatMessage{first, accepted, anchor},
		ContextOwnerID: "child", ContextRevision: 2, ContextGeneration: 1, ContextReason: "run_checkpoint",
	})
	committed := sessiondb.ContextState{AgentID: "child", Revision: 3, Generation: 1, Reason: "run_checkpoint", Messages: []provider.ChatMessage{first, note, anchor, late}}
	if err := reconcileCommittedRecoveryContext(run, committed); err != nil {
		t.Fatal(err)
	}
	archive, active := run.contexts()
	if !reflect.DeepEqual(active, []provider.ChatMessage{first, accepted, note, anchor, late}) || !reflect.DeepEqual(archive, []provider.ChatMessage{first, accepted, note, anchor, late}) {
		t.Fatalf("child reconciliation lost facts or order: active=%+v archive=%+v", active, archive)
	}
	if run.contextRevisionValue() != committed.Revision || run.sourceRevision == run.publishedRevision {
		t.Fatal("accepted output was falsely acknowledged by the receipt publication")
	}
	// A receipt-only publication advances the exact revision without dirtying
	// identical source bytes; immediate compaction must use that new revision.
	committed.Revision, committed.Messages = 4, active
	if err := reconcileCommittedRecoveryContext(run, committed); err != nil {
		t.Fatal(err)
	}
	if run.contextRevisionValue() != 4 || run.sourceRevision != run.publishedRevision {
		t.Fatal("receipt-only child publication left stale recovery authority")
	}
}

func TestChildCommittedContextAdoptionRejectsChangedOrReorderedSource(t *testing.T) {
	first, second := provider.NewTextMessage("user", "first"), provider.NewTextMessage("assistant", "second")
	first.InternalID, second.InternalID = "first", "second"
	for _, outcome := range []string{"changed", "reordered", "generation"} {
		t.Run(outcome, func(t *testing.T) {
			run := newRecoveryRunState(provider.UserInput{}, &sessiondb.RecoveryCheckpoint{
				Archive: []provider.ChatMessage{first, second}, ActiveContext: canonicalContextMessages([]provider.ChatMessage{first, second}),
				ContextOwnerID: "child", ContextRevision: 2, ContextGeneration: 1, ContextReason: "run_checkpoint",
			})
			before := run.checkpoint(recoverytypes.Status{})
			source := sessiondb.ContextState{AgentID: "child", Revision: 3, Generation: 1, Messages: canonicalContextMessages([]provider.ChatMessage{first, second})}
			switch outcome {
			case "changed":
				source.Messages[1].Content = provider.TextContent("different accepted bytes")
			case "reordered":
				source.Messages[0], source.Messages[1] = source.Messages[1], source.Messages[0]
			case "generation":
				source.Generation = 2
			}
			if err := reconcileCommittedRecoveryContext(run, source); err == nil {
				t.Fatal("child reconciliation approved an ambiguous committed source")
			}
			if !reflect.DeepEqual(before, run.checkpoint(recoverytypes.Status{})) {
				t.Fatal("failed child reconciliation mutated its owned source")
			}
		})
	}
}

func TestChildRuntimeReceiptImmediatelyPreparesCompactionFromExactSource(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if calls.Add(1) != 1 {
			t.Error("child receipt/compaction boundary requested another stochastic answer")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		close(entered)
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted before peer receipt\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	cfg := modelAttributionConfig(server.URL, "child-notice-compaction")
	threshold := 20
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryResearcher] = CategoryConfig{
		AccountID: "account", Model: "child-notice-compaction", ModelContextWindow: 64_000, ContextCompactionPercent: &threshold,
	}
	profile.Categories[CategoryAeron] = CategoryConfig{AccountID: "account", Model: "child-compactor", ModelContextWindow: 64_000}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	store := attachDelegationTestCollaboration(t, s)
	runner := NewSubAgentRunner(s, 1)
	s.subAgentSup = runner
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	result, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "retain peer notice before compaction", "isolation": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &identity); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real child provider request did not start")
	}
	handle, exists := runner.Manager().GetActive(agent.AgentID(identity.AgentID))
	if !exists {
		t.Fatal("actual child handle is unavailable")
	}
	prepared := make(chan error, 1)
	original := handle.Agent.PrepareCompaction
	handle.Agent.PrepareCompaction = func(source agent.CompactionSource) error {
		before, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, identity.AgentID)
		if err == nil && !found {
			err = errors.New("child receipt source is unavailable")
		}
		if err == nil {
			err = original(source)
		}
		if err == nil {
			var after sessiondb.ContextState
			after, found, err = sessiondb.LoadContextState(handle.DB, handle.SessionID, identity.AgentID)
			if err == nil && (!found || after.Revision != before.Revision+1 || after.Reason != "compaction_source" || !reflect.DeepEqual(after.Messages, canonicalContextMessages(source.Messages))) {
				err = errors.New("child compaction did not publish the exact admitted source and revision")
			}
		}
		prepared <- err
		handle.Cancel()
		return err
	}
	peer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "compaction-peer", Name: "Peer", SessionID: "peer", OwnerInstance: "peer"})
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: peer.ID, RecipientIDs: []string{identity.AgentID}, Origin: collaboration.OriginPeer,
		Kind: collaboration.EventPeerNote, IdempotencyKey: "large-child-notice", Body: strings.Repeat("complete peer evidence ", 2_700),
	})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-prepared:
		if err != nil {
			t.Fatalf("receipt publication left stale compaction source revision: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not reach immediate post-receipt compaction")
	}
	waitSessionSupervisedJourney(t, "cancelled child compaction settlement", runner.Wait)
	db, err := sessiondb.OpenAndMigrate(s.completedSubAgentDBPath(identity.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	state, found, err := sessiondb.LoadContextState(db, identity.AgentID, identity.AgentID)
	if err != nil || !found {
		t.Fatalf("load settled child authority: found=%v err=%v", found, err)
	}
	counts := make(map[string]int)
	for _, message := range state.Messages {
		counts[message.InternalID]++
		if message.InternalID == note.Event.ID && message.ContentString() != collaborationMessage(collaboration.PendingDelivery{Event: note.Event}).ContentString() {
			t.Fatal("compaction admission truncated or replaced peer evidence")
		}
	}
	if counts[note.Event.ID] != 1 || calls.Load() != 1 {
		t.Fatalf("receipt/compaction lost or replayed work: note=%d calls=%d", counts[note.Event.ID], calls.Load())
	}
	var admissions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collaboration_admissions WHERE agent_id=? AND event_id=?`, identity.AgentID, note.Event.ID).Scan(&admissions); err != nil || admissions != 1 {
		t.Fatalf("child receipt was not durably committed once: count=%d err=%v", admissions, err)
	}
}

func TestChildAdmissionPublisherReturnsExactReceiptAndNoOpState(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	store := attachDelegationTestCollaboration(t, s)
	actor, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: f.childID, Name: "Child", SessionID: f.childID, OwnerInstance: "child-receipt-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{
		ID: "receipt-peer", Name: "Peer", SessionID: "peer", OwnerInstance: "peer-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: peer.ID, RecipientIDs: []string{actor.ID}, Origin: collaboration.OriginPeer,
		Kind: collaboration.EventPeerNote, IdempotencyKey: "receipt-only", Body: "exact peer evidence",
	}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: actor.ID, RuntimeEpoch: actor.RuntimeEpoch, Limit: 1})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("reserve exact child receipt: deliveries=%d err=%v", len(deliveries), err)
	}
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	state, _, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil {
		t.Fatal(err)
	}
	// The notice is already checkpointed; only its receipt advances the revision.
	state, err = sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: f.childID, AgentID: f.childID, ExpectedRevision: state.Revision,
		Mutation: sessiondb.ContextMutation{Append: collaborationDeltas(deliveries, []provider.ChatMessage{collaborationMessage(deliveries[0])}, f.childID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := s.commitCollaborationAdmissions(db, f.childID, f.childID, actor.ID, "", actor.RuntimeEpoch, state.Revision, nil, deliveries)
	if err != nil || committed.Revision != state.Revision+1 || !reflect.DeepEqual(committed.Messages, state.Messages) {
		t.Fatalf("receipt-only publisher returned stale or fabricated source: state=%+v committed=%+v err=%v", state, committed, err)
	}
	loaded, found, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || !found || !reflect.DeepEqual(committed, loaded) {
		t.Fatalf("child caller did not receive the exact committed source: found=%v err=%v", found, err)
	}
	unchanged, err := s.commitCollaborationAdmissions(db, f.childID, f.childID, actor.ID, "", actor.RuntimeEpoch, committed.Revision, nil, nil)
	if err != nil || !reflect.DeepEqual(unchanged, committed) {
		t.Fatalf("no-op publisher fabricated a zero source or advanced revision: state=%+v err=%v", unchanged, err)
	}
	if _, err := s.commitCollaborationAdmissions(db, f.childID, f.childID, actor.ID, "", actor.RuntimeEpoch, state.Revision, nil, nil); err == nil {
		t.Fatal("no-op publisher accepted a stale source revision")
	} else {
		var conflict *sessiondb.ContextRevisionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("stale no-op returned the wrong error: %v", err)
		}
	}
	if _, err := s.commitCollaborationAdmissions(db, f.childID, "missing-owner", actor.ID, "", actor.RuntimeEpoch, 0, nil, nil); err == nil {
		t.Fatal("missing no-op owner fabricated a successful zero source")
	}
	if f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("child receipt publication restarted parked work")
	}
}

func newParkedChildFixture(t *testing.T) *parkedChildFixture {
	t.Helper()
	f := &parkedChildFixture{home: t.TempDir()}
	f.sessions = filepath.Join(f.home, "sessions")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	f.cfg = modelAttributionConfig(server.URL, "child-stop-model")
	profile := f.cfg.Agents.Profiles[f.cfg.Agents.ActiveProfile]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "child-stop-model"}
	f.cfg.Agents.Profiles[f.cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("child-stop-parent", f.cfg, f.home, f.sessions)
	if err != nil {
		t.Fatal(err)
	}
	f.session = s
	t.Cleanup(s.Close)
	project := t.TempDir()
	s.SetWorkDir(project)
	// A real unreadable guidance source parks the child before provider or tools.
	if err := os.Mkdir(filepath.Join(project, "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	// Keep the owning root as a saved session without executing a root turn.
	root := provider.NewTextMessage("user", "retain this parent and its child review")
	root.InternalID = "child-stop-parent-input"
	stamp := time.Unix(100, 0)
	meta := canonicalizeDisplayMeta(root, sessiondb.DisplayMeta{DisplayRole: "user"}, "", 0, stamp)
	if _, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{
		Append: []sessiondb.ContextMessageDelta{contextDelta(root, meta, 1, stamp, "")},
	}}); err != nil {
		t.Fatal(err)
	}
	s.msgs, s.msgTurns, s.displayMetas, s.createdAts = []provider.ChatMessage{root}, []int{1}, []sessiondb.DisplayMeta{meta}, []int64{stamp.UnixNano()}
	runner := NewSubAgentRunner(s, 1)
	s.subAgentSup = runner
	runner.SetParentToolFn(func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		f.effects.Add(1)
		return tools.ToolResult{}, nil
	})
	result, err := runner.StartAgent(t.Context(), map[string]any{
		"category": CategoryResearcher, "description": "preserve the exact original child assignment", "isolation": "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
		t.Fatalf("child launch identity = %s, err=%v", result.Content, err)
	}
	f.childID = identity.AgentID
	settled := make(chan struct{})
	go func() { runner.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("unreadable guidance did not park the real child")
	}
	s.recoveryMu.Lock()
	f.checkpoint = s.pendingRecoveries[f.childID]
	s.recoveryMu.Unlock()
	if f.checkpoint.Status.State != recoverytypes.NeedsAttention || len(f.checkpoint.Archive) == 0 || runner.Manager().ActiveCount() != 0 {
		t.Fatalf("child did not retain an actionable parked task: %+v", f.checkpoint)
	}
	f.childPath = s.completedSubAgentDBPath(f.childID)
	if f.childPath == "" || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatalf("parked child path=%q requests=%d effects=%d", f.childPath, f.requests.Load(), f.effects.Load())
	}
	_, rows, _, err := loadSubAgentSnapshotDB(f.childPath, f.childID)
	if err != nil || len(rows) != 1 || rows[0].Msg.InternalID != f.checkpoint.PendingInput.InternalID || rows[0].Meta.DisplayRole != "assignment" {
		t.Fatalf("real parked child changed its original assignment identity: rows=%+v err=%v", rows, err)
	}
	return f
}

func TestChildParkedStopPreservesNewerContextAndPassiveResume(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	current, found, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || !found {
		t.Fatalf("load parked child authority: found=%v err=%v", found, err)
	}
	newer := provider.NewTextMessage("user", "a newer committed fact must survive child Stop")
	newer.InternalID, newer.Origin = "newer-child-fact", provider.OriginHumanSteering
	stamp := time.Unix(200, 0)
	meta := canonicalizeDisplayMeta(newer, sessiondb.DisplayMeta{DisplayRole: "user"}, "", 1, stamp)
	current, err = sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: f.childID, AgentID: f.childID, ExpectedRevision: current.Revision,
		Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{contextDelta(newer, meta, 1, stamp, f.childID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision <= f.checkpoint.ContextRevision {
		t.Fatal("fixture did not advance beyond the parked checkpoint")
	}
	status := f.checkpoint.Status
	if s.CancelRecoveryFor(f.childID, "foreign-incident", status.Generation) == nil || s.CancelRecoveryFor(f.childID, status.ID, status.Generation+1) == nil {
		t.Fatal("stale child Stop settled the exact task")
	}
	if s.CancelRecoveryFor(f.childID, status.ID, status.Generation) != nil {
		t.Fatal("exact parked child Stop was rejected")
	}
	after, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || len(after) != len(before)+1 || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatalf("Stop rewrote immutable child evidence: before=%+v after=%+v err=%v", before, after, err)
	}
	stopped, found, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || !found || len(stopped.Messages) != len(current.Messages)+1 || !reflect.DeepEqual(current.Messages, stopped.Messages[:len(current.Messages)]) {
		t.Fatalf("Stop replaced newer context with stale bytes: current=%+v stopped=%+v err=%v", current, stopped, err)
	}
	if stopped.Messages[len(stopped.Messages)-1].InternalID != "turn-stopped:"+status.ID || after[len(after)-1].Meta.Snapshots[0].LifecyclePhase != "stopped" {
		t.Fatal("child Stop has no durable visible and context boundary")
	}
	if checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, f.childID, f.childID); err != nil || open {
		t.Fatalf("stopped child still has an unfinished incident: %+v open=%v err=%v", checkpoint, open, err)
	}
	var mirrorSession, mirrorAgent string
	if err := s.db.QueryRow(`SELECT session_id,agent_id FROM agent_recoveries WHERE incident_id=?`, status.ID).Scan(&mirrorSession, &mirrorAgent); err != nil {
		t.Fatal(err)
	}
	mirror, found, err := sessiondb.LoadRecoveryIncident(s.db, mirrorSession, mirrorAgent, status.ID)
	if err != nil || !found {
		t.Fatalf("load parent mirror: found=%v err=%v", found, err)
	}
	mirroredArchive, mirrorRevision := mirror.Archive, mirror.ContextRevision
	counts := make(map[string]int)
	for _, message := range mirroredArchive {
		counts[message.InternalID]++
	}
	if mirrorRevision != stopped.Revision || counts[f.checkpoint.PendingInput.InternalID] != 1 || counts[newer.InternalID] != 1 || counts["turn-stopped:"+status.ID] != 1 {
		t.Fatalf("parent mirror lost or duplicated the child's exact facts: counts=%v revision=%d want=%d", counts, mirrorRevision, stopped.Revision)
	}
	if s.CancelRecoveryFor(f.childID, status.ID, status.Generation) == nil || s.RetryRecoveryFor(f.childID, status.ID, status.Generation) == nil {
		t.Fatal("settled child incident remained executable")
	}
	path := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(path, f.cfg, f.home, f.sessions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	snapshots := resumed.CompletedSubAgentSnapshots()
	if len(snapshots) != 1 || snapshots[0].Result.AgentID != agent.AgentID(f.childID) || snapshots[0].Result.Status != agent.SubAgentCancelled || !reflect.DeepEqual(after, snapshots[0].Messages) {
		t.Fatalf("passive resume changed the child review evidence: %+v", snapshots)
	}
	if len(resumed.PendingSubAgentSnapshots()) != 0 || resumed.SubAgentRunner().Running() || resumed.SubAgentRunner().Manager().ActiveCount() != 0 || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatalf("passive resume executed stopped work: requests=%d effects=%d", f.requests.Load(), f.effects.Load())
	}
}

func TestChildParkedStopStorageFailureDoesNotRewriteTranscript(t *testing.T) {
	f := newParkedChildFixture(t)
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	before, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_child_stop BEFORE INSERT ON agent_recoveries
		WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT, 'child checkpoint unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	status := f.checkpoint.Status
	if f.session.CancelRecoveryFor(f.childID, status.ID, status.Generation) == nil {
		t.Fatal("child Stop acknowledged a failed checkpoint")
	}
	after, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed Stop changed immutable transcript bytes/metadata: before=%+v after=%+v err=%v", before, after, err)
	}
	state, _, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || !reflect.DeepEqual(current, state) {
		t.Fatalf("failed Stop changed current context: before=%+v after=%+v err=%v", current, state, err)
	}
	if checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, f.childID, f.childID); err != nil || !open || checkpoint.Status.State != recoverytypes.NeedsAttention {
		t.Fatalf("storage failure lost parked ownership: %+v open=%v err=%v", checkpoint, open, err)
	}
	if len(f.session.SubAgentRunner().Manager().CompletedResults()) != 0 {
		t.Fatal("storage failure fabricated a completed child")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_child_stop`); err != nil {
		t.Fatal(err)
	}
	if f.session.CancelRecoveryFor(f.childID, status.ID, status.Generation) != nil || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("checked Stop retry failed or executed work")
	}
}

func TestChildParkedStopPublishesUnmirroredAcceptedSnapshotOnce(t *testing.T) {
	f := newParkedChildFixture(t)
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	current, _, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := f.checkpoint
	checkpoint.ContextOwnerID = f.childID
	// Current checkpoints can retain output before terminal transcript IDs exist.
	// Identical text in two accepted messages is still two distinct source facts.
	accepted := provider.NewTextMessage("assistant", "accepted before transcript publication")
	checkpoint.Archive = append(checkpoint.Archive, accepted, accepted)
	checkpoint.ActiveContext = append(current.Messages, accepted, accepted)
	current, err = sessiondb.PublishContext(db, sessiondb.ContextPublication{
		SessionID: f.childID, AgentID: f.childID, RuntimeAgentID: f.childID, ExpectedRevision: current.Revision,
		Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{
			Generation: current.Generation, Reason: "accepted_child_snapshot", Messages: checkpoint.ActiveContext,
		}}, Recovery: &checkpoint,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := checkpoint.Status
	if f.session.CancelRecoveryFor(f.childID, status.ID, status.Generation) != nil {
		t.Fatal("Stop failed to reconcile the child's newer accepted checkpoint")
	}
	stopped, _, err := sessiondb.LoadContextState(db, f.childID, f.childID)
	if err != nil || len(stopped.Messages) != len(current.Messages)+1 {
		t.Fatalf("Stop duplicated accepted snapshot facts: messages=%d err=%v", len(stopped.Messages), err)
	}
	ids := make(map[string]bool)
	for _, message := range stopped.Messages {
		if message.InternalID == "" || ids[message.InternalID] {
			t.Fatalf("accepted snapshot has missing or duplicate publication identity: %q", message.InternalID)
		}
		ids[message.InternalID] = true
	}
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || len(rows) != 4 || rows[1].Msg.ContentString() != accepted.ContentString() || rows[2].Msg.ContentString() != accepted.ContentString() || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatalf("Stop lost, duplicated, or executed unmirrored accepted work: rows=%d err=%v requests=%d effects=%d", len(rows), err, f.requests.Load(), f.effects.Load())
	}
}

func TestChildParkedStopRejectsObsoleteSchemaWithoutRecreatingEvidence(t *testing.T) {
	f := newParkedChildFixture(t)
	db, err := sessiondb.OpenAndMigrate(f.childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	before, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE immutable_message_identities`); err != nil {
		t.Fatal(err)
	}
	status := f.checkpoint.Status
	if f.session.CancelRecoveryFor(f.childID, status.ID, status.Generation) == nil {
		t.Fatal("child Stop accepted an obsolete persistence schema")
	}
	var recreated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='immutable_message_identities'`).Scan(&recreated); err != nil || recreated != 0 {
		t.Fatalf("Stop implicitly aligned the child schema: recreated=%d err=%v", recreated, err)
	}
	after, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil || !reflect.DeepEqual(before, after) || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatalf("obsolete-schema Stop changed evidence or executed work: err=%v requests=%d effects=%d", err, f.requests.Load(), f.effects.Load())
	}
}

func TestChildParkedStopRetriesParentMirrorFailureWithoutDuplicateBoundary(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	if _, err := s.db.Exec(`CREATE TRIGGER reject_child_stop_mirror BEFORE INSERT ON agent_recoveries
		WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT, 'parent mirror unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	status := f.checkpoint.Status
	if s.CancelRecoveryFor(f.childID, status.ID, status.Generation) == nil {
		t.Fatal("child Stop acknowledged a failed parent mirror")
	}
	_, before, _, err := loadSubAgentSnapshotDB(f.childPath, f.childID)
	if err != nil || len(before) != 2 || before[1].Meta.Snapshots[0].LifecyclePhase != "stopped" {
		t.Fatalf("child's durable half of Stop was not retained: rows=%+v err=%v", before, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_child_stop_mirror`); err != nil {
		t.Fatal(err)
	}
	if s.CancelRecoveryFor(f.childID, status.ID, status.Generation) != nil {
		t.Fatal("exact checked Stop retry could not finish its parent mirror")
	}
	_, after, _, err := loadSubAgentSnapshotDB(f.childPath, f.childID)
	if err != nil || !reflect.DeepEqual(before, after) || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatalf("Stop retry rewrote evidence or replayed work: err=%v requests=%d effects=%d", err, f.requests.Load(), f.effects.Load())
	}
}

func TestChildActiveStopPreservesAcceptedWorkAndPassiveResume(t *testing.T) {
	checkChildActiveStop(t, false, false)
}

func TestChildActiveStopStorageFailureParksWithoutAcknowledgingLostTranscript(t *testing.T) {
	checkChildActiveStop(t, true, false)
}

func TestChildPostCaptureQueuedSteeringPreservesAcceptedOutputAndPassiveResume(t *testing.T) {
	checkChildActiveStop(t, false, true)
}

func waitTerminalSettlementAttention(t *testing.T, sender *mockEventSender, agentID string) recoverytypes.Status {
	t.Helper()
	timeout := time.NewTimer(4 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-sender.ch:
			if event.agentID != agentID {
				continue
			}
			switch event.typ {
			case "done", "sub_agent_completed", "sub_agent_done":
				t.Fatalf("failed terminal settlement acknowledged %s before storage repair", event.typ)
			case "recovery":
				if status, ok := event.data.(recoverytypes.Status); ok && status.State == recoverytypes.NeedsAttention {
					return status
				}
			}
		case <-timeout.C:
			t.Fatal("terminal settlement did not retain actionable attention")
		}
	}
}

func TestChildStopDuringFailedTerminalSettlementRetriesOnlyPersistence(t *testing.T) {
	for _, failure := range []string{"parent-result", "child-ledger", "collaboration", "outcome", "parent-mirror", "parent-result-restart"} {
		t.Run(failure, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			var requests, effects, acknowledgements atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			var handle *agent.SubAgentHandle
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				switch requests.Add(1) {
				case 1:
					close(entered)
					<-release
					_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"settlement-read","type":"function","function":{"name":"list_directory","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
				case 2:
					runID, _ := handle.RunIdentity()
					args, err := json.Marshal(map[string]any{
						"recipient_ids": []string{string(handle.Spec.ParentID)}, "run_id": runID, "kind": "final",
						"summary": "child accepted report", "body": "The one directory read completed.",
					})
					if err != nil {
						t.Error(err)
					}
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"settlement-report\",\"type\":\"function\",\"function\":{\"name\":\"publish_report\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", args)
				case 3:
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"child accepted final output\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				default:
					t.Error("terminal persistence retry requested another child answer")
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			t.Cleanup(server.Close)
			cfg := modelAttributionConfig(server.URL, "child-settlement-"+failure)
			profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
			profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "child-settlement-" + failure}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
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
			s.SetCollaboration(store, "child-terminal-fixture")
			if _, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: s.sessionID, Name: "Parent", SessionID: s.sessionID, OwnerInstance: "settlement-parent"}); err != nil {
				t.Fatal(err)
			}
			baseSender := newMockEventSender()
			// Native child tools execute against their own root, not the parent
			// tool proxy. Observe the exact real read outcome to guard replay.
			sender := &sessionSupervisedJourneySender{mockEventSender: baseSender, observe: func(typ string, data any, _ string) {
				if typ == "tool_result" {
					result, _ := data.(map[string]any)
					if result["tool_id"] == "settlement-read" && result["name"] == "list_directory" && result["error"] != "true" && strings.Contains(fmt.Sprint(result["result"]), project) {
						effects.Add(1)
					}
				}
				if typ == "done" || typ == "sub_agent_completed" || typ == "sub_agent_done" {
					acknowledgements.Add(1)
				}
			}}
			runner := NewSubAgentRunner(s, 1)
			s.subAgentSup = runner
			runner.SetOrchestratorID(s.sessionID)
			runner.SetSender(sender)
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			result, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "retain this assignment through failed final settlement", "isolation": "shared"})
			if err != nil {
				t.Fatal(err)
			}
			var identity struct {
				AgentID string `json:"agent_id"`
			}
			if err := json.Unmarshal([]byte(result.Content), &identity); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(4 * time.Second):
				t.Fatal("real child provider request did not start")
			}
			var exists bool
			handle, exists = runner.Manager().GetActive(agent.AgentID(identity.AgentID))
			if !exists {
				t.Fatal("real child handle is unavailable")
			}
			faultDB := s.db
			trigger := `CREATE TRIGGER reject_terminal_settlement BEFORE INSERT ON sub_agent_results BEGIN SELECT RAISE(ABORT, 'parent result unavailable'); END`
			switch failure {
			case "child-ledger":
				faultDB = handle.DB
				trigger = `CREATE TRIGGER reject_terminal_settlement BEFORE UPDATE ON agent_turns WHEN NEW.state<>'running' BEGIN SELECT RAISE(ABORT, 'child ledger unavailable'); END`
			case "collaboration":
				trigger = `CREATE TRIGGER reject_terminal_settlement BEFORE UPDATE ON collaboration_runs WHEN NEW.agent_id='` + identity.AgentID + `' AND NEW.state<>'running' BEGIN SELECT RAISE(ABORT, 'child collaboration unavailable'); END`
			case "outcome":
				trigger = `CREATE TRIGGER reject_terminal_settlement BEFORE INSERT ON collaboration_events WHEN NEW.kind='run_outcome' BEGIN SELECT RAISE(ABORT, 'child outcome unavailable'); END`
			case "parent-mirror":
				trigger = `CREATE TRIGGER reject_terminal_settlement BEFORE DELETE ON agent_recoveries BEGIN SELECT RAISE(ABORT, 'child terminal mirror unavailable'); END;
					CREATE TRIGGER reject_terminal_mirror BEFORE INSERT ON agent_recoveries WHEN NEW.state IN ('recovered','cancelled') BEGIN SELECT RAISE(ABORT, 'child terminal mirror unavailable'); END`
			}
			if _, err := faultDB.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = faultDB.Exec(`DROP TRIGGER IF EXISTS reject_terminal_settlement`)
				_, _ = faultDB.Exec(`DROP TRIGGER IF EXISTS reject_terminal_mirror`)
				select {
				case <-release:
				default:
					close(release)
				}
			})
			close(release)
			first := waitTerminalSettlementAttention(t, baseSender, identity.AgentID)
			before, err := sessiondb.LoadMessagesWithMeta(handle.DB)
			if err != nil || !slices.ContainsFunc(before, func(row sessiondb.MessageWithMeta) bool {
				return row.Msg.ContentString() == "child accepted final output"
			}) {
				t.Fatalf("fault did not occur after successful terminal context publication: rows=%+v err=%v", before, err)
			}
			runID, _ := handle.RunIdentity()
			if s.CancelRecoveryFor(identity.AgentID, first.ID, first.Generation) != nil {
				t.Fatal("exact child Stop was rejected")
			}
			blocked := waitTerminalSettlementAttention(t, baseSender, identity.AgentID)
			if blocked.ID != first.ID || blocked.Generation != first.Generation || acknowledgements.Load() != 0 || len(runner.Manager().CompletedResults()) != 0 {
				t.Fatal("Stop discarded failed child terminal ownership or acknowledged missing persistence")
			}
			checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(handle.DB, identity.AgentID, identity.AgentID)
			if err != nil || !open || checkpoint.Status.State != recoverytypes.NeedsAttention || checkpoint.Status.Generation != first.Generation {
				t.Fatalf("failed stopped child has no exact open envelope: open=%v checkpoint=%+v err=%v", open, checkpoint, err)
			}
			if failure == "parent-result-restart" {
				captured, err := terminalIntent(checkpoint)
				if err != nil || captured == nil || captured.Child == nil || captured.Child.Status != agent.SubAgentCompleted || captured.Child.ReportID == "" || captured.Run.RunID != runID || !captured.Stopped {
					t.Fatalf("child restart has no typed captured terminal provenance: intent=%+v err=%v", captured, err)
				}
				path := s.dbPath
				s.Close()
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = ResumeSessionWithDir(path, cfg, home, filepath.Join(home, "sessions"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				s.SetWorkDir(project)
				store, err = collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				s.SetCollaboration(store, "child-terminal-restart")
				runner = s.SubAgentRunner()
				runner.SetSender(sender)
				faultDB = s.db
				if requests.Load() != 3 || effects.Load() != 1 || runner.Running() || acknowledgements.Load() != 0 {
					t.Fatal("passive child resume executed unfinished terminal work")
				}
				s.recoveryMu.Lock()
				resumed := s.pendingRecoveries[identity.AgentID]
				s.recoveryMu.Unlock()
				if !bytes.Equal(checkpoint.TerminalSettlement, resumed.TerminalSettlement) {
					t.Fatal("passive resume changed immutable terminal intent")
				}
			}
			if _, err := faultDB.Exec(`DROP TRIGGER reject_terminal_settlement`); err != nil {
				t.Fatal(err)
			}
			if _, err := faultDB.Exec(`DROP TRIGGER IF EXISTS reject_terminal_mirror`); err != nil {
				t.Fatal(err)
			}
			if s.RetryRecoveryFor(identity.AgentID, blocked.ID, blocked.Generation) != nil {
				t.Fatal("exact child persistence retry was rejected")
			}
			waitSessionSupervisedJourney(t, "child persistence-only settlement", runner.Wait)
			completed := s.CompletedSubAgentSnapshots()
			if len(completed) != 1 || completed[0].Result.Status != agent.SubAgentCompleted || completed[0].Result.ReportID == "" || requests.Load() != 3 || effects.Load() != 1 || acknowledgements.Load() != 2 {
				t.Fatalf("checked child finalization lost its result or replayed work: results=%+v requests=%d effects=%d acknowledgements=%d", completed, requests.Load(), effects.Load(), acknowledgements.Load())
			}
			if len(completed[0].Messages) != len(before)+1 || !reflect.DeepEqual(before, completed[0].Messages[:len(before)]) || completed[0].Messages[len(before)].Msg.EffectiveOrigin() != provider.OriginTurnStopped {
				t.Fatal("child persistence retry rewrote accepted metadata or duplicated its stop boundary")
			}
			settled, err := store.Run(t.Context(), runID)
			if err != nil || settled.State != collaboration.RunSucceeded {
				t.Fatalf("child run ledger changed its captured terminal outcome: %+v err=%v", settled, err)
			}
			var runs int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM collaboration_runs WHERE agent_id=?`, identity.AgentID).Scan(&runs); err != nil || runs != 1 {
				t.Fatalf("persistence retry created a replacement child run: runs=%d err=%v", runs, err)
			}
		})
	}
}

func TestCheckedTerminalSettlementPreservesSuccessfulStopAndPassiveResume(t *testing.T) {
	for _, owner := range []string{"root", "child"} {
		t.Run(owner, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			if owner == "child" {
				checkChildActiveStop(t, false, false)
				return
			}
			checkSessionSupervisedProtocolJourney(t, "openai", "root", "stop", sessionSupervisedProtocolFixtures()["openai"])
		})
	}
}

func checkChildActiveStop(t *testing.T, rejectTerminal, lateSteering bool) {
	t.Helper()
	var requests, effects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"accepted child work","reasoning_content":"accepted child reasoning","tool_calls":[{"index":0,"id":"child-read-once","type":"function","function":{"name":"list_directory","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		case 2:
			_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"accepted second partial"}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			<-request.Context().Done()
		default:
			t.Error("Stop or passive resume launched a replacement provider request")
		}
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	cfg := modelAttributionConfig(server.URL, "active-child-stop-model")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "active-child-stop-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	s, err := NewWithSessionDir("", cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	// Child reporting is a required runtime channel even when this journey stops
	// before publishing. Use the actual journal-backed product composition.
	store := attachDelegationTestCollaboration(t, s)
	root := provider.NewTextMessage("user", "retain the parent of an actively stopped child")
	root.InternalID = "active-child-stop-parent-input"
	stamp := time.Unix(100, 0)
	meta := canonicalizeDisplayMeta(root, sessiondb.DisplayMeta{DisplayRole: "user"}, "", 0, stamp)
	if _, err := s.publishRootContext(sessiondb.ContextPublication{Mutation: sessiondb.ContextMutation{
		Append: []sessiondb.ContextMessageDelta{contextDelta(root, meta, 1, stamp, "")},
	}}); err != nil {
		t.Fatal(err)
	}
	s.msgs, s.msgTurns, s.displayMetas, s.createdAts = []provider.ChatMessage{root}, []int{1}, []sessiondb.DisplayMeta{meta}, []int64{stamp.UnixNano()}
	// Count the native read's exact successful outcome, not an unused parent
	// proxy; the durable checkpoint below independently verifies its content.
	sender := &sessionSupervisedJourneySender{mockEventSender: newMockEventSender(), observe: func(typ string, data any, _ string) {
		if typ == "tool_result" {
			result, _ := data.(map[string]any)
			if result["tool_id"] == "child-read-once" && result["name"] == "list_directory" && result["error"] != "true" && strings.Contains(fmt.Sprint(result["result"]), s.WorkDir()) {
				effects.Add(1)
			}
		}
	}}
	runner := NewSubAgentRunner(s, 1)
	s.subAgentSup = runner
	runner.SetSender(sender)
	result, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "inspect once and retain accepted work on Stop", "isolation": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &identity); err != nil || identity.AgentID == "" {
		t.Fatalf("child launch = %s err=%v", result.Content, err)
	}
	timeout := time.After(5 * time.Second)
accepted:
	for {
		select {
		case event := <-sender.ch:
			if event.agentID == identity.AgentID && event.typ == "recovery" {
				if status, ok := event.data.(recoverytypes.Status); ok && status.State == recoverytypes.NeedsAttention {
					t.Fatalf("real child checkpoint parked before request two: %+v", status)
				}
			}
			if event.agentID == identity.AgentID && event.typ == "stream_token" && strings.Contains(fmt.Sprint(event.data), "accepted second partial") {
				break accepted
			}
		case <-timeout:
			t.Fatalf("real child did not reach its second accepted response: requests=%d effects=%d", requests.Load(), effects.Load())
		}
	}
	childPath := s.completedSubAgentDBPath(identity.AgentID)
	db, err := sessiondb.OpenAndMigrate(childPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, identity.AgentID, identity.AgentID)
	if err != nil || !open {
		t.Fatalf("active child checkpoint: open=%v err=%v", open, err)
	}
	acceptedWork, outcomes := 0, 0
	for _, message := range checkpoint.ActiveContext {
		if message.ContentString() == "accepted child work" && message.ReasoningContent == "accepted child reasoning" {
			acceptedWork++
		}
		if message.Role == "tool" && message.ToolCallID == "child-read-once" && !message.ToolError && strings.Contains(message.ContentString(), "Directory "+s.WorkDir()+" is empty") {
			outcomes++
		}
	}
	if acceptedWork != 1 || outcomes != 1 || effects.Load() != 1 {
		t.Fatalf("child safe boundary did not durably retain accepted work: accepted=%d outcomes=%d effects=%d", acceptedWork, outcomes, effects.Load())
	}
	if rejectTerminal {
		if _, err := db.Exec(`CREATE TRIGGER reject_child_terminal BEFORE INSERT ON messages
			WHEN NEW.origin='turn_stopped' BEGIN SELECT RAISE(ABORT, 'child transcript unavailable'); END`); err != nil {
			t.Fatal(err)
		}
	}
	var stoppedCheckpoint chan struct{}
	var releaseCheckpoint chan struct{}
	var expectedSteering provider.ChatMessage
	if lateSteering {
		handle, exists := runner.Manager().GetActive(agent.AgentID(identity.AgentID))
		if !exists {
			t.Fatal("actual child handle is unavailable")
		}
		stoppedCheckpoint, releaseCheckpoint = make(chan struct{}), make(chan struct{})
		original := handle.Agent.Checkpoint
		handle.Agent.Checkpoint = func(archive, active []provider.ChatMessage) error {
			err := original(archive, active)
			if len(active) > 0 && active[len(active)-1].EffectiveOrigin() == provider.OriginTurnStopped {
				close(stoppedCheckpoint)
				<-releaseCheckpoint
			}
			return err
		}
		t.Cleanup(func() {
			select {
			case <-releaseCheckpoint:
			default:
				close(releaseCheckpoint)
			}
		})
	}
	if s.CancelRecoveryFor(identity.AgentID, checkpoint.Status.ID, checkpoint.Status.Generation) != nil {
		t.Fatal("exact active child Stop was rejected")
	}
	if lateSteering {
		select {
		case <-stoppedCheckpoint:
		case <-time.After(5 * time.Second):
			t.Fatal("real stopped child did not publish its captured boundary")
		}
		handle, _ := runner.Manager().GetActive(agent.AgentID(identity.AgentID))
		runID, _ := handle.RunIdentity()
		published, err := store.Publish(t.Context(), collaboration.PublishRequest{
			SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
			Kind: collaboration.EventHumanSteering, IdempotencyKey: "postcapture-child-steering",
			RecipientIDs: []string{identity.AgentID}, RunID: runID, Body: "retain this late genuine human steering exactly once",
			References: []collaboration.Reference{{Kind: "history_message", ID: "postcapture-child-steering-original"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := s.humanSteeringMessage(t.Context(), collaboration.PendingDelivery{Event: published.Event}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		expectedSteering = canonicalContextMessage(prepared.Message)
		if err := handle.Agent.FlushRuntimeMessages(t.Context()); err != nil {
			t.Fatal(err)
		}
		close(releaseCheckpoint)
	}
	settled := make(chan struct{})
	go func() { runner.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("active child did not settle after Stop")
	}
	if rejectTerminal {
		if len(runner.Manager().CompletedResults()) != 0 {
			t.Fatal("failed terminal transcript publication falsely acknowledged child cancellation")
		}
		checkpoint, open, err := sessiondb.LoadRecoveryCheckpoint(db, identity.AgentID, identity.AgentID)
		if err != nil || !open || checkpoint.Status.State != recoverytypes.NeedsAttention {
			t.Fatalf("failed active Stop lost its owned snapshot: open=%v checkpoint=%+v err=%v", open, checkpoint, err)
		}
		partials := 0
		for _, message := range checkpoint.ActiveContext {
			if message.ContentString() == "accepted second partial" {
				partials++
			}
		}
		if partials != 1 {
			t.Fatalf("failed terminal publication lost accepted partial output: count=%d", partials)
		}
		var archivePartial, activePartial provider.ChatMessage
		for _, message := range checkpoint.Archive {
			if message.ContentString() == "accepted second partial" {
				archivePartial = canonicalContextMessage(message)
			}
		}
		for _, message := range checkpoint.ActiveContext {
			if message.ContentString() == "accepted second partial" {
				activePartial = message
			}
		}
		if archivePartial.InternalID == "" || !reflect.DeepEqual(archivePartial, activePartial) {
			t.Fatal("failed publication split one accepted output into two archive/context identities")
		}
		if _, err := db.Exec(`DROP TRIGGER reject_child_terminal`); err != nil {
			t.Fatal(err)
		}
		if s.CancelRecoveryFor(identity.AgentID, checkpoint.Status.ID, checkpoint.Status.Generation) != nil {
			t.Fatal("checked parked Stop could not persist the repaired terminal transcript")
		}
	}
	before := s.CompletedSubAgentSnapshots()
	if len(before) != 1 || before[0].Result.Status != agent.SubAgentCancelled {
		t.Fatalf("real active Stop did not retain a cancelled review: %+v", before)
	}
	partials, stops := 0, 0
	for _, row := range before[0].Messages {
		if row.Msg.ContentString() == "accepted second partial" {
			partials++
		}
		if row.Msg.EffectiveOrigin() == provider.OriginTurnStopped {
			stops++
			if !strings.HasPrefix(row.Msg.InternalID, "turn-stopped:") {
				t.Fatal("stopped child boundary has no exact lifecycle identity")
			}
		}
	}
	if partials != 1 || stops != 1 || requests.Load() != 2 || effects.Load() != 1 {
		t.Fatalf("active Stop lost/duplicated work: partials=%d stops=%d requests=%d effects=%d", partials, stops, requests.Load(), effects.Load())
	}
	if lateSteering {
		state, _, err := sessiondb.LoadContextState(db, identity.AgentID, identity.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		count, boundary, steering := 0, -1, -1
		for index, message := range state.Messages {
			if message.EffectiveOrigin() == provider.OriginTurnStopped {
				boundary = index
			}
			if message.InternalID == "postcapture-child-steering-original" {
				count++
				steering = index
				if !reflect.DeepEqual(message, expectedSteering) {
					t.Fatalf("late steering lost exact content or provenance: %+v", message)
				}
			}
		}
		if count != 1 || steering <= boundary || boundary < 0 {
			t.Fatalf("postcapture steering was lost, duplicated or reordered: count=%d boundary=%d steering=%d", count, boundary, steering)
		}
	}
	var terminal sessionSupervisedJourneySnapshot
	if lateSteering {
		terminal, err = loadSessionSupervisedJourneySnapshot(t.Context(), childPath, identity.AgentID, identity.AgentID, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	path := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	after := resumed.CompletedSubAgentSnapshots()
	if len(after) != 1 || !reflect.DeepEqual(before[0].Messages, after[0].Messages) || resumed.SubAgentRunner().Running() || requests.Load() != 2 || effects.Load() != 1 {
		t.Fatalf("passive resume changed accepted evidence or executed work: requests=%d effects=%d", requests.Load(), effects.Load())
	}
	if lateSteering {
		passive, err := loadSessionSupervisedJourneySnapshot(t.Context(), childPath, identity.AgentID, identity.AgentID, "")
		if err != nil || !reflect.DeepEqual(terminal, passive) {
			t.Fatalf("passive resume changed late steering context, transcript, identities or ledger: err=%v", err)
		}
	}
}
