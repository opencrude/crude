package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestVisibleNodesOmitWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewFileTreeModel(dir)
	if m.Root == nil {
		t.Fatal("root node must still exist for aggregation")
	}
	for _, n := range m.VisibleNodes() {
		if n == m.Root {
			t.Fatal("workspace root must not be a painted row")
		}
	}
	if got := m.WorkspaceLabel(); got != filepath.Base(dir) {
		t.Fatalf("WorkspaceLabel=%q want %q", got, filepath.Base(dir))
	}

	m.SetDiagnostics(filepath.Join(dir, "main.go"), 2, 1, 0)
	if m.Root.DiagnosticCount != 3 || m.Root.DiagErrors != 2 {
		t.Fatalf("root aggregates diagnostics even when hidden: total=%d errors=%d",
			m.Root.DiagnosticCount, m.Root.DiagErrors)
	}

	m.CollapseAll()
	if !m.Root.IsExpanded {
		t.Fatal("collapse-all must leave the workspace root expanded")
	}
	foundMain := false
	for _, n := range m.VisibleNodes() {
		if n.Name == "main.go" {
			foundMain = true
		}
		if n == m.Root {
			t.Fatal("collapsed tree still painted the workspace root")
		}
	}
	if !foundMain {
		t.Fatal("top-level files must remain visible after collapse-all")
	}
}

func TestTopLevelRowsKeepChildConnectors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := NewFileTreeModel(dir)
	nodes := model.VisibleNodes()
	if len(nodes) < 2 {
		t.Fatal("need a directory and a file under the hidden root")
	}
	tokens := ui.NewTokenSet(theme.DefaultDark())
	painter := NewTreeRowPainter(model, nil, PaintConfig{IndentWidth: 2, ShowIcons: false})

	paint := func(n *TreeNode) string {
		t.Helper()
		buf := surface.NewCellBuffer(30, 1)
		painter(buf, 0, 0, 0, 30, ui.TreeItem{ID: n.Path, Label: n.Name, Depth: n.Depth, Payload: n}, ui.TreeRowState{}, tokens)
		return rowOf(buf, 0)
	}
	if got := paint(nodes[0]); !strings.Contains(got, "├") {
		t.Fatalf("first top-level row %q should keep the child-branch rail", got)
	}
	if got := paint(nodes[len(nodes)-1]); !strings.Contains(got, "└") {
		t.Fatalf("last top-level row %q should keep the child-branch rail", got)
	}
}

func TestHeaderMarksUseRootAggregates(t *testing.T) {
	root := &TreeNode{
		Name:            "ws",
		IsDir:           true,
		AggregateStatus: GitModified,
		DiagnosticCount: 5,
		DiagErrors:      3,
		DiagWarnings:    2,
	}
	tokens := ui.NewTokenSet(theme.DefaultDark())
	marks := HeaderMarks(root, PaintConfig{ShowGitStatus: true, ShowDiagnostics: true}, tokens)
	if len(marks) != 3 {
		t.Fatalf("marks=%#v want git + errors + warnings", marks)
	}
	if marks[0].Text != "M" {
		t.Fatalf("git mark=%q want M", marks[0].Text)
	}
	if marks[1].Text != "✕3" {
		t.Fatalf("error mark=%q want ✕3", marks[1].Text)
	}
	if marks[2].Text != "⚠2" {
		t.Fatalf("warning mark=%q want ⚠2", marks[2].Text)
	}
	if HeaderMarks(root, PaintConfig{}, tokens) != nil {
		t.Fatal("disabled git/diag config must omit header marks")
	}
}

func TestBreadcrumbOmitsWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(deep, "verydeep.go")
	if err := os.WriteFile(leaf, []byte("package verydeep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewFileTreeModel(dir)
	subNode := m.NodeByPath(sub)
	if subNode == nil {
		t.Fatal("expected top-level dir to load")
	}
	if err := m.loadDirectory(subNode); err != nil {
		t.Fatal(err)
	}
	deepNode := m.NodeByPath(deep)
	if deepNode == nil {
		t.Fatal("expected nested dir to load")
	}
	if err := m.loadDirectory(deepNode); err != nil {
		t.Fatal(err)
	}
	subNode.IsExpanded = true
	deepNode.IsExpanded = true
	m.rebuildVisibleNodes()

	idx := -1
	for i, n := range m.VisibleNodes() {
		if n.Path == leaf {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("leaf not visible")
	}
	m.SetScrollOffset(idx)
	if m.ScrollOffset() <= 0 {
		t.Skip("scroll offset did not advance; breadcrumb would be empty")
	}

	got := BreadcrumbLine(m, 80)
	if strings.Contains(got, filepath.Base(dir)) {
		t.Fatalf("breadcrumb %q must not name the workspace root", got)
	}
	if !strings.Contains(got, "sub") || !strings.Contains(got, "deep") {
		t.Fatalf("breadcrumb %q missing nested ancestors", got)
	}
}
