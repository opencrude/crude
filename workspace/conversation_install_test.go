package workspace

import (
	"reflect"
	"testing"
	"time"

	"crude/types/conversation"
	"crude/types/recovery"
)

func TestConversationInstallDeltaAtomicAndDetached(t *testing.T) {
	c := NewConversation()
	initial := ConversationDelta{Reset: true, Revision: 9, Order: []string{"one"}, Upserts: []conversation.Message{{InternalID: "one", Content: "first", ReferenceLabels: []string{"media"}}}}
	if err := c.InstallDelta(initial); err != nil {
		t.Fatal(err)
	}
	initial.Upserts[0].ReferenceLabels[0] = "changed outside"
	if c.Messages()[0].ReferenceLabels[0] != "media" {
		t.Fatal("mirror aliases input")
	}
	before := c.Messages()
	for _, delta := range []ConversationDelta{
		{BaseRevision: 8, Revision: 10, Upserts: []conversation.Message{{InternalID: "one", Content: "stale"}}},
		{BaseRevision: 9, Revision: 10, Order: []string{"missing"}},
		{BaseRevision: 9, Revision: 10, Order: []string{}},
		{BaseRevision: 9, Revision: 10, Upserts: []conversation.Message{{InternalID: "new"}}},
	} {
		if err := c.InstallDelta(delta); err == nil {
			t.Fatalf("accepted invalid delta: %+v", delta)
		}
		if c.Version() != 9 || !reflect.DeepEqual(before, c.Messages()) {
			t.Fatal("invalid delta partially mutated mirror")
		}
	}
	if err := c.InstallDelta(ConversationDelta{BaseRevision: 9, Revision: 10, Upserts: []conversation.Message{{InternalID: "one", Content: "next"}}}); err != nil {
		t.Fatal(err)
	}
	if c.Messages()[0].Content != "next" || c.Version() != 10 {
		t.Fatal("valid update lost")
	}
}

func TestRecoveryPublishesIncrementalDelta(t *testing.T) {
	c := NewConversation()
	status := recovery.Status{ID: "incident", State: recovery.NeedsAttention, Summary: "first"}
	c.UpdateRecovery(status)
	before := c.Version()
	status.Summary = "second"
	c.UpdateRecovery(status)
	delta := c.ChangesSince(before)
	if delta.Reset || delta.Order != nil || len(delta.Upserts) != 1 || delta.Upserts[0].Recovery.Summary != "second" {
		t.Fatalf("recovery was not incremental: %+v", delta)
	}
}

// A terminal failure can happen long after its first (recoverable) interruption.
// The receipt must reach the tail as a move of its exact ID, not a second row.
func TestFailedRecoveryMovesTheExistingRowBeforePendingInput(t *testing.T) {
	c, replica := NewConversation(), NewConversation()
	c.AppendMessage(conversation.Message{InternalID: "prompt", Role: MessageRoleUser})
	status := recovery.Status{ID: "incident", State: recovery.Recovering, StartedAt: time.Unix(100, 0), UpdatedAt: time.Unix(101, 0)}
	c.UpdateRecovery(status)
	c.AppendMessage(conversation.Message{InternalID: "accepted", Role: MessageRoleAssistant, Content: "Continued after recovery."})
	c.AppendMessage(conversation.Message{InternalID: "queued", Role: MessageRoleUser, PendingSteering: true})
	if err := replica.InstallDelta(c.ChangesSince(0)); err != nil {
		t.Fatal(err)
	}
	status.State, status.Detail, status.UpdatedAt = recovery.Failed, "Provider quota reached", time.Unix(190, 0)
	c.UpdateRecovery(status)
	if err := replica.InstallDelta(c.ChangesSince(replica.Version())); err != nil {
		t.Fatal(err)
	}
	want := []string{"prompt", "accepted", "incident", "queued"}
	for _, transcript := range []*Conversation{c, replica} {
		if got := transcript.messageOrder(); !reflect.DeepEqual(got, want) {
			t.Fatalf("failure remained above accepted work or moved queued input: %v", got)
		}
		row, ok := transcript.Message("incident")
		if !ok || !row.Time.Equal(status.UpdatedAt) || row.Recovery == nil || *row.Recovery != status {
			t.Fatalf("failure lost its exact settlement: %+v", row)
		}
	}
	if c.UpdateRecovery(status) {
		t.Fatal("repeated failure receipt moved or duplicated the row")
	}
}
