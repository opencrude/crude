package filetree

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/third_party/pathwalk"
)

func TestRankPathEntriesPrefersBasenameAndBoundaries(t *testing.T) {
	root := t.TempDir()
	entries := []pathwalk.Entry{
		{Path: filepath.Join(root, "docs", "tree_rows.go"), Relative: "docs/tree_rows.go", Name: "tree_rows.go"},
		{Path: filepath.Join(root, "src", "tree.go"), Relative: "src/tree.go", Name: "tree.go"},
		{Path: filepath.Join(root, "src", "other.go"), Relative: "src/other.go", Name: "other.go"},
	}
	matches := RankPathEntries(entries, "tr")
	if len(matches) != 3 {
		t.Fatalf("matches=%d, want three", len(matches))
	}
	if matches[0].Entry.Name != "tree_rows.go" {
		t.Fatalf("first match=%q, want tree_rows.go", matches[0].Entry.Name)
	}
	if len(matches[0].NamePositions) != 2 {
		t.Fatalf("name positions=%v, want two highlighted runes", matches[0].NamePositions)
	}
}

func TestRankPathEntriesExactNameWithRepeatedLetters(t *testing.T) {
	entry := pathwalk.Entry{Path: "/repo/README.md", Relative: "README.md", Name: "README.md"}
	matches := RankPathEntries([]pathwalk.Entry{entry}, "README.md")
	if len(matches) != 1 || matches[0].Entry.Path != entry.Path {
		t.Fatalf("exact filename did not match: %+v", matches)
	}
}

func TestNewPickerSearchModelMaterializesAncestorsWithoutIO(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "nested", "main.go")
	matches := []PathMatch{{
		Entry: pathwalk.Entry{
			Path: file, Relative: "src/nested/main.go", Name: "main.go",
		},
		NamePositions: []int{0, 1, 2},
	}}
	model := NewPickerSearchModel(root, false, matches)
	nodes := model.VisibleNodes()
	if len(nodes) != 3 {
		t.Fatalf("visible nodes=%d, want ancestors plus file", len(nodes))
	}
	for _, path := range []string{
		filepath.Join(root, "src"),
		filepath.Join(root, "src", "nested"),
		file,
	} {
		if model.NodeByPath(path) == nil {
			t.Fatalf("missing projected path %q", path)
		}
	}
	if got := model.FilterMatchPositions(file); len(got) != 3 {
		t.Fatalf("file match positions=%v", got)
	}
}

func TestPickerFileTreeDoesNotSynchronouslyLoadUnloadedChildOnFilter(t *testing.T) {
	root := t.TempDir()
	model := NewPickerFileTreeModel(root, false)
	model.SetDirectoryEntries(root, []DirectoryEntry{{Name: "src", Path: filepath.Join(root, "src"), IsDir: true}})
	model.SetDirectoryLoader(func(string) tea.Cmd { return nil })
	model.SetFilter("src")
	if node := model.NodeByPath(filepath.Join(root, "src")); node == nil || !node.IsDir {
		t.Fatal("filter removed loaded picker directory")
	}
}
