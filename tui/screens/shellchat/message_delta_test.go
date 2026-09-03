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
