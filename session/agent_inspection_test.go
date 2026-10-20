package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
)

func TestAgentInspectExplainsFinishedPeerWakeWithoutOpeningSession(t *testing.T) {
	sessions := t.TempDir()
	observerDB, err := sessiondb.OpenAndMigrate(filepath.Join(sessions, "observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer observerDB.Close()
	store, err := collaboration.Attach(observerDB, "/inspection/project", filepath.Join(sessions, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	observer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "observer", SessionID: "observer", OwnerInstance: "observer-owner"})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "peer", Name: "Beatriz", SessionID: "peer-session", OwnerInstance: "peer-owner"})
	if err != nil {
		t.Fatal(err)
	}
	peerPath := filepath.Join(sessions, peer.SessionID+".db")
	peerDB, err := sessiondb.OpenAndMigrate(peerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer peerDB.Close()
	if err := sessiondb.CreateSessionRow(peerDB, peer.SessionID); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now()
	notice, err := store.Publish(t.Context(), collaboration.PublishRequest{SenderID: observer.ID, RecipientIDs: []string{peer.ID}, Origin: collaboration.OriginRuntime, Kind: collaboration.EventPeerNote, IdempotencyKey: "cancel-notice", Body: "Child cancelled; the obsolete task will not continue."})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(t.Context(), collaboration.StartRunRequest{ID: "peer-wake-run", AgentID: peer.ID, RuntimeEpoch: peer.RuntimeEpoch, Origin: collaboration.OriginPeer})
	if err != nil {
		t.Fatal(err)
	}
	input := collaborationMessage(collaboration.PendingDelivery{Event: notice.Event})
	answer := provider.NewTextMessage("assistant", "Inspected the cancelled child; no coding task resumed.")
	answer.InternalID = "recent-answer"
	answer.ReasoningContent = "DO_NOT_EXPOSE_PRIVATE_REASONING"
	result := provider.NewTextMessage("tool", "DO_NOT_EXPOSE_TOOL_OUTPUT")
	result.InternalID, result.ToolCallID = "inspection-tool", "inspect-call"
	messages := []provider.ChatMessage{input, result, answer}
	metas := make([]sessiondb.DisplayMeta, len(messages))
	for i, message := range messages {
		meta := sessiondb.DisplayMeta{TurnGeneration: 10}
		if message.Role == "tool" {
			meta.ToolName, meta.ToolArgs = "execution.inspect", "DO_NOT_EXPOSE_RAW_ARGUMENTS"
		}
		metas[i] = canonicalizeDisplayMeta(message, meta, "", i, stamp)
	}
	if _, err := sessiondb.ReplaceMessagesWithMeta(peerDB, peer.SessionID, messages, []int{10, 10, 10}, metas); err != nil {
		t.Fatal(err)
	}
	if _, err := peerDB.Exec(`UPDATE messages SET agent_id=?`, peer.ID); err != nil {
		t.Fatal(err)
	}
	// Before terminal settlement, admission is the exact run/input link. The
	// inspector must not pick some other notice merely received nearby.
	if _, err := peerDB.Exec(`INSERT INTO collaboration_admissions(delivery_id,event_id,session_id,agent_id,runtime_epoch,delivery_version,context_commit_id,run_id,published_at,admitted_at)
		VALUES('input-link',?,?,?,1,1,'recorded-input',?,1,1)`, notice.Event.ID, peer.SessionID, peer.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	s := &Session{sessionID: "observer", db: observerDB, dbPath: filepath.Join(sessions, "observer.db"), collaborationStore: store}
	activeView, err := s.inspectAgent(t.Context(), store, peer.ID)
	if err != nil || activeView.Run == nil || activeView.Run.ID != run.ID || !activeView.Trigger.Available || activeView.Trigger.EventID != notice.Event.ID {
		t.Fatal("active wake inspection lost exact admission", activeView, err)
	}
	readOnlyDB, closeReadOnly, err := s.agentInspectionDB(t.Context(), store, peer)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := readOnlyDB.Exec(`UPDATE sessions SET message_count=0`)
	closeReadOnly()
	if writeErr == nil {
		t.Fatal("cross-session inspection handle is writable")
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: run.ID, AgentID: peer.ID, RuntimeEpoch: peer.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunSucceeded, Outcome: "completed", ContextCommitID: notice.Event.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachAgent(t.Context(), peer.ID, "peer-owner", peer.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	before, err := store.Agent(t.Context(), peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, handled, err := s.executeCollaborationTool(t.Context(), observer.ID, "agent_inspect", map[string]any{"agent_id": peer.ID})
	if err != nil || !handled {
		t.Fatal(resultJSON, handled, err)
	}
	var view agentInspection
	if err := json.Unmarshal([]byte(resultJSON.Content), &view); err != nil {
		t.Fatal(err)
	}
	if view.Run != nil || view.LatestRun == nil || view.LatestRun.ID != run.ID || view.LatestRun.State != collaboration.RunSucceeded || view.Agent.Presence != collaboration.RuntimeOffline {
		t.Fatal("finished offline history looks active", view)
	}
	if !view.Trigger.Available || view.Trigger.EventID != notice.Event.ID || view.Trigger.Origin != "runtime" || view.Trigger.SenderID != observer.ID || view.Trigger.Preview != notice.Event.Body {
		t.Fatal("wake reason could not be answered directly", view.Trigger)
	}
	if !view.RecentWork.Available || view.RecentWork.Generation != 10 || len(view.RecentWork.Messages) != 2 || view.RecentWork.Messages[1].Text != answer.ContentString() {
		t.Fatal("visible recent work unavailable or wrong", view.RecentWork)
	}
	if strings.Contains(resultJSON.Content, "DO_NOT_EXPOSE") || len(view.Reports) != 0 {
		t.Fatal("private or unrelated evidence leaked", resultJSON.Content)
	}
	after, err := store.Agent(t.Context(), peer.ID)
	if err != nil || !reflect.DeepEqual(before, after) || len(s.orchestrators) != 0 || s.SubAgentRunner() != nil {
		t.Fatal("inspection attached or started the foreign session", err)
	}
	var deliveryState string
	if err := observerDB.QueryRow(`SELECT state FROM collaboration_deliveries WHERE id=?`, notice.Deliveries[0].ID).Scan(&deliveryState); err != nil || deliveryState != "pending" {
		t.Fatal("inspection admitted or consumed a notification", deliveryState, err)
	}
	if collaborationEffect("agent_inspect") != "read" {
		t.Fatal("inspection acquired an effectful permission lane")
	}
	// The existing lifecycle remains usable after passive inspection.
	rebound, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: peer.ID, SessionID: peer.SessionID, OwnerInstance: "new-peer-owner"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.StartRun(t.Context(), collaboration.StartRunRequest{ID: "next-real-run", AgentID: peer.ID, RuntimeEpoch: rebound.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal("inspection broke later run admission", err)
	}
	replacement, err := s.inspectAgent(t.Context(), store, peer.ID)
	if err != nil || replacement.Run == nil || replacement.Run.ID != next.ID || replacement.Trigger.Available || replacement.RecentWork.RunID != run.ID {
		t.Fatal("replacement attempt inherited the old trigger or confused persisted history with live work", replacement, err)
	}
	if _, err := store.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: next.ID, AgentID: peer.ID, RuntimeEpoch: rebound.RuntimeEpoch, ExpectedVersion: next.Version, State: collaboration.RunSucceeded, Outcome: "completed"}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentInspectRealHumanRunDoesNotGuessUnrecordedTrigger(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"finished human task\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(release)
	s, err := NewWithSessionDir("human-observe", modelAttributionConfig(server.URL, "inspection-model"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(t.TempDir())
	store := attachDelegationTestCollaboration(t, s)
	sender := newMockEventSender()
	orchestrator, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	input := provider.PlainInput("inspect this exact human input")
	input.InternalID = "human-input-id"
	intent, err := s.PrepareInputAdmission(orchestrator.ID, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.CommitInputAdmission(t.Context(), intent); result.Status != InputAdmissionAccepted {
		t.Fatal(result)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("human turn never reached provider")
	}
	view, err := s.inspectAgent(t.Context(), store, orchestrator.ID)
	if err != nil || view.Run == nil || view.Run.State.Terminal() || view.Trigger.Available || view.Trigger.Note == "" || view.RecentWork.Input == nil || view.RecentWork.Input.MessageID != input.InternalID || view.RecentWork.Input.Preview != input.Text {
		t.Fatal("active human input missing or unrecorded run link guessed", view, err)
	}
	release <- struct{}{}
	waitTurnDone(t, sender, orchestrator.ID)
	view, err = s.inspectAgent(t.Context(), store, orchestrator.ID)
	if err != nil || view.Run != nil || !view.Trigger.Available || view.Trigger.InputID != input.InternalID || view.Trigger.Preview != input.Text {
		t.Fatal("finished human input link unavailable", view, err)
	}
	if len(view.RecentWork.Messages) != 1 || view.RecentWork.Messages[0].Text != "finished human task" {
		t.Fatal("real session-owned output missing from runtime inspection", view.RecentWork)
	}
}

func TestAgentInspectChildContinuationLabelsAssignmentHistoryHonestly(t *testing.T) {
	s, store, parent, _ := collaborationContractFixture(t)
	child, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "continued-child", SessionID: s.sessionID, ParentID: parent.ID, OwnerInstance: "continued-owner"})
	if err != nil {
		t.Fatal(err)
	}
	handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: agent.AgentID(child.ID)}, Agent: &agent.Agent{TurnGeneration: 1}, DB: s.db}
	assignment := provider.NewTextMessage("user", "original child assignment")
	first := provider.NewTextMessage("assistant", "accepted before continuation")
	archive, active, deltas, err := childTerminalPublication(handle, []provider.ChatMessage{assignment, first}, []provider.ChatMessage{assignment, first}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{SessionID: s.sessionID, AgentID: child.ID, RuntimeAgentID: child.ID, Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 1, Reason: "first", Messages: active}}, TranscriptAppend: deltas})
	if err != nil {
		t.Fatal(err)
	}
	handle.Agent.TurnGeneration = 2
	continued := provider.NewTextMessage("assistant", "accepted after explicit continuation")
	_, active, deltas, err = childTerminalPublication(handle, append(archive, continued), append(active, continued), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessiondb.PublishContext(s.db, sessiondb.ContextPublication{SessionID: s.sessionID, AgentID: child.ID, RuntimeAgentID: child.ID, ExpectedRevision: state.Revision, Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: 2, Reason: "continued", Messages: active}}, TranscriptAppend: deltas}); err != nil {
		t.Fatal(err)
	}
	view, err := s.inspectAgent(t.Context(), store, child.ID)
	if err != nil || !view.RecentWork.Available || view.RecentWork.Scope != "child_assignment_transcript" || view.RecentWork.Generation != 0 || view.RecentWork.RunID != "" || len(view.RecentWork.Messages) != 2 {
		t.Fatal("continued child evidence was falsely scoped to one attempt", view, err)
	}
	if view.RecentWork.Messages[0].Text != first.ContentString() || view.RecentWork.Messages[1].Text != continued.ContentString() {
		t.Fatal("child history lost preserved work", view.RecentWork)
	}
}

func TestAgentInspectPreservesParkedRecoveryAndMissingStorage(t *testing.T) {
	f := newParkedChildFixture(t)
	s := f.session
	store := attachDelegationTestCollaboration(t, s)
	parent, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: s.sessionID + "_orch_1", SessionID: s.sessionID, OwnerInstance: "inspect-parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: f.childID, SessionID: f.childID, ParentID: parent.ID, OwnerInstance: "inspect-child"})
	if err != nil {
		t.Fatal(err)
	}
	before := s.pendingRecoveries[f.childID]
	view, err := s.inspectAgent(t.Context(), store, child.ID)
	if err != nil || view.Recovery == nil || view.Recovery.ID != before.Status.ID || !reflect.DeepEqual(before, s.pendingRecoveries[f.childID]) || f.requests.Load() != 0 || f.effects.Load() != 0 {
		t.Fatal("inspection changed parked work or hid recovery", view, err)
	}
	observerDB, err := sessiondb.OpenAndMigrate(filepath.Join(filepath.Dir(s.dbPath), "other-observer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer observerDB.Close()
	observer := &Session{sessionID: "other-observer", db: observerDB, dbPath: filepath.Join(filepath.Dir(s.dbPath), "other-observer.db"), collaborationStore: store}
	foreign, err := observer.inspectAgent(t.Context(), store, child.ID)
	if err != nil || foreign.Recovery == nil || foreign.Recovery.ID != before.Status.ID || foreign.Recovery.Generation != before.Status.Generation {
		t.Fatal("cross-session inspection hid durable parked recovery", foreign, err)
	}
	absent, err := store.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: "never-started", SessionID: "missing-session", OwnerInstance: "missing-owner"})
	if err != nil {
		t.Fatal(err)
	}
	view, err = s.inspectAgent(t.Context(), store, absent.ID)
	if err != nil || view.Run != nil || view.LatestRun != nil || view.Trigger.Available || view.RecentWork.Available || view.RecentWork.Note == "" {
		t.Fatal("missing work is fabricated or destroys basic inspection", view, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.dbPath), "missing-session.db")); !os.IsNotExist(err) {
		t.Fatal("passive inspection created storage", err)
	}
}
