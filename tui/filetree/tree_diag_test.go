package filetree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetDiagnostics_PathMatch(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)

	// The tree root should have main.go as a child.
	// Use the cleaned path (which is what the tree uses internally).
	cleaned := filepath.Clean(mainGo)
	node := tree.NodeByPath(cleaned)
	if node == nil {
		t.Fatalf("node not found at %q", cleaned)
	}

	// Copy of m.nodes to show what paths exist in the tree
	t.Logf("found node: Name=%q Path=%q", node.Name, node.Path)
}

func TestSetDiagnostics_UpdatesNode(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)
	cleaned := filepath.Clean(mainGo)

	// Initial: no diagnostics
	node := tree.NodeByPath(cleaned)
	if node == nil {
		t.Fatalf("node not found at %q", cleaned)
	}
	if node.DiagnosticCount != 0 {
		t.Errorf("expected 0 initially, got %d", node.DiagnosticCount)
	}

	// Set diagnostics using the cleaned path.
	tree.SetDiagnostics(cleaned, 2, 1, 0)

	node = tree.NodeByPath(cleaned)
	if node == nil {
		t.Fatalf("node disappeared after SetDiagnostics")
	}
	if node.DiagnosticCount != 3 {
		t.Errorf("expected DiagnosticCount=3, got %d", node.DiagnosticCount)
	}
	if node.DiagErrors != 2 {
		t.Errorf("expected DiagErrors=2, got %d", node.DiagErrors)
	}
	if node.DiagWarnings != 1 {
		t.Errorf("expected DiagWarnings=1, got %d", node.DiagWarnings)
	}

	// Root (parent) should have aggregated counts.
	if tree.Root.DiagnosticCount != 3 {
		t.Errorf("expected root DiagnosticCount=3, got %d", tree.Root.DiagnosticCount)
	}
	if tree.Root.DiagErrors != 2 {
		t.Errorf("expected root DiagErrors=2, got %d", tree.Root.DiagErrors)
	}
}

func TestSetDiagnostics_UncleanedPath(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)
	cleaned := filepath.Clean(mainGo)

	// Set diagnostics with an UNCLEANED path (e.g. what LSP might send).
	tree.SetDiagnostics(mainGo+"/./.", 1, 0, 0)
	//                                        ^^^^  filepath.Clean("/home/.../main.go/./.") = "/home/.../main.go"

	node := tree.NodeByPath(cleaned)
	if node == nil {
		t.Fatalf("node not found at %q", cleaned)
	}
	if node.DiagErrors != 1 {
		t.Errorf("expected DiagErrors=1 after uncleaned path, got %d", node.DiagErrors)
	}
}

func TestSetDiagnostics_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	createFileForTest(t, a, "package a")
	createFileForTest(t, b, "package b")

	tree := NewFileTreeModel(dir)

	tree.SetDiagnostics(a, 1, 0, 0)
	tree.SetDiagnostics(b, 0, 2, 1)

	na := tree.NodeByPath(a)
	nb := tree.NodeByPath(b)
	if na == nil || nb == nil {
		t.Fatal("nodes not found")
	}
	if na.DiagErrors != 1 {
		t.Errorf("a.go expected 1 error, got %d", na.DiagErrors)
	}
	if nb.DiagWarnings != 2 {
		t.Errorf("b.go expected 2 warnings, got %d", nb.DiagWarnings)
	}
	if nb.DiagHints != 1 {
		t.Errorf("b.go expected 1 hint, got %d", nb.DiagHints)
	}
	if tree.Root.DiagnosticCount != 4 {
		t.Errorf("root expected 4 total, got %d", tree.Root.DiagnosticCount)
	}
}

func TestSetDiagnostics_Replace(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	createFileForTest(t, mainGo, "package main")

	tree := NewFileTreeModel(dir)

	tree.SetDiagnostics(mainGo, 5, 0, 0)
	if tree.Root.DiagErrors != 5 {
		t.Errorf("expected 5 after first set, got %d", tree.Root.DiagErrors)
	}

	// Replace diagnostics
	tree.SetDiagnostics(mainGo, 0, 0, 0)
	if tree.Root.DiagErrors != 0 {
		t.Errorf("expected 0 after clearing, got %d", tree.Root.DiagErrors)
	}
	if tree.Root.DiagnosticCount != 0 {
		t.Errorf("expected 0 after clearing, got %d", tree.Root.DiagnosticCount)
	}
}

func TestSetDiagnostics_NodeNotLoaded(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	mkdirForTest(t, sub)
	subFile := filepath.Join(sub, "deep.go")
	createFileForTest(t, subFile, "package deep")

	tree := NewFileTreeModel(dir)
	subDir := filepath.Join(dir, "sub")

	subNode := tree.NodeByPath(subDir)
	if subNode == nil {
		t.Fatalf("sub directory node not found")
	}
	if subNode.Children != nil {
		t.Fatal("sub children should be nil before SetDiagnostics")
	}

	// Set diagnostics for the deep file — should auto-load the parent
	// without expanding it.
	tree.SetDiagnostics(subFile, 1, 0, 0)

	// Node should now exist (parent was loaded).
	node := tree.NodeByPath(subFile)
	if node == nil {
		t.Fatal("deep.go node should exist after SetDiagnostics (auto-loaded)")
	}
	if node.DiagErrors != 1 {
		t.Errorf("expected 1 error, got %d", node.DiagErrors)
	}

	// Sub dir should remain collapsed.
	subNode = tree.NodeByPath(subDir)
	if subNode == nil {
		t.Fatal("sub directory node not found")
	}
	if subNode.IsExpanded {
		t.Fatal("sub should NOT be expanded after SetDiagnostics")
	}

	// Sub dir should have aggregated count.
	if subNode.DiagnosticCount != 1 {
		t.Errorf("expected sub DiagnosticCount=1, got %d", subNode.DiagnosticCount)
	}

	// Root should also have aggregated count.
	if tree.Root.DiagnosticCount != 1 {
		t.Errorf("expected root DiagnosticCount=1, got %d", tree.Root.DiagnosticCount)
	}
}

func createFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mkdirForTest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}
