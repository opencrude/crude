package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/sessiondb"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestAttachmentChipsHaveDisjointOpenAndRemoveTargets(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	refs := []sessiondb.Reference{{ID: "one", Kind: sessiondb.KindPastedText, Label: "[One]", Title: "notes with spaces.txt", Content: "notes"}, {ID: "two", Kind: sessiondb.KindPastedText, Label: "[Two]", Title: "second.txt", Content: "more"}}
	m.SetReferenceStore(&pasteReferenceStore{references: refs})
	m.SetInput("[One] [Two]")
	m.LayoutShell(32, 5)
	chips := m.ReferenceChips(32)
	if len(chips) != 2 || chips[0].Remove.W != 3 || chips[1].ID != "" {
		t.Fatalf("expected roomy close and overflow: %+v", chips)
	}
	for _, r := range []ui.Rect{chips[0].Body, chips[0].Remove} {
		for x := r.X; x < r.X+r.W; x++ {
			res := m.HandleShell(ui.EventContext{LocalX: x, LocalY: 1}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
			want := "input.attachment.open"
			if r == chips[0].Remove {
				want = "input.attachment.remove"
			}
			if res.Action == nil || res.Action.ID != want || res.Action.Payload != "one" {
				t.Fatalf("x=%d dispatched %+v, want %s", x, res, want)
			}
		}
	}
	m.HandleShell(ui.EventContext{LocalX: chips[1].Body.X, LocalY: 1}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if next := m.ReferenceChips(32); len(next) == 0 || next[0].ID != "two" {
		t.Fatalf("overflow lost hidden attachment: %+v", next)
	}
}
