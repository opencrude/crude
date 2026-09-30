package filetree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/rendering"
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

func TestDeepTreeReclaimsGuideColumnsWithoutFlatteningAncestors(t *testing.T) {
	root := t.TempDir()
	current := root
	var ancestors []string
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		current = filepath.Join(current, name)
		if err := os.Mkdir(current, 0o755); err != nil {
			t.Fatal(err)
		}
		ancestors = append(ancestors, current)
	}
	leaf := filepath.Join(current, "long_module_name.go")
	if err := os.WriteFile(leaf, []byte("package p"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := NewFileTreeModel(root)
	for _, path := range ancestors {
		model.Expand(path)
	}
	nodes := model.VisibleNodes()
	if len(nodes) != len(ancestors)+1 || nodes[len(nodes)-1].Path != leaf {
		t.Fatalf("folder chain was flattened or leaf is missing: %d rows", len(nodes))
	}
	item := nodes[len(nodes)-1]
	painter := NewTreeRowPainter(model, nil, PaintConfig{IndentWidth: 2, ShowIcons: false})
	tokens := ui.NewTokenSet(theme.DefaultDark())
	for _, tc := range []struct{ width, connectorX int }{{36, item.Depth}, {80, item.Depth * 3}} {
		buf := surface.NewCellBuffer(tc.width, 1)
		painter(buf, 0, 0, 0, tc.width, ui.TreeItem{ID: item.Path, Label: item.Name, Depth: item.Depth, Payload: item}, ui.TreeRowState{}, tokens)
		if got := rowOf(buf, 0); !strings.Contains(got, item.Name) {
			t.Fatalf("width %d spent name columns on guides: %q", tc.width, got)
		}
		if got := buf.Get(tc.connectorX, 0).Char; got != '└' {
			t.Fatalf("width %d lost the leaf's depth-%d branch at %d: %q", tc.width, item.Depth, tc.connectorX, got)
		}
	}
	model.Update(TreeHeightMsg{Height: 2})
	model.SetScrollOffset(len(ancestors) - 1) // g and its file; f is the common offscreen parent.
	context := ViewportContext(model, 2, 16, nil)
	if !strings.HasPrefix(context.Breadcrumb, "…") || !strings.HasSuffix(context.Breadcrumb, "f") || context.PathAt(0) != "" {
		t.Fatalf("narrow breadcrumb lost the closest parent or made ellipsis navigable: %+v", context)
	}
	if x := strings.LastIndex(context.Breadcrumb, "f"); context.PathAt(rendering.VisibleWidth(context.Breadcrumb[:x])) != ancestors[len(ancestors)-2] {
		t.Fatalf("closest visible breadcrumb does not navigate to f: %q", context.Breadcrumb)
	}
	model.Collapse(ancestors[0])
	if len(model.VisibleNodes()) != 1 {
		t.Fatal("compressing guides must not change expand/collapse or visible membership")
	}
}

func TestTreeBreadcrumbDoesNotClaimAncestorOutsideVisibleBranch(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/b/c/long_module_name.go", "a/b/d/other.go", "z.go"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package p"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewFileTreeModel(root)
	for _, dir := range []string{"a", "a/b", "a/b/c", "a/b/d"} {
		m.Expand(filepath.Join(root, dir))
	}
	m.Update(TreeHeightMsg{Height: 3})
	m.SetScrollOffset(2) // c, long_module_name.go, d: all within a/b.
	if got := BreadcrumbLine(m, 36); !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Fatalf("missing shared offscreen ancestry for one subtree: %q", got)
	}
	context := ViewportContext(m, 3, 36, nil)
	if context.BaseDepth != 2 || context.PathAt(0) != filepath.Join(root, "a") || context.PathAt(1) != "" {
		t.Fatalf("breadcrumb and guide base disagree or separator navigates: %+v", context)
	}
	leaf := m.NodeByPath(filepath.Join(root, "a/b/c/long_module_name.go"))
	buf := surface.NewCellBuffer(36, 1)
	NewTreeRowPainter(m, nil, PaintConfig{IndentWidth: 2, IndentBaseDepth: context.BaseDepth, ShowIcons: false})(
		buf, 0, 0, 0, 36, ui.TreeItem{ID: leaf.Path, Label: leaf.Name, Depth: leaf.Depth, Payload: leaf}, ui.TreeRowState{}, ui.NewTokenSet(theme.DefaultDark()))
	if got := rowOf(buf, 0); !strings.Contains(got, leaf.Name) || buf.Get(3, 0).Char != '└' {
		t.Fatalf("breadcrumb context did not reclaim ancestor rails without losing local branch: %q", got)
	}
	m.SetScrollOffset(3) // leaf, d, other: same a/b ancestry despite scroll.
	if next := ViewportContext(m, 3, 36, nil); next.BaseDepth != context.BaseDepth || next.Breadcrumb != context.Breadcrumb {
		t.Fatalf("row alignment jumped while breadcrumb context stayed the same: before=%+v after=%+v", context, next)
	}
	m.Update(TreeHeightMsg{Height: 4})
	m.SetScrollOffset(3) // file in c, d, file in d, root-level z.go.
	if got := BreadcrumbLine(m, 36); !strings.Contains(got, "a") || !strings.Contains(got, "c") {
		t.Fatalf("normal breadcrumb disappeared when viewport crossed a root branch: %q", got)
	}
	if next := ViewportContext(m, 4, 36, nil); next.BaseDepth != 0 || next.PathAt(0) != filepath.Join(root, "a") {
		t.Fatalf("mixed-branch breadcrumb should navigate but retain absolute guides: %+v", next)
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
