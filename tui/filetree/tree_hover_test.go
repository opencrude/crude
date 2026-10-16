package filetree

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// TestHover_SetsIndexOnMotion verifies that a MouseMotionMsg with a
// Y coordinate inside the visible range updates Hover.Index to point
// at the corresponding visible node.
func TestHover_SetsIndexOnMotion(t *testing.T) {
	// Build a small tree.
	root := &TreeNode{
		Name:       "root",
		Path:       "/root",
		IsDir:      true,
		IsExpanded: true, // needed so children appear in VisibleNodes
		Children: []*TreeNode{
			{Name: "a.txt", Path: "/root/a.txt"},
			{Name: "b.txt", Path: "/root/b.txt"},
			{Name: "c.txt", Path: "/root/c.txt"},
		},
	}
	for _, c := range root.Children {
		c.Parent = root
	}
	m := &FileTreeModel{
		Root:          root,
		nodes:         map[string]*TreeNode{root.Path: root},
		showHidden:    false,
		visibleHeight: 10,
		Hover:         ui.NewHover(),
	}
	m.rebuildVisibleNodes()

	if got := len(m.VisibleNodes()); got != 3 {
		t.Fatalf("setup: VisibleNodes len = %d, want 3 (children only)", got)
	}

	// No hover initially.
	if m.Hover.Index != -1 {
		t.Fatalf("initial hover index = %d, want -1", m.Hover.Index)
	}

	// Mouse over row 1 (should be b.txt, visible node index 1).
	changed := m.handleMouseMotion(tea.MouseMotionMsg{X: 5, Y: 1})
	if !changed {
		t.Fatal("expected hover state to change on first motion into a row")
	}
	if m.Hover.Index != 1 {
		t.Fatalf("hover index after motion = %d, want 1", m.Hover.Index)
	}

	// Motion over the same row: no change.
	changed = m.handleMouseMotion(tea.MouseMotionMsg{X: 5, Y: 1})
	if changed {
		t.Fatal("expected no change when motion stays in the same row")
	}

	// Motion to row 2: change.
	changed = m.handleMouseMotion(tea.MouseMotionMsg{X: 5, Y: 2})
	if !changed {
		t.Fatal("expected hover state to change on motion between rows")
	}
	if m.Hover.Index != 2 {
		t.Fatalf("hover index = %d, want 2", m.Hover.Index)
	}
}

// TestHover_ClearsOnOutOfRange verifies that motion outside the
// visible range clears the hover state, the GUI equivalent of
// "the cursor left the list".
func TestHover_ClearsOnOutOfRange(t *testing.T) {
	root := &TreeNode{
		Name:       "root",
		Path:       "/root",
		IsDir:      true,
		IsExpanded: true,
		Children: []*TreeNode{
			{Name: "a.txt", Path: "/root/a.txt"},
		},
	}
	root.Children[0].Parent = root
	m := &FileTreeModel{
		Root:          root,
		nodes:         map[string]*TreeNode{root.Path: root},
		visibleHeight: 5,
		Hover:         ui.NewHover(),
	}
	m.rebuildVisibleNodes()

	m.Hover.Set(0)
	if !m.Hover.IsActive() {
		t.Fatal("setup: hover should be active")
	}

	// Y beyond the visible height: clear.
	changed := m.handleMouseMotion(tea.MouseMotionMsg{X: 0, Y: 10})
	if !changed {
		t.Fatal("expected hover to clear when mouse moves below the tree")
	}
	if m.Hover.IsActive() {
		t.Fatal("hover should be cleared after motion out of range")
	}
}
