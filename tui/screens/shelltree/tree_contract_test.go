package shelltree

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/theme"
	"crude/tui/ui"
)

type treePaths struct {
	root string
	pkg  string
	main string
	util string
}

// treeFixture builds a small workspace: main.go plus pkg/util.go.
func treeFixture(t *testing.T) (*filetree.FileTreeModel, treePaths) {
	t.Helper()
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.go")
	utilPath := filepath.Join(pkg, "util.go")
	for _, spec := range []struct {
		path string
		body string
	}{
		{mainPath, "package main\n"},
		{utilPath, "package pkg\n"},
	} {
		if err := os.WriteFile(spec.path, []byte(spec.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filetree.NewFileTreeModel(dir), treePaths{
		root: dir,
		pkg:  pkg,
		main: mainPath,
		util: utilPath,
	}
}

func setupTreeHost(t *testing.T, model *filetree.FileTreeModel) *ui.Host {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th, model)
	host := ui.NewHost(th, screen)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(TreeID)
	return host
}

func pressKey(host *ui.Host, key tea.KeyPressMsg) {
	host.Handle(key)
}

func selectedBase(model *filetree.FileTreeModel) string {
	node := model.SelectedNode()
	if node == nil {
		return ""
	}
	return filepath.Base(node.Path)
}

// filterTypingCases exercises filter input at Host level; model owns value,
// TextField is paint-only (synced from model in syncFromModel).
func filterTypingCases() []struct {
	name  string
	setup func(*filetree.FileTreeModel, treePaths)
	key   tea.KeyPressMsg
	check func(t *testing.T, model *filetree.FileTreeModel, paths treePaths)
} {
	type filterCase = struct {
		name  string
		setup func(*filetree.FileTreeModel, treePaths)
		key   tea.KeyPressMsg
		check func(t *testing.T, model *filetree.FileTreeModel, paths treePaths)
	}
	return []filterCase{
		{
			name: "filter_typing_updates_model_once_per_key",
			setup: func(model *filetree.FileTreeModel, _ treePaths) {
				model.EnterFilterMode()
			},
			key: tea.KeyPressMsg{Code: 'm', Text: "m"},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.Filter() != "m" {
					t.Fatalf("filter=%q want m", model.Filter())
				}
			},
		},
		{
			name: "filter_multi_char_sequence",
			setup: func(model *filetree.FileTreeModel, _ treePaths) {
				model.EnterFilterMode()
				model.SetFilter("ma")
			},
			key: tea.KeyPressMsg{Code: 'i', Text: "i"},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.Filter() != "mai" {
					t.Fatalf("filter=%q want mai", model.Filter())
				}
			},
		},
		{
			name: "filter_backspace",
			setup: func(model *filetree.FileTreeModel, _ treePaths) {
				model.EnterFilterMode()
				model.SetFilter("mai")
			},
			key: tea.KeyPressMsg{Code: tea.KeyBackspace},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.Filter() != "ma" {
					t.Fatalf("filter=%q want ma", model.Filter())
				}
			},
		},
		{
			name: "filter_up_down_while_active",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.EnterFilterMode()
				model.SetFilter("go")
				model.Select(paths.main)
			},
			key: tea.KeyPressMsg{Code: tea.KeyDown},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.CursorIndex() < 1 {
					t.Fatalf("Down should move selection while filtering, cursor=%d", model.CursorIndex())
				}
			},
		},
	}
}

func nodeExpanded(model *filetree.FileTreeModel, path string) bool {
	for _, n := range model.VisibleNodes() {
		if n != nil && n.Path == path {
			return n.IsExpanded
		}
	}
	node := model.SelectedNode()
	if node != nil && node.Path == path {
		return node.IsExpanded
	}
	return false
}

// TestShellTreeKeyboardContractTable documents file-tree keyboard behavior
// at the product Screen/Host level (visible rows, selection, expand state).
func TestShellTreeKeyboardContractTable(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*filetree.FileTreeModel, treePaths)
		key   tea.KeyPressMsg
		check func(t *testing.T, model *filetree.FileTreeModel, paths treePaths)
	}{
		{
			name: "right_expands_collapsed_directory",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.pkg)
			},
			key: tea.KeyPressMsg{Code: tea.KeyRight},
			check: func(t *testing.T, model *filetree.FileTreeModel, paths treePaths) {
				if selectedBase(model) != "pkg" {
					t.Fatalf("selected=%q want pkg", selectedBase(model))
				}
				if !nodeExpanded(model, paths.pkg) {
					t.Fatal("pkg should be expanded after Right")
				}
			},
		},
		{
			name: "right_enters_first_child_when_expanded",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.pkg)
				model.Expand(paths.pkg)
			},
			key: tea.KeyPressMsg{Code: tea.KeyRight},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if selectedBase(model) != "util.go" {
					t.Fatalf("selected=%q want util.go", selectedBase(model))
				}
			},
		},
		{
			name: "left_collapses_expanded_directory",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.pkg)
				model.Expand(paths.pkg)
			},
			key: tea.KeyPressMsg{Code: tea.KeyLeft},
			check: func(t *testing.T, model *filetree.FileTreeModel, paths treePaths) {
				if selectedBase(model) != "pkg" {
					t.Fatalf("selected=%q want pkg", selectedBase(model))
				}
				if nodeExpanded(model, paths.pkg) {
					t.Fatal("pkg should collapse after Left")
				}
			},
		},
		{
			name: "left_moves_to_parent_from_nested_file",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Expand(paths.pkg)
				model.Select(paths.util)
			},
			key: tea.KeyPressMsg{Code: tea.KeyLeft},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if selectedBase(model) != "pkg" {
					t.Fatalf("selected=%q want pkg", selectedBase(model))
				}
			},
		},
		{
			name: "enter_toggles_directory_expand",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.pkg)
			},
			key: tea.KeyPressMsg{Code: tea.KeyEnter},
			check: func(t *testing.T, model *filetree.FileTreeModel, paths treePaths) {
				if !nodeExpanded(model, paths.pkg) {
					t.Fatal("Enter should expand directory")
				}
			},
		},
		{
			name: "enter_on_file_keeps_selection",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.main)
			},
			key: tea.KeyPressMsg{Code: tea.KeyEnter},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if selectedBase(model) != "main.go" {
					t.Fatalf("selected=%q want main.go", selectedBase(model))
				}
			},
		},
		{
			name: "home_jumps_to_first_visible_row",
			key:  tea.KeyPressMsg{Code: tea.KeyHome},
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Select(paths.util)
				model.Expand(paths.pkg)
			},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.CursorIndex() != 0 {
					t.Fatalf("cursor=%d want 0", model.CursorIndex())
				}
				if selectedBase(model) != filepath.Base(model.VisibleRows()[0]) {
					t.Fatalf("selected=%q want first row %q", selectedBase(model), filepath.Base(model.VisibleRows()[0]))
				}
			},
		},
		{
			name: "end_jumps_to_last_visible_row",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Expand(paths.pkg)
				model.Select(paths.main)
			},
			key: tea.KeyPressMsg{Code: tea.KeyEnd},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				last := len(model.VisibleRows()) - 1
				if model.CursorIndex() != last {
					t.Fatalf("cursor=%d want %d", model.CursorIndex(), last)
				}
			},
		},
		{
			name: "up_at_top_stays_at_first_row",
			setup: func(model *filetree.FileTreeModel, _ treePaths) {
				model.SetCursorIndex(0)
			},
			key: tea.KeyPressMsg{Code: tea.KeyUp},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.CursorIndex() != 0 {
					t.Fatalf("cursor=%d want 0", model.CursorIndex())
				}
			},
		},
		{
			name: "down_at_bottom_stays_at_last_row",
			setup: func(model *filetree.FileTreeModel, paths treePaths) {
				model.Expand(paths.pkg)
				model.SetCursorIndex(len(model.VisibleRows()) - 1)
			},
			key: tea.KeyPressMsg{Code: tea.KeyDown},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				last := len(model.VisibleRows()) - 1
				if model.CursorIndex() != last {
					t.Fatalf("cursor=%d want %d", model.CursorIndex(), last)
				}
			},
		},
		{
			name: "slash_enters_filter_mode",
			key:  tea.KeyPressMsg{Code: '/', Text: "/"},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if !model.FilterActive() {
					t.Fatal("FilterActive should be true after /")
				}
			},
		},
		{
			name: "esc_clears_active_filter",
			setup: func(model *filetree.FileTreeModel, _ treePaths) {
				model.EnterFilterMode()
				model.SetFilter("mai")
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, model *filetree.FileTreeModel, _ treePaths) {
				if model.Filter() != "" {
					t.Fatalf("filter=%q want empty after Esc", model.Filter())
				}
				if model.FilterActive() {
					t.Fatal("filter mode should deactivate after Esc")
				}
			},
		},
	}

	for _, tc := range filterTypingCases() {
		tests = append(tests, tc)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, paths := treeFixture(t)
			host := setupTreeHost(t, model)
			if tc.setup != nil {
				tc.setup(model, paths)
			}
			pressKey(host, tc.key)
			if tc.check != nil {
				tc.check(t, model, paths)
			}
		})
	}
}
