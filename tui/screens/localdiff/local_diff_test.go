package localdiff

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"errors"
	"strings"
	"testing"
)

func paintedText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Content != "" {
				b.WriteString(c.Content)
			} else if c.Width > 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func TestLocalDiffExactOwnerActionsAndSnapshotStates(t *testing.T) {
	s := NewWithID(nil, "local/a")
	s.SetPrepared(diffview.Prepare(diffview.Comparison{Path: "path.go", BeforeLabel: "Saved revision 1", AfterLabel: "Buffer revision 9", Before: "original\n", After: "changed\n"}))
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(140, 20)
	h.Layout()
	h.Focus(s.DocumentID())
	buf := surface.NewCellBuffer(140, 20)
	h.Paint(buf)
	for _, want := range []string{"path.go", "Saved revision 1", "Buffer revision 9", "original", "changed"} {
		if !strings.Contains(paintedText(buf), want) {
			t.Fatal(want)
		}
	}
	for _, action := range []string{ActionRefresh, ActionOpenSource, ActionClose} {
		r := s.HandleAction(ui.EventContext{}, ui.Action{ID: action})
		if r.Action == nil || r.Action.Payload != s {
			t.Fatalf("wrong owner %s: %+v", action, r)
		}
	}
	s.SetLoading(true)
	h.Paint(buf)
	if got := paintedText(buf); !strings.Contains(got, "Preparing comparison") || !strings.Contains(got, "changed") {
		t.Fatal("loading erased old snapshot")
	}
	s.SetError(errors.New("source unavailable"))
	h.Paint(buf)
	if got := paintedText(buf); !strings.Contains(got, "source unavailable") || !strings.Contains(got, "original") {
		t.Fatal("error erased evidence")
	}
	s.SetError(nil)
	s.SetNotice("Snapshot changed; refresh to compare current versions")
	h.Paint(buf)
	if !strings.Contains(paintedText(buf), "Snapshot changed") {
		t.Fatal("missing stale notice")
	}
	h.Handle(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	h.MarkLayout()
	h.Layout()
	if h.Focused() != s.search.ID() {
		t.Fatalf("search focus=%s", h.Focused())
	}
	for _, r := range "changed" {
		h.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEscape}) // clear retained search selection first
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	h.MarkLayout()
	h.Layout()
	if h.Focused() != s.DocumentID() {
		t.Fatal("search escape lost reader focus")
	}
}
