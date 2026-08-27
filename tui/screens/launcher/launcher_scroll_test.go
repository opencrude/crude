package launcher

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/workspace"
)

// TestLauncherListOffsetSurvivesRelayout guards against scroll snap-back in
// the recent-workspaces list: the offset produced by wheel scrolling must not
// be yanked back to the selection by the next per-frame relayout. The
// launcher opts out of selection-anchored scroll for exactly this reason —
// every frame re-runs Layout, and anchoring used to undo wheel gestures
// within one tick whenever the selected row left the viewport.
func TestLauncherListOffsetSurvivesRelayout(t *testing.T) {
	s, h := launcherContractHost(t, 110, 16, manyWorkspaces(30))
	if s.list.list.SelectionAnchorsScroll() {
		t.Fatal("launcher list must not anchor scroll to selection (per-frame Layout would snap back)")
	}
	listRect, ok := h.ScreenRect(WorkspaceListID)
	if !ok || listRect.Empty() {
		t.Fatal("workspace list must be mounted with geometry")
	}

	before := s.ListScrollOffset()
	h.Handle(tea.MouseWheelMsg{X: listRect.X + 1, Y: listRect.Y + 1, Button: tea.MouseWheelDown})
	afterWheel := s.ListScrollOffset()
	if afterWheel <= before {
		t.Fatalf("wheel did not scroll: before=%d after=%d", before, afterWheel)
	}

	// Simulate fresh shell frames: Layout, MarkLayout, Layout.
	h.Layout()
	h.MarkLayout()
	h.Layout()
	if got := s.ListScrollOffset(); got != afterWheel {
		t.Fatalf("SNAP-BACK: offset %d after wheel became %d after relayout", afterWheel, got)
	}
}

// TestLauncherKeyboardNavRevealsSelection verifies that keyboard-driven
// selection moves reveal the selected row now that per-frame anchoring is
// disabled: hover and wheel never re-anchor, but up/down/pgup/pgdn/home/end
// must still scroll the row into view.
func TestLauncherKeyboardNavRevealsSelection(t *testing.T) {
	s, h := launcherContractHost(t, 110, 16, manyWorkspaces(30))
	// 30 workspaces in a short viewport: walking down must scroll.
	for i := 0; i < 12; i++ {
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if it := s.SelectedItem(); it == nil || it.Workspace == nil {
		t.Fatalf("expected a workspace selected, got %#v", s.SelectedItem())
	}
	vh := s.list.list.Scroll().Viewport()
	off := s.ListScrollOffset()
	cursor := s.list.list.SelectedIndex()
	if cursor < off || cursor >= off+vh {
		t.Fatalf("selected row %d not visible in viewport [%d,%d)", cursor, off, off+vh)
	}

	h.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if max := s.list.list.Scroll().MaxOffset(); s.ListScrollOffset() != max {
		t.Fatalf("End should scroll to max: off=%d max=%d", s.ListScrollOffset(), max)
	}
}

// TestLauncherWorkspaceItemAt covers the row hit-test used by the right-click
// context menu: visible rows map to their entries, empty space returns nil.
func TestLauncherWorkspaceItemAt(t *testing.T) {
	s, _ := launcherContractHost(t, 110, 16, manyWorkspaces(4))

	if got := s.WorkspaceItemAt(0); got == nil || got.Name != "ws-00" {
		t.Fatalf("row 0 = %#v, want ws-00", got)
	}
	if got := s.WorkspaceItemAt(2); got == nil || got.Name != "ws-02" {
		t.Fatalf("row 2 = %#v, want ws-02", got)
	}
	if got := s.WorkspaceItemAt(50); got != nil {
		t.Fatalf("row past the last item must be nil, got %#v", got)
	}
	if got := s.WorkspaceItemAt(-1); got != nil {
		t.Fatalf("negative row must be nil, got %#v", got)
	}
}

// TestLauncherRemoveWorkspace covers the context-menu removal primitive:
// matching by ID, path fallback for legacy entries without an ID, and
// no-ops for unknown workspaces.
func TestLauncherRemoveWorkspace(t *testing.T) {
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", []workspace.WorkspaceInfo{
		{ID: "id-a", ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		{ID: "id-b", ProjectName: "beta", ProjectDir: "/projects/beta"},
		{ID: "id-c", ProjectName: "gamma", ProjectDir: "/projects/gamma"},
	})

	if !s.RemoveWorkspace("id-b", "/projects/beta") {
		t.Fatal("RemoveWorkspace should report a successful removal")
	}
	if got := s.TotalItems(); got != 4 { // 2 fixed actions + 2 workspaces
		t.Fatalf("TotalItems=%d want 4 after removal", got)
	}
	for i := 2; i < len(s.items); i++ {
		if s.items[i].Workspace != nil && s.items[i].Workspace.Name == "beta" {
			t.Fatal("beta should no longer be listed")
		}
	}

	// Path fallback for legacy entries without an ID.
	if !s.RemoveWorkspace("", "/projects/alpha") {
		t.Fatal("path fallback should remove the entry")
	}
	if got := s.TotalItems(); got != 3 {
		t.Fatalf("TotalItems=%d want 3 after path-fallback removal", got)
	}

	if s.RemoveWorkspace("missing", "/nowhere") {
		t.Fatal("unknown workspace must not report a removal")
	}
}
