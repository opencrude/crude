package localdiff

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"strings"
	"testing"
)

func TestNarrowActionsRemainReadableAndKeyboardAccessible(t *testing.T) {
	s := New(nil)
	s.SetPrepared(diffview.Prepare(diffview.Comparison{After: "original"}))
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(32, 20)
	h.Layout()
	buf := surface.NewCellBuffer(32, 20)
	h.Paint(buf)
	if !strings.Contains(paintedText(buf), "More") || strings.Contains(paintedText(buf), "Refresh") {
		t.Fatal("narrow row did not adapt")
	}
	h.Focus(s.actions.ID())
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	h.Layout()
	h.Paint(buf)
	for _, want := range []string{"Refresh", "Open Source", "Unified / Split", "Find", "Previous", "Next", "Close"} {
		if !strings.Contains(paintedText(buf), want) {
			t.Fatal(want)
		}
	}
	if h.Focused() != s.more.ID() {
		t.Fatal("More list not keyboard focused")
	}
	r := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if r.Action == nil || r.Action.ID != ActionRefresh || r.Action.Payload != s {
		t.Fatalf("list lost exact-owner action: %+v", r)
	}
	h.Layout()
	if s.moreOpen || h.Focused() != s.DocumentID() {
		t.Fatal("action list did not restore reader")
	}
}

func TestFindNoMatchFeedbackAndF3Focus(t *testing.T) {
	s := New(nil)
	s.SetPrepared(diffview.Prepare(diffview.Comparison{After: "needle\nneedle"}))
	s.SetNotice("Read-only snapshot · Refresh to compare the latest versions")
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(70, 20)
	h.Layout()
	h.Focus(s.DocumentID())
	h.Handle(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	h.Layout()
	for _, r := range "missing" {
		h.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	buf := surface.NewCellBuffer(70, 20)
	h.Paint(buf)
	if !strings.Contains(paintedText(buf), "No matches") {
		t.Fatal("no-match feedback absent")
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyF3})
	if h.Focused() != s.search.ID() {
		t.Fatal("F3 stole search focus")
	}
}
