package shellchat

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestSetMessagesWarmSourceSwitchDoesNotAllocate(t *testing.T) {
	s := New(theme.DefaultDark())
	first := make([]chatprojection.MessageRow, 80)
	second := make([]chatprojection.MessageRow, len(first))
	for i := range first {
		id := strconv.Itoa(i)
		first[i] = chatprojection.MessageRow{ID: id, Lines: []string{"first source"}}
		second[i] = chatprojection.MessageRow{ID: id, Lines: []string{"second source"}}
	}
	s.SetMessages(first, PaintParams{SourceID: 1, Generation: 7})
	s.SetMessages(second, PaintParams{SourceID: 2, Generation: 7})
	revision := s.list.StructureRevision()
	firstActive := false
	allocations := testing.AllocsPerRun(100, func() {
		firstActive = !firstActive
		if firstActive {
			s.SetMessages(first, PaintParams{SourceID: 1, Generation: 7})
		} else {
			s.SetMessages(second, PaintParams{SourceID: 2, Generation: 7})
		}
	})
	if allocations != 0 {
		t.Fatalf("warm source installation allocated %.0f times; want zero", allocations)
	}
	if s.list.StructureRevision() != revision+101 {
		t.Fatal("equal local generations bypassed source-switch index rebuilding")
	}
}

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
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 30, H: 2})
	row, ok := s.MessageAtLocalY(1)
	if !ok {
		t.Fatal("updated row missing")
	}
	if got := row.Lines[0]; got != "updated" {
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
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 30, H: 1})
	row, ok := s.MessageAtLocalY(0)
	if !ok || row.Lines[0] != "hydrated history" {
		t.Fatalf("different source did not replace retained rows: %#v", row)
	}
}

func TestSetMessagesSourceSwitchAndDeltaPaintLatestSnapshot(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	first := []chatprojection.MessageRow{
		{ID: "shared", Lines: []string{"first source"}, LeafToggles: map[int]string{0: "first-leaf"}},
		{ID: "tail", Lines: []string{"first tail"}},
	}
	second := []chatprojection.MessageRow{
		{ID: "shared", Lines: []string{"second source"}, LeafToggles: map[int]string{0: "second-leaf"}},
		{ID: "tail", Lines: []string{"second tail"}},
	}
	paint := PaintParams{SourceID: 1, Generation: 7, Compose: ComposeParams{InnerW: 20, PadLR: 2, Theme: th}}
	s.SetMessages(first, paint)
	host := ui.NewHost(th, s)
	host.Resize(24, 1)
	host.Layout()
	s.list.SelectIndex(1)
	s.ScrollModel().SetAutoScroll(false)
	s.ScrollModel().ScrollTo(0)
	revision := s.list.StructureRevision()
	paint.SourceID = 2
	s.SetMessages(second, paint)
	if s.list.StructureRevision() != revision+1 || s.ScrollModel().Offset() != 0 || s.list.SelectedIndex() != 1 {
		t.Fatal("source switch skipped rebuilding or moved disarmed scroll/selection")
	}
	buf := surface.NewCellBuffer(24, 1)
	host.Layout()
	host.Paint(buf)
	if text := PlainRow(buf, 0, 24); !strings.Contains(text, "second source") || strings.Contains(text, "first source") {
		t.Fatalf("source switch painted stale text: %q", text)
	}
	row, ok := s.MessageAtLocalY(0)
	if !ok || row.Lines[0] != "second source" {
		t.Fatal("hit testing did not resolve the painted source")
	}
	host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	result := host.Handle(tea.MouseReleaseMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.Payload.(chatprojection.ChatTogglePayload).MessageID != "second-leaf" {
		t.Fatalf("source switch used stale disclosure: %+v", result.Action)
	}
	updated := append([]chatprojection.MessageRow(nil), second...)
	updated[0] = chatprojection.MessageRow{ID: "shared", Lines: []string{"latest delta"}}
	paint.Generation++
	paint.ChangedIndices = []int{0}
	revision = s.list.StructureRevision()
	s.SetMessages(updated, paint)
	// A detached snapshot can be reused by its owner; unchanged items must not
	// retain pointers into it after a replacement snapshot is published.
	second[1] = first[1]
	if s.list.StructureRevision() != revision {
		t.Fatal("stable-height replacement snapshot rebuilt the list")
	}
	buf = surface.NewCellBuffer(24, 1)
	host.Paint(buf)
	if text := PlainRow(buf, 0, 24); !strings.Contains(text, "latest delta") {
		t.Fatalf("replacement delta was not painted: %q", text)
	}
	s.ScrollModel().ScrollTo(1)
	buf = surface.NewCellBuffer(24, 1)
	host.Paint(buf)
	row, ok = s.MessageAtLocalY(0)
	if !ok || row.Lines[0] != "second tail" || !strings.Contains(PlainRow(buf, 0, 24), "second tail") {
		t.Fatal("unchanged item retained a detached snapshot instead of the latest rows")
	}
}

func TestSetMessagesReusesStorageAndClearsRemovedItems(t *testing.T) {
	s := New(theme.DefaultDark())
	rows := []chatprojection.MessageRow{{ID: "a", Lines: []string{"a"}}, {ID: "b", Lines: []string{"b"}}, {ID: "c", Lines: []string{"c"}}}
	s.SetMessages(rows, PaintParams{SourceID: 1, Generation: 1})
	items := s.list.Items()
	start := &s.paintState.globalStart[0]
	s.SetMessages(rows[:1], PaintParams{SourceID: 2, Generation: 1})
	if &s.list.Items()[0] != &items[0] || &s.paintState.globalStart[0] != start {
		t.Fatal("smaller source replaced reusable item/start storage")
	}
	for _, item := range items[1:] {
		if item.ID != "" || item.Payload != nil {
			t.Fatal("removed source retained item identity or payload references")
		}
	}
	s.SetMessages(nil, PaintParams{SourceID: 3, Generation: 1})
	if len(s.list.Items()) != 0 || len(s.paintState.globalStart) != 0 || s.ScrollModel().Content() != 0 || items[0].ID != "" {
		t.Fatal("empty source retained removed items or geometry")
	}
	s.SetMessages(rows, PaintParams{SourceID: 1, Generation: 1})
	if &s.list.Items()[0] != &items[0] || &s.paintState.globalStart[0] != start || s.ScrollModel().Content() != 3 {
		t.Fatal("regrowth failed to reuse and republish current source geometry")
	}
}
