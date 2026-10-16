package sessionpicker

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/theme"
	"crude/tui/ui"
)

func twoSessions() []sessiondb.SessionMeta {
	now := time.Now().UnixNano()
	return []sessiondb.SessionMeta{
		{ID: "a", ProjectName: "alpha", Model: "gpt", TurnCount: 1, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "alpha task", Valid: true}},
		{ID: "b", ProjectName: "beta", Model: "claude", TurnCount: 2, CreatedAt: now,
			FirstPrompt: sql.NullString{String: "beta work", Valid: true}},
	}
}

// The whole point of the redesign: the search row is visible (and clickable)
// the moment the picker opens; no hidden "/" shortcut required.
func TestSearchRowAlwaysVisibleAndPainted(t *testing.T) {
	screen, host := pickerHost(t, twoSessions())
	buf := paintHost(host, 72, 14)
	text := bufferText(buf)
	for _, want := range []string{"Type to search", "type to filter", "Enter resume"} {
		if !strings.Contains(text, want) {
			t.Fatalf("paint missing %q:\n%s", want, text)
		}
	}
	b := screen.search.Bounds()
	if b.Y != 1 || b.W <= 0 || b.H != 1 {
		t.Fatalf("search row bounds = %#v, want Y=1 H=1 with real width", b)
	}
}

// Opening into the list is the old flow; opening now means typing filters
// immediately. (openSessionPicker calls FocusSearch after wiring focus.)
func TestTypingFiltersImmediatelyAfterOpen(t *testing.T) {
	screen, host := pickerHost(t, twoSessions())
	screen.FocusSearch()

	host.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
	host.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	host.Layout()
	text := bufferText(paintHost(host, 72, 14))
	if !strings.Contains(text, "beta work") || strings.Contains(text, "alpha task") {
		t.Fatalf("typing did not filter live:\n%s", text)
	}
	if !strings.Contains(text, "Past sessions (1/2)") {
		t.Fatalf("title should show matched/total while filtering:\n%s", text)
	}

	// Focus stayed in the field: Esc clears the query instead of closing.
	if res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); res.Cmd != nil {
		t.Fatal("Esc with a query must clear it, not emit a close command")
	}
	if len(screen.filtered) != 2 || screen.search.field.Value() != "" {
		t.Fatalf("query not cleared: filtered=%d value=%q", len(screen.filtered), screen.search.field.Value())
	}
	cancel := commandMessage[CancelMsg](t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if cancel.Target != screen {
		t.Fatal("second Esc should close the window")
	}
}

// Down leaves the field and enters the list in one keypress; letters stop
// editing the query once the list owns focus.
func TestDownFromSearchMovesIntoList(t *testing.T) {
	screen, host := pickerHost(t, twoSessions())
	screen.FocusSearch()

	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	meta, ok := screen.Selected()
	if !ok || meta.ID != "b" {
		t.Fatalf("handoff selection = %#v, want b", meta)
	}

	host.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if screen.filterText != "" || len(screen.filtered) != 2 {
		t.Fatal("letters must reach the list, not the query, after handoff")
	}

	// Up cycles: b → a (top row), then another Up refocuses the search field.
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	focused := false
	screen.SetFocusHandler(func(id ui.ComponentID) {
		if id == SearchID {
			focused = true
		}
	})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if !focused {
		t.Fatal("Up past the first row should refocus the search field")
	}
}

// Multi-term queries AND-match across fields (title words + model + project…).
func TestMultiTermQueryMatchesAcrossFields(t *testing.T) {
	now := time.Now().UnixNano()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open([]sessiondb.SessionMeta{
		{ID: "web", ProjectName: "webapp", Model: "claude", CreatedAt: now,
			FirstPrompt: sql.NullString{String: "build login page", Valid: true}},
		{ID: "api", ProjectName: "api", Model: "gpt", CreatedAt: now,
			FirstPrompt: sql.NullString{String: "build beta endpoint", Valid: true}},
	})

	cases := []struct {
		query string
		want  []string
	}{
		{"beta gpt", []string{"api"}},
		{"login claude", []string{"web"}},
		{"build", []string{"web", "api"}},
		{"nope gpt", nil},
		{"   ", []string{"web", "api"}},
	}
	for _, tc := range cases {
		screen.applyFilterValue(tc.query)
		var got []string
		for _, ses := range screen.filtered {
			got = append(got, ses.ID)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Fatalf("query %q: got %v want %v", tc.query, got, tc.want)
		}
	}
}

// REGRESSION (user report): while any listed agent is working, the spinner
// tick rebuilds every row and previously snapped the viewport back toward the
// top ~10x/second, making scrolling unreliable. Ticks must leave both the
// scroll offset and the selection untouched.
func TestSpinnerResyncPreservesScrollViewport(t *testing.T) {
	now := time.Now().UnixNano()
	n := 40
	sessions := make([]sessiondb.SessionMeta, n)
	for i := range sessions {
		sessions[i] = sessiondb.SessionMeta{
			ID:          fmt.Sprintf("s%02d", i),
			ProjectName: fmt.Sprintf("proj%02d", i),
			TurnCount:   i + 1,
			CreatedAt:   now,
			FirstPrompt: sql.NullString{String: fmt.Sprintf("task number %02d", i), Valid: true},
		}
	}

	th := theme.DefaultDark()
	screen := New(th)
	screen.Open(sessions)
	screen.SetLiveInfo(map[string]LiveInfo{"s00": {Status: StatusWorking}})

	component := ui.NewComponentWidget(th, screen, 60, 16)
	screen.SetFocusHandler(component.Focus)
	screen.FocusList()
	host := component.Host()
	host.Resize(60, 16)
	host.Layout()

	// Scroll to the bottom and park the selection on the last row.
	lastIdx := -1
	for i, item := range screen.list.list.Items() {
		if item.ID == "s39" {
			lastIdx = i
		}
	}
	if lastIdx < 0 {
		t.Fatal("missing last row")
	}
	screen.list.list.SelectIndex(lastIdx)
	screen.list.list.Scroll().ScrollTo(screen.list.list.Scroll().MaxOffset())
	bottom := screen.list.list.Scroll().Offset()
	if bottom == 0 {
		t.Fatal("precondition: expected a scrolled-down viewport")
	}

	// One tick in the viewmodel runs SetSpinFrame + SetLiveInfo back-to-back;
	// reproduce several of them.
	for tick := 0; tick < 3; tick++ {
		screen.SetSpinFrame(100 * (tick + 1))
		screen.SetLiveInfo(map[string]LiveInfo{"s00": {Status: StatusWorking}})
		if off := screen.list.list.Scroll().Offset(); off != bottom {
			t.Fatalf("tick %d scrolled the list: %d → %d", tick, bottom, off)
		}
		meta, ok := screen.Selected()
		if !ok || meta.ID != "s39" {
			t.Fatalf("tick %d moved the selection: %#v", tick, meta)
		}
	}
}
