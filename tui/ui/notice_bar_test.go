package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

// A click on a bar button reaches the owner with the owner's payload, never
// the button's label, so one handler can act on the right view.
func TestNoticeBarClickCarriesOwnerPayload(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	bar := NewNoticeBar("bar", tokens)
	owner := &struct{ name string }{"editor"}
	bar.Set("Changed on disk. Conflicts with your unsaved edits.", ColorWarning, owner,
		ActionBarButton{Label: "Compare", Action: "compare", Enabled: true},
		ActionBarButton{Label: "Discard", Action: "discard", Enabled: true})
	h := NewHost(theme.DefaultDark(), bar)
	h.Resize(90, 1)
	h.Layout()
	buf := surface.NewCellBuffer(90, 1)
	h.Paint(buf)
	row := noticeRow(buf, 90)
	x := strings.Index(row, "[ Discard ]")
	if x < 0 || !strings.HasPrefix(row, "! Changed on disk.") {
		t.Fatalf("bar paints %q", row)
	}
	res := h.Handle(tea.MouseClickMsg{X: len([]rune(row[:x])) + 2, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "discard" || res.Action.Payload != owner {
		t.Fatalf("click must carry the owner's payload: %+v", res.Action)
	}
	// Narrow: the buttons keep their room; the message is cut.
	h.Resize(40, 1)
	h.Layout()
	narrow := surface.NewCellBuffer(40, 1)
	h.Paint(narrow)
	row = noticeRow(narrow, 40)
	if !strings.Contains(row, "Compare") || !strings.Contains(row, "Discard") {
		t.Fatalf("narrow bar lost a decision: %q", row)
	}
}

func noticeRow(buf *surface.CellBuffer, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		if c := buf.Get(x, 0).Content; c != "" {
			b.WriteString(c)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
