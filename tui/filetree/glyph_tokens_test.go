package filetree

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// TestIconResolverDirGlyphsConsumeGlyphTokens verifies the non-NerdFont
// directory fallback reads expand/collapse glyphs from the resolver's glyph
// tokens: overriding the token changes the resolved icon.
func TestIconResolverDirGlyphsConsumeGlyphTokens(t *testing.T) {
	r := NewIconResolver()
	r.UseNerdFont = false
	g := theme.DefaultGlyphs()
	g.TreeExpanded = "v"
	g.TreeCollapsed = "c"
	r.SetGlyphs(g)

	if got := r.ResolveIcon("dir", true, false, true).Icon; got != "v" {
		t.Fatalf("expanded dir icon=%q want %q (TreeExpanded token)", got, "v")
	}
	if got := r.ResolveIcon("dir", true, false, false).Icon; got != "c" {
		t.Fatalf("collapsed dir icon=%q want %q (TreeCollapsed token)", got, "c")
	}
}

func TestIconResolverApplyThemeUsesExplicitNerdFontPreference(t *testing.T) {
	r := NewIconResolver()
	th := theme.DefaultDark()
	enabled := true
	th.Tree.Config.UseNerdFont = &enabled

	r.ApplyTheme(th)

	if !r.UseNerdFont {
		t.Fatal("explicit theme Nerd Font preference was ignored")
	}
	if got := r.ResolveIcon("main.go", false, false, false).Icon; got == fallbackFileIcon {
		t.Fatalf("explicit Nerd Font preference still produced fallback icon %q", got)
	}
}

// TestTreeRowIndicatorConsumesExpandGlyphs verifies the native tree-row
// painter (icons disabled) paints the TreeCollapsed/TreeExpanded tokens for
// directory rows instead of hard-coded runes.
func TestTreeRowIndicatorConsumesExpandGlyphs(t *testing.T) {
	dir := t.TempDir()
	mkdirForTest(t, filepath.Join(dir, "sub"))
	model := NewFileTreeModel(dir)

	node := model.NodeByPath(filepath.Join(dir, "sub"))
	if node == nil || !node.IsDir {
		t.Fatalf("subdir node not found or not a dir")
	}
	item := ui.TreeItem{ID: node.Path, Label: node.Name, Depth: node.Depth, Payload: node}

	th := theme.DefaultDark()
	th.Glyphs.TreeCollapsed = "x"
	th.Glyphs.TreeExpanded = "X"
	tokens := ui.NewTokenSet(th)

	painter := NewTreeRowPainter(model, nil, PaintConfig{IndentWidth: 2, ShowIcons: false})
	buf := surface.NewCellBuffer(30, 1)
	painter(buf, 0, 0, 0, 30, item, ui.TreeRowState{}, tokens)
	if !strings.Contains(rowOf(buf, 0), "x") {
		t.Fatalf("collapsed dir row missing %q token glyph", "x")
	}

	node.IsExpanded = true
	buf = surface.NewCellBuffer(30, 1)
	painter(buf, 0, 0, 0, 30, item, ui.TreeRowState{}, tokens)
	if !strings.Contains(rowOf(buf, 0), "X") {
		t.Fatalf("expanded dir row missing %q token glyph", "X")
	}
}

// TestBreadcrumbLineConsumesBreadcrumbSepToken verifies the sticky parent
// path header joins ancestors with the theme BreadcrumbSep glyph.
func TestBreadcrumbLineConsumesBreadcrumbSepToken(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	mkdirForTest(t, sub)
	deep := filepath.Join(sub, "deep")
	mkdirForTest(t, deep)
	createFileForTest(t, filepath.Join(deep, "verydeep.go"), "package verydeep\n")

	model := NewFileTreeModel(dir)
	subNode := model.NodeByPath(sub)
	if subNode == nil {
		t.Fatalf("sub dir node not loaded")
	}
	if err := model.loadDirectory(subNode); err != nil {
		t.Fatalf("loadDirectory(sub): %v", err)
	}
	deepNode := model.NodeByPath(deep)
	if deepNode == nil {
		t.Fatalf("deep dir node not loaded")
	}
	if err := model.loadDirectory(deepNode); err != nil {
		t.Fatalf("loadDirectory(deep): %v", err)
	}
	subNode.IsExpanded = true
	deepNode.IsExpanded = true
	model.rebuildVisibleNodes()

	deepPath := filepath.Join(deep, "verydeep.go")
	idx := -1
	for i, n := range model.VisibleNodes() {
		if n.Path == deepPath {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("deep path not visible: %v", model.VisibleRows())
	}
	model.SetScrollOffset(idx)
	if model.ScrollOffset() <= 0 {
		t.Skip("scroll offset did not advance; breadcrumb would be empty")
	}

	sep := theme.DefaultGlyphs().BreadcrumbSep
	if sep == "" {
		sep = "▸"
	}
	got := BreadcrumbLine(model, 80)
	if !strings.Contains(got, " "+sep+" ") {
		t.Fatalf("breadcrumb %q missing BreadcrumbSep token %q", got, sep)
	}
	if !strings.Contains(got, "sub") || !strings.Contains(got, "deep") {
		t.Fatalf("breadcrumb %q missing ancestor path segments", got)
	}
	if strings.Contains(got, filepath.Base(dir)) {
		t.Fatalf("breadcrumb %q must not name the workspace root", got)
	}

	icons := NewIconResolver()
	icons.UseNerdFont = false
	withIcons := BreadcrumbLineWithIcons(model, 80, icons)
	folderIcon := icons.ResolveIcon("sub", true, false, true).Icon
	if folderIcon == "" || !strings.Contains(withIcons, folderIcon+" sub") {
		t.Fatalf("icon breadcrumb %q missing normal tree folder icon %q", withIcons, folderIcon)
	}
}

func rowOf(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Char != ' ' {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}
