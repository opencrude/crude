package shellchat

import (
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/theme"
)

func TestSetMessagesAppliesStableHeightDeltaWithoutListRebuild(t *testing.T) {
	s := New(theme.DefaultDark())
	rows := []chatprojection.MessageRow{{ID: "a", Lines: []string{"first"}}, {ID: "b", Lines: []string{"second"}}}
	s.SetMessages(rows, PaintParams{Generation: 1, StructureChanged: true})
	revision := s.list.StructureRevision()
	rows[1] = chatprojection.MessageRow{ID: "b", Lines: []string{"updated"}}
	s.SetMessages(rows, PaintParams{Generation: 2, ChangedIndices: []int{1}})
	if s.list.StructureRevision() != revision {
		t.Fatal("stable-height delta rebuilt prefix index")
	}
	s.list.SelectIndex(1)
	item, ok := s.list.Selected()
	if !ok {
		t.Fatal("updated row missing")
	}
	if got := item.Payload.(chatprojection.MessageRow).Lines[0]; got != "updated" {
		t.Fatalf("updated row=%q", got)
	}
}

func TestSetMessagesRebuildsForDifferentSourceAtSameGeneration(t *testing.T) {
	s := New(theme.DefaultDark())
	s.SetMessages(nil, PaintParams{SourceID: 1, Generation: 1, StructureChanged: true})

	rows := []chatprojection.MessageRow{{ID: "resumed", Lines: []string{"hydrated history"}}}
	s.SetMessages(rows, PaintParams{SourceID: 2, Generation: 1, StructureChanged: true})

	if len(s.list.Items()) != 1 {
		t.Fatalf("different source at the same local generation left %d rows, want 1", len(s.list.Items()))
	}
	s.list.SelectIndex(0)
	item, ok := s.list.Selected()
	if !ok || item.Payload.(chatprojection.MessageRow).Lines[0] != "hydrated history" {
		t.Fatalf("different source did not replace retained rows: %#v", item)
	}
}
