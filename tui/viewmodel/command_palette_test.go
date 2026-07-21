package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widgets"
)

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func TestNewCommandPaletteModelShowsAllCommandsByDefault(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	m := newCommandPaletteModel(testTheme(), nil, reg)
	if len(m.filtered) != len(entries) {
		t.Errorf("expected %d filtered items, got %d", len(entries), len(m.filtered))
	}
}

func TestCommandPaletteFilterReducesItems(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	allCount := len(m.filtered)
	m.setFilter("sett")
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("expected some filtered results")
	}
	if len(m.filtered) >= allCount {
		t.Errorf("filter should reduce items: %d >= %d", len(m.filtered), allCount)
	}
}

func TestCommandPaletteFuzzyMatch(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	m.setFilter("cps")
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("fuzzy filter 'cps' should match 'Composer'")
	}
	found := false
	for _, item := range m.filtered {
		if item.ID == "composer.toggle" {
			found = true
			break
		}
	}
	if !found {
		t.Error("fuzzy filter 'cps' should match 'composer.toggle'")
	}
}

func TestCommandPaletteEmptyFilterShowsAll(t *testing.T) {
	reg := ui.DefaultRegistry()
	entries := paletteEntriesFromRegistry(reg)
	m := newCommandPaletteModel(testTheme(), nil, reg)
	m.setFilter("")
	m.applyFilter()
	if len(m.filtered) != len(entries) {
		t.Errorf("empty filter should show all %d items, got %d", len(entries), len(m.filtered))
	}
}

func TestCommandPaletteBuildGroups(t *testing.T) {
	reg := ui.DefaultRegistry()
	m := newCommandPaletteModel(testTheme(), nil, reg)
	if len(m.groups) == 0 {
		t.Fatal("expected at least one group")
	}
	totalItems := 0
	for _, g := range m.groups {
		if g.Count <= 0 {
			t.Errorf("group %q has count %d", g.Category, g.Count)
		}
		totalItems += g.Count
	}
	if totalItems != len(m.filtered) {
		t.Errorf("group item count %d != filtered items %d", totalItems, len(m.filtered))
	}
}

func TestCommandPaletteNavigationArrows(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	if m.idx != 0 {
		t.Errorf("initial idx = %d, want 0", m.idx)
	}

	m.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.idx != 1 {
		t.Errorf("after Down idx = %d, want 1", m.idx)
	}

	m.updateKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.idx != 0 {
		t.Errorf("after Up idx = %d, want 0", m.idx)
	}
}

func TestCommandPaletteNavigationWrapsNot(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.idx = 0
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.idx != 0 {
		t.Errorf("Up at top should not wrap, got idx=%d", m.idx)
	}

	m.idx = len(m.filtered) - 1
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.idx != len(m.filtered)-1 {
		t.Errorf("Down at bottom should not wrap, got idx=%d", m.idx)
	}
}

func TestCommandPalettePageUpDown(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.height = 10
	m.idx = 5
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.idx < 0 || m.idx > 5 {
		t.Errorf("PgUp from 5 should decrease idx, got %d", m.idx)
	}

	m.idx = 5
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.idx <= 5 || m.idx >= len(m.filtered) {
		t.Errorf("PgDown from 5 should increase idx, got %d", m.idx)
	}
}

func TestCommandPaletteBackspaceRemovesChar(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.setFilter("test")
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filterText() != "tes" {
		t.Errorf("after backspace filter = %q, want 'tes'", m.filterText())
	}
}

func TestCommandPaletteTypingUpdatesFilter(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.updateKey(tea.KeyPressMsg{Text: "side"})
	if m.filterText() != "side" {
		t.Errorf("filter = %q, want 'side'", m.filterText())
	}
	if len(m.filtered) == 0 || len(m.filtered) >= len(paletteEntriesFromRegistry(ui.DefaultRegistry())) {
		t.Errorf("expected some filtered items, got %d", len(m.filtered))
	}
}

func TestCommandPaletteEnterReturnsCallback(t *testing.T) {
	var called string
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		called = cmdID
		return nil
	}, ui.DefaultRegistry())
	m.idx = 0
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if called == "" {
		t.Error("expected callback to be called on Enter")
	}
	if cmd != nil {
		t.Log("got expected non-nil command from Enter")
	}
}

func TestCommandPaletteEnterOnEmptyFilterReturnsNil(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		return nil
	}, ui.DefaultRegistry())
	m.filtered = nil
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected nil command when no items")
	}
}

func TestCommandPaletteEscReturnsNil(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Error("expected nil command from Esc")
	}
}

// ── Frame-based tests ─────────────────────────────────────────────────────

func TestBuildCommandPaletteFrame_ListHasAllItems(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	list := widgets.CommandPaletteList(cw)
	if list == nil {
		t.Fatal("expected list component in frame")
	}
	if len(list.Items()) == 0 {
		t.Error("expected non-empty list items")
	}
}

func TestBuildCommandPaletteFrame_SearchFieldPresent(t *testing.T) {
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 60, Height: 14,
	})
	search := widgets.CommandPaletteSearch(cw)
	if search == nil {
		t.Fatal("expected search field in frame")
	}
}

func TestCommandPaletteFrame_WidthConsistent(t *testing.T) {
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 50, Height: 14,
	})
	w, _ := cw.Measure(80, 24)
	if w > 80 {
		t.Errorf("frame width %d exceeds available 80", w)
	}
}

func TestCommandPaletteListItemsHasHeaders(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	items := m.listItems()
	hasHeader := false
	hasSeparator := false
	for _, it := range items {
		if it.Header {
			hasHeader = true
		}
		if it.Separator {
			hasSeparator = true
		}
	}
	if !hasHeader {
		t.Error("expected category headers in list items")
	}
	// Separators appear between groups
	if len(m.groups) > 1 && !hasSeparator {
		t.Error("expected separators between groups")
	}
}

func TestCommandPaletteListItemsFilterReduces(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil, ui.DefaultRegistry())
	m.setFilter("sett")
	m.applyFilter()
	items := m.listItems()
	if len(items) == 0 {
		t.Fatal("expected filtered list items")
	}
	// All selectable items should have "Settings" or related label
	found := false
	for _, it := range items {
		if !it.Selectable() {
			continue
		}
		if strings.Contains(it.Label, "Settings") || strings.Contains(it.Label, "settings") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Settings' in filtered list items")
	}
}

func TestCommandPaletteViewWidthDoesNotExceed(t *testing.T) {
	// Verify the frame's measured width doesn't exceed constraints
	cw := widgets.BuildCommandPaletteFrame(testTheme(), widgets.CommandPaletteSpec{
		Items: []ui.ListItem{{ID: "x", Label: "X"}},
		Width: 50, Height: 6,
	})
	w, h := cw.Measure(80, 24)
	if w > 80 {
		t.Errorf("width %d exceeds 80", w)
	}
	if h > 24 {
		t.Errorf("height %d exceeds 24", h)
	}
	_ = rendering.VisibleWidth("") // keep import
}
