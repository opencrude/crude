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

// A comparison shows the two versions and the decisions between them, not a
// second editor toolbar: no path row (the tab names the file), no Refresh,
// Find, Close or presentation buttons (their keys and the tab menu remain).
// One bar holds the state, the Keep buttons and Save, at any width.
func TestComparisonShowsOnlyVersionsAndDecisions(t *testing.T) {
	s := NewWithID(nil, "local/a")
	s.SetResolvable(true, diffview.ChoiceRight)
	s.SetPrepared(diffview.Prepare(diffview.Comparison{Path: "/work/README.md", BeforeLabel: "On disk", AfterLabel: "Your text", Before: "a\nb\n", After: "a\nB\n"}))
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(140, 16)
	h.Layout()
	h.Focus(s.DocumentID())
	buf := surface.NewCellBuffer(140, 16)
	h.Paint(buf)
	painted := paintedText(buf)
	rows := strings.Split(painted, "\n")
	for _, want := range []string{"1 change chosen", "Keep on disk", "Keep your text", "Keep both", "Save"} {
		if !strings.Contains(rows[0], want) {
			t.Fatalf("the first row must hold the state and decisions, missing %q:\n%s", want, painted)
		}
	}
	if !strings.Contains(rows[1], "On disk") || !strings.Contains(rows[1], "Your text") {
		t.Fatalf("the panes must start on the second row:\n%s", painted)
	}
	for _, unwanted := range []string{"/work/README.md", "Refresh", "Unified", "Find", "Close", "Open Source", "More"} {
		if strings.Contains(painted, unwanted) {
			t.Fatalf("duplicate chrome %q on the page:\n%s", unwanted, painted)
		}
	}
	r := h.Handle(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if r.Action == nil || r.Action.ID != ActionSave || r.Action.Payload != s {
		t.Fatalf("Ctrl+S must save the decided result for this page: %+v", r)
	}
	h.Handle(tea.KeyPressMsg{Code: '1', Text: "1"})
	if got, _ := s.viewer.ResolvedText(); got != "a\nb\n" {
		t.Fatalf("key 1 keeps the left side: %q", got)
	}
	// Tab reaches the decisions from the keyboard.
	h.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
	if h.Focused() == s.DocumentID() {
		t.Fatal("Tab must move into the decision bar")
	}
	// While the comparison updates, Save waits and the bar says why.
	s.SetLoading(true)
	h.Layout()
	h.Paint(buf)
	if got := paintedText(buf); !strings.Contains(strings.Split(got, "\n")[0], "Updating the comparison") {
		t.Fatalf("loading state missing from the bar:\n%s", got)
	}
	h.Focus(s.DocumentID())
	if r := h.Handle(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); r.Action != nil {
		t.Fatalf("Save must wait for the update: %+v", r)
	}
	// Narrow: the decisions keep the row, named by the sides alone; the
	// text and the navigation buttons (n/p remain) give way.
	s.SetLoading(false)
	h.Resize(48, 16)
	h.Layout()
	narrow := surface.NewCellBuffer(48, 16)
	h.Paint(narrow)
	first := strings.Split(paintedText(narrow), "\n")[0]
	for _, want := range []string{"[ On disk ]", "[ Your text ]", "[ Both ]", "Save"} {
		if !strings.Contains(first, want) {
			t.Fatalf("narrow bar lost %q: %q", want, first)
		}
	}
}

func TestFindNoMatchFeedbackAndF3Focus(t *testing.T) {
	s := New(nil)
	s.SetPrepared(diffview.Prepare(diffview.Comparison{After: "needle\nneedle"}))
	s.SetNotice("Refresh compares the latest versions")
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
	if command := s.viewer.SearchCommand(); command != nil {
		command().(diffview.SearchReadyMsg).Apply()
	}
	t.Cleanup(s.viewer.WaitSearch)
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
