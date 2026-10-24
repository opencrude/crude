package workspace

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"crude/types/reasoning"
)

func TestConversationPendingInputSurvivesWireResetAndDelta(t *testing.T) {
	source, replica := NewConversation(), NewConversation()
	source.AppendPendingSteering(Message{InternalID: "queued-input", Role: MessageRoleUser, Content: "wait for compaction"})
	apply := func(revision uint64) {
		t.Helper()
		data, err := json.Marshal(source.ChangesSince(revision))
		if err != nil {
			t.Fatal(err)
		}
		var delta ConversationDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			t.Fatal(err)
		}
		if err := replica.InstallDelta(delta); err != nil {
			t.Fatal(err)
		}
	}
	apply(0)
	if replica.PendingSteeringCount() != 1 {
		t.Fatal("wire reset dropped the pending input marker")
	}
	revision := source.Version()
	source.ConsumePendingSteering("queued-input")
	apply(revision)
	if replica.PendingSteeringCount() != 0 || len(replica.Messages()) != 1 {
		t.Fatal("wire admission did not settle the same input in place")
	}
}

// Apply publications to a subscriber-owned replica and compare with the legacy
// full export. Inputs use real mutation entry points, not journal helpers.
func TestConversationDeltaMutationJourney(t *testing.T) {
	c := NewConversation()
	records := map[string]Message{}
	var order []string
	var revision uint64
	check := func(structural bool) {
		t.Helper()
		d := c.ChangesSince(revision)
		if d.BaseRevision != revision || d.Revision != c.Version() {
			t.Fatalf("revision envelope: %+v", d)
		}
		if (d.Order != nil) != structural {
			t.Fatalf("order presence=%v, want %v", d.Order != nil, structural)
		}
		if d.Reset {
			records = map[string]Message{}
		}
		for _, id := range d.Removed {
			delete(records, id)
		}
		for _, message := range d.Upserts {
			records[message.InternalID] = message
		}
		for _, append := range d.Appends {
			message := records[append.InternalID]
			if uint64(len(message.Content)) != append.Offset {
				t.Fatal("append offset differs from independent subscriber")
			}
			message.Content += append.Text
			message.ThinkingDisplay = append.ThinkingDisplay
			records[append.InternalID] = message
		}
		if d.Order != nil {
			order = d.Order
		}
		got := make([]Message, len(order))
		for i, id := range order {
			got[i] = records[id]
		}
		if !reflect.DeepEqual(got, c.Messages()) {
			t.Fatalf("replica differs: got %+v, want %+v", got, c.Messages())
		}
		if len(records) != len(order) {
			t.Fatal("removed record remains in subscriber")
		}
		revision = d.Revision
	}
	c.AppendMessage(Message{InternalID: "human", Role: MessageRoleUser, Content: "work"})
	check(true)
	c.EnsureAssistant()
	check(true)
	c.AppendToken("answer")
	check(false)
	c.MarkError("interrupted")
	check(true)
	c.AppendReasoning(reasoning.Delta{Text: "consider", Kind: reasoning.Provisional})
	check(true)
	c.AppendThinking(" more")
	check(false)
	c.SetLatestThinkingKind(reasoning.Summary)
	check(false)
	c.MarkAllThinkingInterrupted()
	check(false)
	c.MarkAllThinkingDone()
	check(false)
	c.AppendThinking("next")
	check(true)
	// Starting a new thought also settles any older active thought.
	c.AppendMessage(Message{Role: MessageRoleUser, Content: "between"})
	check(true)
	c.AppendThinking("fresh")
	check(true)
	c.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
	check(true)
	c.UpdateTurnStatus(TurnStatus{Phase: "preparing", Progress: 20})
	check(false)
	first := c.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "first"})
	check(true)
	second := c.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "second"})
	check(true)
	c.ConsumePendingSteering(first)
	check(true)
	c.SettlePendingSteeringMessages([]string{second})
	check(false)
	c.UpdateToolStreaming("tool", "internal", "agent_start", map[string]any{"target": "child"})
	check(false) // Promotes the status in place, retaining its canonical ID.
	c.UpdateToolCall("tool", "internal", "agent_start", "assignment")
	check(false)
	c.SetToolCommand("tool", "internal", "command")
	check(false)
	c.SetToolInput("tool", "internal", "input", "scope")
	check(false)
	c.SetToolRound("tool", "internal", "round")
	check(false)
	c.AttachSubPaneToTool("tool", "child", "Child")
	check(false)
	c.SetToolPermTag("tool", "internal", "allowed")
	check(false)
	c.UpdateToolProgress("tool", "internal", "agent_start", "running", "doing", "path", 0, 0, 1)
	check(false)
	c.UpdateToolResult("tool", "internal", "agent_start", "result", false, 5, 1, 0)
	check(false)
	c.AdvanceToolLifecycles(time.Now().Add(time.Second))
	check(false)
	c.AdvanceToolLifecycles(time.Now().Add(2 * time.Second))
	check(false)
	c.UpdateToolCall("second-tool", "second-internal", "read_file", "file")
	check(true)
	c.MarkStreamingToolsCancelled()
	check(false)
	c.UpdateToolResult("second-tool", "second-internal", "read_file", "late result", false, 0, 0, 0)
	check(false)
	c.MarkToolNotExecuted("second-tool", "second-internal")
	check(false)
	c.UpdateTurnStatus(TurnStatus{Phase: "waiting"})
	check(true)
	c.ClearTurnStatus()
	check(true)
	c.EnsureAssistant()
	check(true)
	c.Finish("finished", "completed", true, "")
	check(false)
	c.Finish("", "truncated", true, "")
	check(false)
	c.Finish("", "cancelled", true, "stop")
	check(true)
	check(false)
}

func TestConversationDeltaReadersAndDetachedValues(t *testing.T) {
	c := NewConversation()
	c.AppendMessage(ownedMessageFixture())
	base := c.Version()
	c.AppendMessage(Message{InternalID: "assistant", Role: MessageRoleAssistant, Content: "old"})
	c.AppendToken(" new")
	first, second := c.ChangesSince(base), c.ChangesSince(base)
	if !reflect.DeepEqual(first, second) || len(first.Upserts) != 1 || first.Upserts[0].Content != "old new" {
		t.Fatal("readers did not independently receive coalesced current values")
	}
	first.Order[0] = "changed"
	first.Upserts[0].Content = "changed"
	if second.Order[0] != "message" || second.Upserts[0].Content != "old new" {
		t.Fatal("readers alias publication slices")
	}
	reset := c.ChangesSince(0)
	mutateMessageReferences(reset.Upserts[0])
	if !reflect.DeepEqual(c.ChangesSince(0).Upserts[0], ownedMessageFixture()) {
		t.Fatal("reset leaks nested message references")
	}
	// Steering settlement publishes the nested evidence incrementally too.
	pending := ownedMessageFixture()
	pending.InternalID = "pending"
	c.AppendPendingSteering(pending)
	base = c.Version()
	c.ConsumePendingSteering("pending")
	d := c.ChangesSince(base)
	mutateMessageReferences(d.Upserts[0])
	if !reflect.DeepEqual(c.ChangesSince(base).Upserts[0], pending) {
		t.Fatal("incremental publication leaks nested references")
	}
}

func TestConversationDeltaJournalGapAndEmptyOrder(t *testing.T) {
	c := NewConversation()
	c.EnsureAssistant()
	base := c.Version()
	for i := 0; i < conversationJournalLimit; i++ {
		c.AppendToken("x")
	}
	if d := c.ChangesSince(base); d.Reset || len(d.Appends) != 1 || len(d.Upserts) != 0 || d.Order != nil {
		t.Fatal("retained boundary failed incremental publication")
	}
	c.AppendToken("x")
	if d := c.ChangesSince(base); !d.Reset || len(d.Order) != 1 || len(d.Upserts) != 1 {
		t.Fatal("journal gap did not reset")
	}
	if !c.ChangesSince(c.Version() + 1).Reset {
		t.Fatal("future revision must fail closed")
	}
	c = NewConversation()
	c.UpdateTurnStatus(TurnStatus{Phase: "waiting"})
	base = c.Version()
	c.ClearTurnStatus()
	d := c.ChangesSince(base)
	if d.Reset || d.Order == nil || len(d.Order) != 0 || len(d.Removed) != 1 || len(d.Upserts) != 0 {
		t.Fatalf("empty membership publication: %+v", d)
	}
}

func BenchmarkConversationDeltaDenseUpdate(b *testing.B) {
	c := NewConversation()
	for i := 0; i < 10000; i++ {
		c.AppendMessage(Message{Role: MessageRoleUser, Content: "dense accepted transcript"})
	}
	c.UpdateToolCall("tool", "internal", "read_file", "file")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base := c.Version()
		c.SetToolCommand("tool", "internal", "command")
		d := c.ChangesSince(base)
		if d.Reset || d.Order != nil || len(d.Upserts) != 1 {
			b.Fatal("dense update exported unchanged transcript")
		}
	}
}

func TestConversationDeltaDenseStreamAllocations(t *testing.T) {
	c := NewConversation()
	for i := 0; i < 10000; i++ {
		c.AppendMessage(Message{Role: MessageRoleUser, Content: "dense accepted transcript"})
	}
	c.EnsureAssistant()
	allocs := testing.AllocsPerRun(100, func() {
		base := c.Version()
		c.AppendToken("x")
		d := c.ChangesSince(base)
		if d.Reset || d.Order != nil || len(d.Appends) != 1 || len(d.Upserts) != 0 {
			t.Fatal("stream published unchanged dense transcript")
		}
	})
	if allocs > 4 {
		t.Fatalf("dense streamed delta allocations = %.0f, want <=4", allocs)
	}
}
