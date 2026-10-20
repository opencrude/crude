package sessiondb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/types"
	recoverytypes "crude/types/recovery"
)

func TestInspectAgentWorkProjectsOnlyBoundedPublicEvidence(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	insert := func(turn int, role, origin, visibility, id string, meta DisplayMeta) {
		t.Helper()
		payload, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO messages(internal_id,role,origin,visibility,content,reasoning_content,tool_calls,sequence_num,turn_number,agent_id,created_at,display_meta)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, role, origin, visibility, "RAW_PROVIDER_CONTENT", "PRIVATE_REASONING", `[{"arguments":"SECRET_ARGUMENTS"}]`, int64(turn*100)+meta.DurationMs, turn, "agent", time.Now().UnixNano(), payload); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, "assistant", "assistant", "conversation", "old-message", DisplayMeta{Snapshots: []types.ConversationMessage{{Role: "assistant", Content: "STALE_OLD_TURN"}}})
	insert(2, "user", "peer", "context_only", "wake-event", DisplayMeta{TurnGeneration: 7, Snapshots: []types.ConversationMessage{{Role: "user", Content: "HIDDEN_INPUT"}}})
	if _, err := db.Exec(`INSERT INTO collaboration_admissions(delivery_id,event_id,session_id,agent_id,runtime_epoch,delivery_version,context_commit_id,run_id,published_at,admitted_at) VALUES('delivery','wake-event','session','agent',1,1,'commit','exact-run',1,1)`); err != nil {
		t.Fatal(err)
	}
	for i := range 15 {
		insert(2, "tool", "tool", "conversation", fmt.Sprintf("tool-%02d", i), DisplayMeta{TurnGeneration: 7, DurationMs: int64(i + 1), Snapshots: []types.ConversationMessage{{Role: "tool", ToolName: "read_file", ToolStatus: "success", Content: "PRIVATE_TOOL_OUTPUT", ToolArgs: "SECRET_ARGUMENTS", ToolInput: "SECRET_INPUT"}}})
	}
	insert(2, "assistant", "assistant", "conversation", "latest-answer", DisplayMeta{TurnGeneration: 7, DurationMs: 20, Snapshots: []types.ConversationMessage{{Role: "thinking", Content: "PRIVATE_REASONING"}, {Role: "assistant", Content: strings.Repeat("visible answer ", 150)}}})
	insert(2, "assistant", "runtime_context", "context_only", "hidden", DisplayMeta{TurnGeneration: 7, DurationMs: 21, ContextOnly: true, Snapshots: []types.ConversationMessage{{Role: "assistant", Content: "PRIVATE_SYSTEM_CONTEXT"}}})
	var before int
	if err := db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	view, err := InspectAgentWork(t.Context(), db, "agent", "agent")
	if err != nil || !view.Available || view.Turn != 2 || view.Generation != 7 || !view.Truncated || len(view.Messages) != 12 {
		t.Fatal("recent public evidence not bounded to newest turn", view, err)
	}
	if view.Input == nil || view.Input.MessageID != "wake-event" || view.Input.RunID != "exact-run" || view.Input.Preview != "" {
		t.Fatal("exact input attribution or hidden input privacy lost", view.Input)
	}
	last := view.Messages[len(view.Messages)-1]
	if last.MessageID != "latest-answer" || last.Role != "assistant" || !last.Truncated || len(last.Text) != 1200 {
		t.Fatal("latest answer lost or unbounded", last)
	}
	for _, message := range view.Messages[:len(view.Messages)-1] {
		if message.ToolName != "read_file" || message.Status != "success" || message.Text != "" {
			t.Fatal("tool evidence missing or tool output exposed", message)
		}
	}
	encoded, _ := json.Marshal(view)
	for _, excluded := range []string{"PRIVATE_", "SECRET_", "RAW_PROVIDER_", "STALE_", "HIDDEN_INPUT"} {
		if strings.Contains(string(encoded), excluded) {
			t.Fatal("private/unrelated evidence leaked", excluded)
		}
	}
	var after int
	if err := db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("inspection wrote session data", before, after, err)
	}
	absent, err := InspectAgentWork(t.Context(), db, "other-agent", "other-agent")
	if err != nil || absent.Available || len(absent.Messages) != 0 {
		t.Fatal("another agent's work was substituted", absent, err)
	}
}

func TestInspectAgentRecoveryDoesNotExposeOrdinaryActiveCheckpointsAsStops(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []recoverytypes.State{"", recoverytypes.Recovering, recoverytypes.NeedsAttention, recoverytypes.Interrupted, recoverytypes.Cancelled, recoverytypes.Recovered} {
		cp := RecoveryCheckpoint{Status: recoverytypes.Status{ID: "incident", AgentID: "agent", Generation: 2, State: state}}
		if err := SaveRecoveryCheckpoint(db, "session", cp); err != nil {
			t.Fatal(err)
		}
		view, err := InspectAgentRecovery(t.Context(), db, "session", "agent")
		want := state == recoverytypes.NeedsAttention || state == recoverytypes.Interrupted
		if err != nil || (view != nil) != want {
			t.Fatal("checkpoint state confused with actionable recovery", state, view, err)
		}
	}
}
