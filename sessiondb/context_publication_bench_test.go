package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/types/recovery"
)

// checkpointBench is a resumed dense context of history messages (~7.5 KB
// each, ~4 MB at 560: the shape of a real 100 MB session) with a recovery
// incident that has already checkpointed it once, so a timed round is a
// steady-state tool round.
type checkpointBench struct {
	b       *testing.B
	db      *sql.DB
	state   ContextState
	created int64
	round   int
}

func newCheckpointBench(b *testing.B, history int) *checkpointBench {
	db := OpenAndMigrateAt(b.TempDir())
	b.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		b.Fatal(err)
	}
	body := strings.Repeat("tool output line with realistic width and content ", 150)
	bench := &checkpointBench{b: b, db: db, created: time.Unix(0, 1).UnixNano()}
	deltas := make([]ContextMessageDelta, history)
	for i := range deltas {
		bench.created++
		deltas[i] = ContextMessageDelta{Message: provider.ChatMessage{InternalID: fmt.Sprintf("m%d", i), Role: "user", Content: provider.TextContent(fmt.Sprintf("%d %s", i, body))}, Turn: i/4 + 1, CreatedAt: bench.created, AgentID: "root"}
	}
	var err error
	if bench.state, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", Mutation: ContextMutation{Append: deltas}}); err != nil {
		b.Fatal(err)
	}
	return bench
}

func (c *checkpointBench) checkpoint(archive []provider.ChatMessage) *RecoveryCheckpoint {
	return &RecoveryCheckpoint{Status: recovery.Status{ID: "incident", AgentID: "root", Generation: 1}, PendingInput: provider.PlainInput("continue"), InputAdmitted: true, Archive: archive}
}

func (c *checkpointBench) message() provider.ChatMessage {
	c.round++
	return provider.ChatMessage{InternalID: fmt.Sprintf("n%d", c.round), Role: "user", Content: provider.TextContent("round result")}
}

// appendRound publishes a round as an appended transcript message.
func (c *checkpointBench) appendRound() {
	c.created++
	delta := ContextMessageDelta{Message: c.message(), Turn: 200 + c.round, CreatedAt: c.created, AgentID: "root"}
	archive := append(c.state.Messages[:len(c.state.Messages):len(c.state.Messages)], delta.Message)
	state, err := PublishContext(c.db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: c.state.Revision,
		Mutation: ContextMutation{Append: []ContextMessageDelta{delta}}, Recovery: c.checkpoint(archive)})
	if err != nil {
		c.b.Fatal(err)
	}
	c.state = state
}

// recoveryRound publishes a round as the supervised run does: its whole active
// context as a revision-fenced replacement extending the committed one.
func (c *checkpointBench) recoveryRound() {
	active := append(c.state.Messages[:len(c.state.Messages):len(c.state.Messages)], c.message())
	state, err := PublishContext(c.db, ContextPublication{SessionID: "session", AgentID: "root", RuntimeAgentID: "root", ExpectedRevision: c.state.Revision,
		Mutation: ContextMutation{Replace: &ContextReplacement{Generation: c.state.Generation, Reason: c.state.Reason, Messages: active}}, Recovery: c.checkpoint(active)})
	if err != nil {
		c.b.Fatal(err)
	}
	c.state = state
}

// BenchmarkCheckpointAppend_560Msgs measures a tool round that appends one
// message to a dense context and checkpoints the recovery archive.
func BenchmarkCheckpointAppend_560Msgs(b *testing.B) {
	bench := newCheckpointBench(b, 560)
	bench.appendRound()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bench.appendRound()
	}
}

// BenchmarkRecoveryCheckpoint_560Msgs is the supervised tool-round checkpoint
// as the run publishes it. A round writes what it adds: rows for the new
// message, never the history.
func BenchmarkRecoveryCheckpoint_560Msgs(b *testing.B) { benchmarkRecoveryCheckpoint(b, 560) }

// BenchmarkRecoveryCheckpoint_2000Msgs stays within a small constant of the
// 560-message case.
func BenchmarkRecoveryCheckpoint_2000Msgs(b *testing.B) { benchmarkRecoveryCheckpoint(b, 2000) }

func benchmarkRecoveryCheckpoint(b *testing.B, history int) {
	bench := newCheckpointBench(b, history)
	bench.recoveryRound()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bench.recoveryRound()
	}
}

// Appending stores only the new messages as tail rows. The stored list must
// stay exactly what encoding the full context produces, for every message
// shape, so archives and replays compare equal.
func TestCheckpointAppendKeepsExactContextEncoding(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	created := time.Unix(0, 1).UnixNano()
	messages := []provider.ChatMessage{
		{InternalID: "u1", Role: "user", Content: provider.TextContent("escape <b>&</b>   ✕ \"quoted\"")},
		{InternalID: "a1", Role: "assistant", Content: provider.TextContent(""), ReasoningContent: "think", ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"a<b>.go"}`}}}, ReasoningDetails: []json.RawMessage{json.RawMessage(`{"type":"x","text":"y"}`)}},
		{InternalID: "t1", Role: "tool", ToolCallID: "c1", Content: provider.TextContent("line\nline\ttab"), ToolError: true},
		{InternalID: "u2", Role: "user", Content: provider.TextContent("next"), ReferenceLabels: []string{"a", "b"}},
	}
	var state ContextState
	for start, batch := 0, 1; start < len(messages); start, batch = start+batch, batch+1 {
		end := min(len(messages), start+batch)
		deltas := make([]ContextMessageDelta, 0, end-start)
		for _, message := range messages[start:end] {
			created++
			deltas = append(deltas, ContextMessageDelta{Message: message, Turn: 1, CreatedAt: created, AgentID: "root"})
		}
		var err error
		state, err = PublishContext(db, ContextPublication{SessionID: "session", AgentID: "root", ExpectedRevision: state.Revision, Mutation: ContextMutation{Append: deltas}})
		if err != nil {
			t.Fatal(err)
		}
		stored := storedContextEncoding(t, db, "session", "root")
		want, err := json.Marshal(state.Messages)
		if err != nil {
			t.Fatal(err)
		}
		if string(stored) != string(want) {
			t.Fatalf("after %d messages the stored context differs from its full encoding:\n got %s\nwant %s", end, stored, want)
		}
		loaded, ok, err := LoadContextState(db, "session", "root")
		if err != nil || !ok || !reflect.DeepEqual(loaded.Messages, state.Messages) {
			t.Fatalf("stored context does not round-trip: %v %v", ok, err)
		}
	}
}

func storedContextEncoding(t testing.TB, db *sql.DB, sessionID, agentID string) []byte {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	encoded, err := loadContextEncodingTx(tx, contextCacheKey{db, sessionID, agentID})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
