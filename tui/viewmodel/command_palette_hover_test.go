package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCommandPalette_HoverSetsIdx(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 80
	m.height = 14
	m.screenX = 0
	m.screenY = 0
	_ = m.View(80) // populates lineToItem

	m.idx = 5
	// First item (composer, idx=0) is at contentLine=1.
	// localY = contentY(1) + 2 (prompt+divider) + visLine(1) = 4.
	msg := tea.MouseMotionMsg{X: 5, Y: 4}
	_ = m.Update(msg)
	if m.idx != 0 {
		t.Errorf("hovering first item should set idx=0, got %d", m.idx)
	}
}

func TestCommandPalette_HoverYOutsideList(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 80
	m.height = 14
	m.screenX = 0
	m.screenY = 0
	_ = m.View(80) // populates lineToItem

	m.idx = 3
	// Y=15 is past the content area height (14).
	msg := tea.MouseMotionMsg{X: 5, Y: 15}
	_ = m.Update(msg)
	if m.idx != 3 {
		t.Errorf("hovering outside palette should not change idx (got %d)", m.idx)
	}
}

func TestCommandPalette_HoverYOnSectionHeader(t *testing.T) {
	m := newCommandPaletteModel(testTheme(), nil)
	m.width = 80
	m.height = 14
	m.screenX = 0
	m.screenY = 0
	_ = m.View(80) // populates lineToItem

	m.idx = 3
	// The first section header ("  Tools") is at contentLine=0.
	// localY = contentY(1) + 2 (prompt+divider) + visLine(0) = 3.
	msg := tea.MouseMotionMsg{X: 5, Y: 3}
	_ = m.Update(msg)
	if m.idx != 3 {
		t.Errorf("hovering section header should not change idx (got %d)", m.idx)
	}
}
