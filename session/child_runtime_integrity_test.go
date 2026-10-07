package session

import (
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
	"crude/sessiondb"
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
		[]provider.ChatMessage{assignment, assistant}, []provider.ChatMessage{assignment, assistant}, time.Unix(100, 0))
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
