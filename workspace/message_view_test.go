package workspace

import (
	"testing"

	"crude/sessiondb"
)

func TestTakeHydratedDataIsOneShot(t *testing.T) {
	sess := &Session{
		hydratedMessages: []ChatMessageView{{Role: "user", Content: "hello"}},
		hydratedRefs:     []sessiondb.Reference{{ID: "ref-1"}},
	}

	if got := sess.TakeHydratedMessages(); len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("unexpected hydrated messages: %#v", got)
	}
	if got := sess.TakeHydratedMessages(); len(got) != 0 {
		t.Fatalf("hydrated messages were returned twice: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 1 || got[0].ID != "ref-1" {
		t.Fatalf("unexpected hydrated references: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 0 {
		t.Fatalf("hydrated references were returned twice: %#v", got)
	}
}
