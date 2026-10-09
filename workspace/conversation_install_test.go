package workspace

import (
	"reflect"
	"testing"

	"crude/types/recovery"
)

func TestConversationInstallDeltaAtomicAndDetached(t *testing.T) {
	c := NewConversation()
	initial := ConversationDelta{Reset: true, Revision: 9, Order: []string{"one"}, Upserts: []Message{{InternalID: "one", Content: "first", ReferenceLabels: []string{"media"}}}}
	if err := c.InstallDelta(initial); err != nil {
		t.Fatal(err)
	}
	initial.Upserts[0].ReferenceLabels[0] = "changed outside"
	if c.Messages()[0].ReferenceLabels[0] != "media" {
		t.Fatal("mirror aliases input")
	}
	before := c.Messages()
	for _, delta := range []ConversationDelta{
		{BaseRevision: 8, Revision: 10, Upserts: []Message{{InternalID: "one", Content: "stale"}}},
		{BaseRevision: 9, Revision: 10, Order: []string{"missing"}},
		{BaseRevision: 9, Revision: 10, Order: []string{}},
		{BaseRevision: 9, Revision: 10, Upserts: []Message{{InternalID: "new"}}},
	} {
		if err := c.InstallDelta(delta); err == nil {
			t.Fatalf("accepted invalid delta: %+v", delta)
		}
		if c.Version() != 9 || !reflect.DeepEqual(before, c.Messages()) {
			t.Fatal("invalid delta partially mutated mirror")
		}
	}
	if err := c.InstallDelta(ConversationDelta{BaseRevision: 9, Revision: 10, Upserts: []Message{{InternalID: "one", Content: "next"}}}); err != nil {
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
