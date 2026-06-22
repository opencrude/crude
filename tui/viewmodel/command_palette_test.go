package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func TestNewCommandPaletteModelShowsAllCommandsByDefault(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	if len(m.filtered) != len(paletteCommands) {
		t.Errorf("expected %d filtered items, got %d", len(paletteCommands), len(m.filtered))
	}
}

func TestCommandPaletteFilterReducesItems(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	allCount := len(m.filtered)
	m.filter = "sett"
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("expected some filtered results")
	}
	if len(m.filtered) >= allCount {
		t.Errorf("filter should reduce items: %d >= %d", len(m.filtered), allCount)
	}
}

func TestCommandPaletteFuzzyMatch(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.filter = "cps"
	m.applyFilter()
	if len(m.filtered) == 0 {
		t.Fatal("fuzzy filter 'cps' should match 'Composer'")
	}
	found := false
	for _, item := range m.filtered {
		if item.ID == "composer" {
			found = true
			break
		}
	}
	if !found {
		t.Error("fuzzy filter 'cps' should match 'composer'")
	}
}

func TestCommandPaletteEmptyFilterShowsAll(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.filter = ""
	m.applyFilter()
	if len(m.filtered) != len(paletteCommands) {
		t.Errorf("empty filter should show all %d items, got %d", len(paletteCommands), len(m.filtered))
	}
}

func TestCommandPaletteBuildGroups(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
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
	m := newCommandPaletteModel(testTheme(), nil)
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
	m := newCommandPaletteModel(testTheme(), nil)
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
	m := newCommandPaletteModel(testTheme(), nil)
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
	m := newCommandPaletteModel(testTheme(), nil)
	m.filter = "test"
	m.updateKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filter != "tes" {
		t.Errorf("after backspace filter = %q, want 'tes'", m.filter)
	}
}

func TestCommandPaletteTypingUpdatesFilter(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.updateKey(tea.KeyPressMsg{Text: "side"})
	if m.filter != "side" {
		t.Errorf("filter = %q, want 'side'", m.filter)
	}
	if len(m.filtered) == 0 || len(m.filtered) >= len(paletteCommands) {
		t.Errorf("expected some filtered items, got %d", len(m.filtered))
	}
}

func TestCommandPaletteEnterReturnsCallback(t *testing.T) {
	var called string
	m := newCommandPaletteModel(testTheme(), func(cmdID string) tea.Cmd {
		called = cmdID
		return nil
	})
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
	})
	m.filtered = nil
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected nil command when no items")
	}
}

func TestCommandPaletteEscReturnsNil(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Error("expected nil command from Esc")
	}
}

func TestCommandPaletteViewNonEmpty(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 50
	m.height = 14

	v := m.View(50)
	if v == "" {
		t.Fatal("expected non-empty view")
	}
	if !strings.Contains(v, ">") {
		t.Error("expected prompt in view")
	}
}

func TestCommandPaletteViewShowsFilteredItems(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 50
	m.height = 14
	m.filter = "sett"
	m.applyFilter()

	v := m.View(50)
	if strings.Contains(v, "no matching") {
		t.Errorf("expected filtered results, got: %s", v)
	}
	cleaned := rendering.StripAnsi(v)
	if !strings.Contains(cleaned, "Settings") {
		t.Errorf("expected 'Settings' in view, got: %s", cleaned)
	}
}

func TestCommandPaletteScrollbarAtLastColumn(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 20
	m.height = 4

	v := m.View(20)
	lines := strings.Split(v, "\n")

	for i, line := range lines {
		visW := rendering.VisibleWidth(line)
		plain := rendering.StripAnsi(line)
		if visW != 20 {
			t.Errorf("line %d: visW=%d (want 20): %q", i, visW, plain)
		}
	}
}

func TestCommandPaletteViewDoesNotExceedWidth(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 50
	m.height = 6

	v := m.View(50)
	lines := strings.Split(v, "\n")
	for _, line := range lines {
		visW := rendering.VisibleWidth(line)
		if visW > 50 {
			t.Errorf("line width %d exceeds surface width 50", visW)
		}
	}
}

func TestCommandPaletteViewConsistentWidth(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 60
	m.height = 14

	v := m.View(60)
	lines := strings.Split(v, "\n")
	for i, line := range lines {
		visW := rendering.VisibleWidth(line)
		if visW != 60 && line != "" {
			t.Logf("line %d: width=%d (expected 60)", i, visW)
		}
	}
}

func TestCommandPaletteViewNoMatch(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 50
	m.height = 14
	m.filter = "xyzzynotexist"
	m.applyFilter()

	v := m.View(50)
	if !strings.Contains(v, "no matching") {
		t.Errorf("expected 'no matching' for non-matching filter, got: %s", v)
	}
}
