package filetree

import (
	"path/filepath"
	"testing"

	"crude/third_party/pathwalk"
)

func TestRemoteDiagnosticsAggregateUnloadedPathsAndSurviveBrowse(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "pkg")
	path := filepath.Join(folder, "deep", "main.go")
	m := NewRemoteModel(root, nil)
	m.SetDiagnostics(path, 1, 2, 0)
	if m.Root.DiagnosticCount != 3 {
		t.Fatal("diagnostics arriving before rows did not reach the root aggregate")
	}
	m.SetDirectoryEntries(root, []DirectoryEntry{{Name: "pkg", Path: folder, IsDir: true}})
	if node := m.NodeByPath(folder); node == nil || node.DiagErrors != 1 || node.DiagWarnings != 2 {
		t.Fatalf("collapsed directory has no diagnostics for unloaded descendants: %+v", node)
	}
	deep := filepath.Dir(path)
	m.SetDirectoryEntries(folder, []DirectoryEntry{{Name: "deep", Path: deep, IsDir: true}})
	m.SetDirectoryEntries(deep, []DirectoryEntry{{Name: "main.go", Path: path}})
	if node := m.NodeByPath(path); node == nil || node.DiagnosticCount != 3 || m.Root.DiagnosticCount != 3 {
		t.Fatalf("loaded file lost or double-counted diagnostics: %+v; root=%+v", node, m.Root)
	}
	m.SetDiagnostics(path, 0, 0, 0)
	if m.Root.DiagnosticCount != 0 || m.NodeByPath(path).DiagnosticCount != 0 {
		t.Fatal("cleared diagnostics remained painted")
	}
}

func TestRemoteSearchStatusIncludesUnloadedMatchesAndRefreshPreservesSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "main.go")
	base := NewRemoteModel(root, nil)
	base.SetRemoteGitStatuses(map[string]GitStatus{path: GitModified})
	base.SetDiagnostics(path, 1, 0, 0)
	search := NewSearchModelFromBase(base, []PathMatch{{Entry: pathwalk.Entry{Path: path, Name: "main.go"}}})
	if node := search.NodeByPath(path); node.GitStatus != GitModified || node.DiagErrors != 1 {
		t.Fatalf("unloaded search match has no status: %+v", node)
	}
	selected := search.SelectedNode().Path
	base.SetRemoteGitStatuses(nil)
	base.ClearDiagnostics()
	search.RefreshStatusFrom(base)
	if node := search.NodeByPath(path); node.GitStatus != GitNone || node.DiagnosticCount != 0 || search.SelectedNode().Path != selected {
		t.Fatalf("status refresh lost selection or retained obsolete evidence: %+v", node)
	}
}
