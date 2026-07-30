package launcher

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func launcherContractHost(t *testing.T, width, height int, workspaces []workspace.WorkspaceInfo) (*Screen, *ui.Host) {
	t.Helper()
	s := New(theme.DefaultDark())
	s.SetData("/projects/current", workspaces)
	h := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(h.Focus)
	h.Resize(width, height)
	h.Layout()
	s.SyncFocus()
	return s, h
}

func manyWorkspaces(n int) []workspace.WorkspaceInfo {
	out := make([]workspace.WorkspaceInfo, n)
	for i := range out {
		out[i] = workspace.WorkspaceInfo{
			ProjectName: fmt.Sprintf("ws-%02d", i),
			ProjectDir:  fmt.Sprintf("/projects/ws-%02d", i),
		}
	}
	return out
}

// TestLauncherHostKeyboardContractTable documents Screen/Host keyboard behavior
// for fixed action rows, workspace list navigation, filter mode, and bounds.
func TestLauncherHostKeyboardContractTable(t *testing.T) {
	t.Run("filter_slash_activates_once", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		res := h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
		if !res.Handled || !s.FilterActive() || h.Focused() != SearchID {
			t.Fatalf("slash should activate filter: handled=%v active=%v focus=%q", res.Handled, s.FilterActive(), h.Focused())
		}
		h.Handle(tea.KeyPressMsg{Code: 'a', Text: "a"})
		if got := s.Query(); got != "a" {
			t.Fatalf("typed query=%q want a", got)
		}
		// Second slash is typed into the field, not a second activation.
		h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
		if got := s.Query(); got != "a/" {
			t.Fatalf("second slash should append to query, got %q", got)
		}
	})

	t.Run("backspace_on_empty_filter_closes", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, nil)
		h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
		res := h.Handle(tea.KeyPressMsg{Code: tea.KeyBackspace})
		if !res.Handled || s.FilterActive() {
			t.Fatal("backspace on empty filter should deactivate")
		}
	})

	t.Run("navigation_bounds_fixed_and_workspace_rows", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
			{ProjectName: "beta", ProjectDir: "/projects/beta"},
		})
		if got := s.SelectedItem(); got == nil || got.Kind != ItemNewSession {
			t.Fatalf("initial=%#v want NewSession", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
		if got := s.SelectedItem(); got == nil || got.Kind != ItemNewSession {
			t.Fatalf("up at top stays on hero: %#v", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := s.SelectedItem(); got == nil || got.Kind != ItemOpenFolder {
			t.Fatalf("down to open folder: %#v", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "alpha" {
			t.Fatalf("down to first workspace: %#v", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "beta" {
			t.Fatalf("end to last workspace: %#v", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
		if got := s.SelectedItem(); got == nil || got.Kind != ItemNewSession {
			t.Fatalf("home to first selectable: %#v", got)
		}
	})

	t.Run("pgup_pgdown_clamped", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "only", ProjectDir: "/projects/only"},
		})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := s.SelectedItem(); got == nil || got.Workspace == nil {
			t.Fatal("setup: workspace should be selected")
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyPgUp})
		if got := s.SelectedItem(); got == nil || got.Kind != ItemNewSession {
			t.Fatalf("pgup jumps toward top: %#v", got)
		}
		h.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
		if got := s.SelectedItem(); got == nil || got.Workspace == nil {
			t.Fatalf("pgdown toward workspace: %#v", got)
		}
	})

	t.Run("workspace_list_scroll_offset", func(t *testing.T) {
		s, h := launcherContractHost(t, 110, 16, manyWorkspaces(30))
		h.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
		if s.ListScrollOffset() == 0 {
			t.Fatal("end on long list should scroll workspace viewport")
		}
		before := s.ListScrollOffset()
		h.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
		if s.ListScrollOffset() >= before {
			t.Fatalf("home should reduce scroll offset: before=%d after=%d", before, s.ListScrollOffset())
		}
	})

	t.Run("enter_activates_exactly_once", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, nil)
		activations := 0
		for i := 0; i < 2; i++ {
			res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if res.Cmd == nil {
				t.Fatal("Enter should emit ActivateMsg cmd")
			}
			msg, ok := res.Cmd().(ActivateMsg)
			if !ok || msg.Screen != s || msg.Kind != ItemNewSession {
				t.Fatalf("activation=%#v", res.Cmd())
			}
			activations++
		}
		if activations != 2 {
			t.Fatalf("activation count=%d", activations)
		}
	})

	t.Run("esc_clears_filter_then_noop", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
		if s.FilterActive() {
			t.Fatal("first Esc should clear filter")
		}
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "alpha" {
			t.Fatalf("Esc after filter clear keeps prior workspace selection: %#v", got)
		}
		res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
		if !res.Handled || s.FilterActive() {
			t.Fatal("second Esc is consumed noop; launcher stays open")
		}
	})

	t.Run("ctrl_c_never_navigates", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
		})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		before := s.SelectedItem()
		res := h.Handle(tea.KeyPressMsg{Text: "ctrl+c", Mod: tea.ModCtrl, Code: 'c'})
		if !res.Handled {
			t.Fatal("Ctrl+C should be consumed by launcher screen")
		}
		if got := s.SelectedItem(); got == nil || before == nil || got.Kind != before.Kind {
			t.Fatalf("Ctrl+C must not change selection: before=%#v after=%#v", before, got)
		}
	})

	t.Run("filter_keeps_fixed_action_rows", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha"},
			{ProjectName: "beta", ProjectDir: "/projects/beta"},
		})
		h.Handle(tea.KeyPressMsg{Code: '/', Text: "/"})
		h.Handle(tea.KeyPressMsg{Code: 'b', Text: "b"})
		if s.TotalItems() != 4 {
			t.Fatalf("filter must not remove fixed rows, items=%d", s.TotalItems())
		}
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "beta" {
			t.Fatalf("filtered selection=%#v", got)
		}
	})

	t.Run("d_on_workspace_row_emits_delete_once", func(t *testing.T) {
		s, h := launcherContractHost(t, 100, 24, []workspace.WorkspaceInfo{
			{ProjectName: "alpha", ProjectDir: "/projects/alpha", ID: "ws-alpha"},
			{ProjectName: "beta", ProjectDir: "/projects/beta", ID: "ws-beta"},
		})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "alpha" {
			t.Fatalf("setup: workspace alpha should be selected, got %#v", got)
		}
		beforeItems := s.TotalItems()
		res := h.Handle(tea.KeyPressMsg{Code: 'd', Text: "d"})
		if !res.Handled || res.Cmd == nil {
			t.Fatal("d on workspace row must be handled with DeleteMsg cmd")
		}
		msg, ok := res.Cmd().(DeleteMsg)
		if !ok || msg.Kind != ItemWorkspace || msg.WorkspaceID != "ws-alpha" || msg.Screen != s {
			t.Fatalf("DeleteMsg=%#v", res.Cmd())
		}
		if s.TotalItems() != beforeItems-1 {
			t.Fatalf("delete should remove one in-memory workspace row: before=%d after=%d", beforeItems, s.TotalItems())
		}
		if got := s.SelectedItem(); got == nil || got.Workspace == nil || got.Workspace.Name != "beta" {
			t.Fatalf("selection after delete=%#v want beta workspace", got)
		}
		res2 := h.Handle(tea.KeyPressMsg{Code: 'd', Text: "d"})
		if res2.Cmd != nil {
			if msg2, ok := res2.Cmd().(DeleteMsg); ok && msg2.WorkspaceID == "ws-alpha" {
				t.Fatal("second d must not re-emit delete for removed workspace")
			}
		}
	})
}
