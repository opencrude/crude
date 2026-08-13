package filetree

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// filterFixture builds root/{alpha.go, README.md, deep/nested/target_file.go}
// without expanding any directory beyond the root.
func filterFixture(t *testing.T) (*FileTreeModel, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	nested := filepath.Join(dir, "deep", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"alpha":  filepath.Join(dir, "alpha.go"),
		"readme": filepath.Join(dir, "README.md"),
		"target": filepath.Join(nested, "target_file.go"),
		"deep":   filepath.Join(dir, "deep"),
		"nested": nested,
		"root":   dir,
	}
	for _, p := range []string{paths["alpha"], paths["readme"], paths["target"]} {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewFileTreeModel(dir), paths
}

func visiblePaths(m *FileTreeModel) map[string]bool {
	out := make(map[string]bool)
	for _, n := range m.VisibleNodes() {
		out[n.Path] = true
	}
	return out
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		name, pattern string
		want          []int
		ok            bool
	}{
		{"main.go", "mg", []int{0, 5}, true},
		{"main.go", "MAIN", []int{0, 1, 2, 3}, true},
		{"main.go", "", nil, true},
		{"main.go", "gm", nil, false},
		{"pkg", "go", nil, false},
		{"Über.txt", "üb", []int{0, 1}, true},
	}
	for _, tc := range cases {
		got, ok := FuzzyMatch(tc.name, tc.pattern)
		if ok != tc.ok {
			t.Fatalf("FuzzyMatch(%q,%q) ok=%v want %v", tc.name, tc.pattern, ok, tc.ok)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("FuzzyMatch(%q,%q) positions=%v want %v", tc.name, tc.pattern, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("FuzzyMatch(%q,%q) positions=%v want %v", tc.name, tc.pattern, got, tc.want)
			}
		}
	}
}

func TestSetFilterFuzzySubsequenceMatchesName(t *testing.T) {
	m, paths := filterFixture(t)
	// "ago" is a subsequence of alpha.go but not of README.md.
	m.SetFilter("ago")
	vis := visiblePaths(m)
	if !vis[paths["alpha"]] {
		t.Fatalf("alpha.go should match fuzzy 'ago'; visible=%v", vis)
	}
	if vis[paths["readme"]] {
		t.Fatal("README.md must not match fuzzy 'ago'")
	}
}

func TestSetFilterFindsMatchesInUnloadedDirsAndAutoExpands(t *testing.T) {
	m, paths := filterFixture(t)
	if m.NodeByPath(paths["deep"]).Children != nil {
		t.Fatal("precondition: deep must be unloaded")
	}

	m.SetFilter("target")

	vis := visiblePaths(m)
	for _, key := range []string{"deep", "nested", "target"} {
		if !vis[paths[key]] {
			t.Fatalf("%s should be visible under filter; visible=%v", key, vis)
		}
	}
	if vis[paths["alpha"]] || vis[paths["readme"]] {
		t.Fatal("non-matching files must be hidden while filtering")
	}
	for _, key := range []string{"deep", "nested"} {
		if !m.NodeByPath(paths[key]).IsExpanded {
			t.Fatalf("%s should be auto-expanded for the match", key)
		}
	}

	if pos := m.FilterMatchPositions(paths["target"]); len(pos) != len("target") {
		t.Fatalf("target_file.go match positions=%v", pos)
	}
	if pos := m.FilterMatchPositions(paths["alpha"]); pos != nil {
		t.Fatalf("alpha.go should have no match positions, got %v", pos)
	}

	// Clearing the filter restores the auto-expanded directories.
	m.SetFilter("")
	for _, key := range []string{"deep", "nested"} {
		if m.NodeByPath(paths[key]).IsExpanded {
			t.Fatalf("%s should collapse back after the filter clears", key)
		}
	}
	if m.FilterMatchPositions(paths["target"]) != nil {
		t.Fatal("match positions must clear with the filter")
	}
}

func TestSetFilterKeepsManuallyExpandedDirsOnClear(t *testing.T) {
	m, paths := filterFixture(t)
	m.Expand(paths["deep"])

	m.SetFilter("target")
	m.SetFilter("")

	if !m.NodeByPath(paths["deep"]).IsExpanded {
		t.Fatal("manually expanded dir must stay expanded after filter clears")
	}
	if m.NodeByPath(paths["nested"]).IsExpanded {
		t.Fatal("filter-expanded dir must collapse after filter clears")
	}
}

func TestSetFilterNoMatchesYieldsEmptyVisibleList(t *testing.T) {
	m, _ := filterFixture(t)
	m.SetFilter("zzzzqq")
	if n := len(m.VisibleNodes()); n != 0 {
		t.Fatalf("no-match filter should yield no rows, got %d", n)
	}
}

func TestFilterSelectionJumpsToFirstMatch(t *testing.T) {
	m, paths := filterFixture(t)
	// Root is selected before filtering; the pattern matches only the
	// nested file. Selection must land on the match so Enter opens it
	// instead of toggling the selected directory (VS Code behavior).
	m.SetFilter("targetfile")
	if sel := m.SelectedNode(); sel == nil || sel.Path != paths["target"] {
		t.Fatalf("selection=%v want first match %q", sel, paths["target"])
	}
	// A selected node that itself matches is kept across filter edits.
	m.SetFilter("target")
	if sel := m.SelectedNode(); sel == nil || sel.Path != paths["target"] {
		t.Fatalf("matching selection not kept: %v", sel)
	}
}

func TestTypeToFilterStartsAndEditsFilter(t *testing.T) {
	m, paths := filterFixture(t)

	cmd := m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: 'a', Text: "a"}})
	if !m.FilterActive() {
		t.Fatal("typing a printable char must enter filter mode")
	}
	if m.Filter() != "a" {
		t.Fatalf("filter=%q want a", m.Filter())
	}
	if cmd == nil {
		t.Fatal("type-to-filter should emit EnterFilterModeMsg for focus routing")
	}
	if _, ok := cmd().(EnterFilterModeMsg); !ok {
		t.Fatalf("cmd=%T want EnterFilterModeMsg", cmd())
	}

	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: 'l', Text: "l"}})
	if m.Filter() != "al" {
		t.Fatalf("filter=%q want al", m.Filter())
	}

	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: tea.KeyBackspace}})
	if m.Filter() != "a" {
		t.Fatalf("after backspace filter=%q want a", m.Filter())
	}

	// Navigation still moves through visible rows while filtering.
	m.SetCursorIndex(0)
	before := m.CursorIndex()
	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: tea.KeyDown}})
	if m.CursorIndex() != before+1 {
		t.Fatalf("Down while filtering cursor=%d want %d", m.CursorIndex(), before+1)
	}

	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: tea.KeyEsc}})
	if m.Filter() != "" || m.FilterActive() {
		t.Fatalf("Esc must clear filter, got %q active=%v", m.Filter(), m.FilterActive())
	}
	_ = paths
}

func TestTypeToFilterIgnoresModifiedKeys(t *testing.T) {
	m, _ := filterFixture(t)
	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: 'g', Text: "g", Mod: tea.ModCtrl}})
	if m.FilterActive() || m.Filter() != "" {
		t.Fatalf("ctrl-modified key must not start filter: active=%v filter=%q", m.FilterActive(), m.Filter())
	}
}

func TestTreeCtrlChordsSurviveTypeToFilter(t *testing.T) {
	m, _ := filterFixture(t)

	if m.ShowHidden() {
		t.Fatal("precondition: hidden files off")
	}
	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: '.', Mod: tea.ModCtrl}})
	if !m.ShowHidden() {
		t.Fatal("Ctrl+. should toggle hidden files")
	}
	if m.FilterActive() {
		t.Fatal("Ctrl+. must not start the filter")
	}

	cmd := m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}})
	if cmd == nil {
		t.Fatal("Ctrl+R should trigger a tree refresh command")
	}
	if _, ok := cmd().(TreeRefreshDoneMsg); !ok {
		t.Fatalf("Ctrl+R cmd=%T want TreeRefreshDoneMsg", cmd())
	}
	if m.FilterActive() {
		t.Fatal("Ctrl+R must not start the filter")
	}
}

func TestSlashEntersEmptyFilterMode(t *testing.T) {
	m, _ := filterFixture(t)
	m.Update(TreeKeyPressMsg{Key: tea.KeyPressMsg{Code: '/', Text: "/"}})
	if !m.FilterActive() {
		t.Fatal("/ must enter filter mode")
	}
	if m.Filter() != "" {
		t.Fatalf("/ must not seed the pattern, got %q", m.Filter())
	}
}

func TestExpandToPathLoadsAncestorsAndSelects(t *testing.T) {
	m, paths := filterFixture(t)
	if m.NodeByPath(paths["deep"]).Children != nil {
		t.Fatal("precondition: deep must be unloaded")
	}

	if !m.ExpandToPath(paths["target"]) {
		t.Fatal("ExpandToPath should reveal a nested unloaded file")
	}

	vis := visiblePaths(m)
	if !vis[paths["target"]] {
		t.Fatalf("target must be visible after reveal; visible=%v", vis)
	}
	node := m.SelectedNode()
	if node == nil || node.Path != paths["target"] {
		t.Fatalf("reveal should select the target, selected=%v", node)
	}
	idx := m.CursorIndex()
	if nodes := m.VisibleNodes(); idx < 0 || idx >= len(nodes) || nodes[idx].Path != paths["target"] {
		t.Fatalf("cursor index %d should point at the target", idx)
	}
}

func TestExpandToPathRejectsOutsideRoot(t *testing.T) {
	m, _ := filterFixture(t)
	if m.ExpandToPath(filepath.Join(t.TempDir(), "elsewhere.go")) {
		t.Fatal("paths outside the workspace root must not reveal")
	}
	if m.ExpandToPath("") {
		t.Fatal("empty path must not reveal")
	}
}
