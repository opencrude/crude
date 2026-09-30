package filetree

import (
	"slices"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// PaintConfig holds tree paint options from the active theme.
type PaintConfig struct {
	IndentWidth     int
	IndentBaseDepth int // offscreen common ancestry already named by the breadcrumb
	ShowIcons       bool
	ShowGitStatus   bool
	ShowDiagnostics bool
	UseNerdFont     bool
	Background      cell.Color
	BackgroundSet   bool
}

// PaintConfigFromTheme extracts paint config from a theme.
func PaintConfigFromTheme(th *theme.Theme) PaintConfig {
	cfg := PaintConfig{IndentWidth: 2, ShowIcons: true, ShowGitStatus: true, ShowDiagnostics: true}
	if th == nil {
		return cfg
	}
	c := th.Tree.Config
	cfg.IndentWidth = c.IndentWidth
	cfg.ShowIcons = c.ShowIcons
	cfg.ShowGitStatus = c.ShowGitStatus
	cfg.ShowDiagnostics = c.ShowDiagnostics
	if c.UseNerdFont != nil {
		cfg.UseNerdFont = *c.UseNerdFont
	}
	if cfg.IndentWidth <= 0 {
		cfg.IndentWidth = 2
	}
	return cfg
}

// TreeItems projects visible file-tree nodes into ui.TreeItem values.
func TreeItems(model *FileTreeModel) []ui.TreeItem {
	if model == nil {
		return nil
	}
	nodes := model.VisibleNodes()
	items := make([]ui.TreeItem, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		items = append(items, ui.TreeItem{
			ID:      node.Path,
			Label:   node.Name,
			Depth:   node.Depth,
			Payload: node,
		})
	}
	return items
}

// NewTreeRowPainter returns a native cell-buffer row painter for file-tree rows.
func NewTreeRowPainter(model *FileTreeModel, icons *IconResolver, cfg PaintConfig) ui.TreeRowPainter {
	return func(buf *surface.CellBuffer, ox, oy, rowY, width int, item ui.TreeItem, st ui.TreeRowState, tokens *ui.TokenSet) {
		node, _ := item.Payload.(*TreeNode)
		if node == nil || buf == nil || tokens == nil {
			return
		}
		y := oy + rowY
		x := ox
		bg := cell.NoColor
		if cfg.BackgroundSet {
			bg = cfg.Background
		}
		hovered := st.Hovered
		if !hovered && model != nil && model.Hover.Index >= 0 {
			if nodes := model.VisibleNodes(); model.Hover.Index < len(nodes) && nodes[model.Hover.Index] == node {
				hovered = true
			}
		}
		// Cursor/selection chrome is gated by st.Cursor (Host focus). Painting
		// SelectedBg whenever the model selection matches made the tree look
		// focused while the launcher owned keys.
		switch {
		case st.Cursor:
			bg = tokens.TreeCursorBg()
		case hovered:
			bg = tokens.TreeHoverBg()
		}
		if !bg.IsDefault() {
			ui.FillRect(buf, 0, 0, ui.Rect{X: ox, Y: y, W: width, H: 1}, cell.BlankCell(bg))
		}

		indentFG := tokens.TreeIndentFg()
		remainingHOff := 0
		indentWidth := cfg.IndentWidth
		if model != nil {
			remainingHOff = model.HScrollOffset()
			indentWidth = adaptiveIndentWidth(width, max(0, model.MaxVisibleDepth()-cfg.IndentBaseDepth), cfg.IndentWidth)
		}
		for d := cfg.IndentBaseDepth; d < node.Depth; d++ {
			colW := 1 + indentWidth
			if remainingHOff >= colW {
				remainingHOff -= colW
				continue
			}
			if hasSiblingBelow(node, d) {
				buf.Set(x, y, cell.NewCell(ui.FirstRune(tokens.Glyphs().BorderV, '│'), indentFG, bg, 0))
			} else {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			}
			x++
			for s := 0; s < indentWidth; s++ {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
		}
		// Depth 0 is a child of the hidden workspace root. Those rows still
		// get the first branch rail (├/└) so top-level folders look like
		// they did under the old root row.
		if node.Depth >= 0 && remainingHOff < 4 {
			if isLastChild(node) {
				buf.Set(x, y, cell.NewCell('└', indentFG, bg, 0))
			} else {
				buf.Set(x, y, cell.NewCell('├', indentFG, bg, 0))
			}
			x++
			buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			x++
			buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			x++
		}

		if node.IsDir && (!cfg.ShowIcons || icons == nil) {
			g := tokens.Glyphs()
			indicator := string(ui.FirstRune(g.TreeCollapsed, '▸'))
			if node.IsExpanded {
				indicator = string(ui.FirstRune(g.TreeExpanded, '▾'))
			}
			if icons != nil && icons.UseNerdFont {
				if node.IsExpanded {
					indicator = "\uf07c"
				} else {
					indicator = "\uf07b"
				}
			}
			x = buf.WriteString(x, y, indicator, tokens.TreeDirFg(), bg, 0)
			buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			x++
		}

		if cfg.ShowIcons && icons != nil {
			iconRes := icons.ResolveIcon(node.Name, node.IsDir, node.IsSymlink, node.IsExpanded)
			if iconRes.Icon != "" {
				iconFG := tokens.TreeFileFg()
				if iconRes.Color != nil {
					c := rendering.ThemeColorToCell(iconRes.Color)
					if !c.IsDefault() {
						iconFG = c
					}
				}
				x = buf.WriteString(x, y, iconRes.Icon, iconFG, bg, 0)
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
		}

		displayGit := node.GitStatus
		if node.IsDir && displayGit == GitNone && node.AggregateStatus != GitNone {
			displayGit = node.AggregateStatus
		}
		type indicatorSegment struct {
			text string
			fg   cell.Color
		}
		var indicators [3]indicatorSegment
		indicatorCount := 0
		if node.Selected {
			indicators[indicatorCount] = indicatorSegment{text: "✓", fg: tokens.Color(ui.ColorAccent)}
			indicatorCount++
		}
		if node.Loading {
			if indicatorCount >= len(indicators) {
				indicatorCount = len(indicators) - 1
			}
			indicators[indicatorCount] = indicatorSegment{text: "…", fg: tokens.Color(ui.ColorMuted)}
			indicatorCount++
		}
		if cfg.ShowGitStatus && displayGit != GitNone {
			if gitStr := StatusString(displayGit); gitStr != "" {
				indicators[indicatorCount] = indicatorSegment{text: gitStr, fg: gitColor(tokens, displayGit)}
				indicatorCount++
			}
		}
		if cfg.ShowDiagnostics && node.DiagnosticCount > 0 {
			if node.DiagErrors > 0 {
				indicators[indicatorCount] = indicatorSegment{text: "✕" + itoa(node.DiagErrors), fg: tokens.TreeDiagError()}
				indicatorCount++
			}
			if node.DiagWarnings > 0 {
				indicators[indicatorCount] = indicatorSegment{text: "⚠" + itoa(node.DiagWarnings), fg: tokens.TreeDiagWarning()}
				indicatorCount++
			}
		}
		indicatorW := 0
		for i := 0; i < indicatorCount; i++ {
			if i > 0 {
				indicatorW++
			}
			indicatorW += rendering.VisibleWidth(indicators[i].text)
		}
		indicatorX := ox + width
		nameMax := width - (x - ox)
		if indicatorW > 0 {
			indicatorX = ox + width - indicatorW - 1
			if indicatorX-x < 2 {
				indicatorCount = 0
				indicatorX = ox + width
			} else {
				nameMax = indicatorX - x - 1
			}
		}
		if nameMax < 1 {
			nameMax = 1
		}
		nameText := node.Name
		truncated := false
		if rendering.VisibleWidth(node.Name) > nameMax {
			nameText = rendering.TruncateVisual(node.Name, nameMax, "…")
			truncated = true
		}
		nameFG := tokens.TreeFileFg()
		nameAttr := cell.Attrs(0)
		if node.IsDir {
			nameFG = tokens.TreeDirFg()
			nameAttr = cell.AttrBold
		} else if node.IsSymlink {
			nameFG = tokens.TreeSymlinkFg()
		}
		if st.Cursor {
			if fg := tokens.TreeCursorFg(); !fg.IsDefault() {
				nameFG = fg
			}
			nameAttr = cell.AttrBold
		}
		var matchPos []int
		if model != nil {
			matchPos = model.FilterMatchPositions(node.Path)
		}
		if len(matchPos) == 0 {
			x = buf.WriteString(x, y, nameText, nameFG, bg, nameAttr)
		} else {
			x = writeNameHighlighted(buf, x, y, nameText, truncated, matchPos, nameFG, tokens.Color(ui.ColorAccent), bg, nameAttr)
		}

		for fx := x; fx < indicatorX; fx++ {
			buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
		}
		x = indicatorX
		for i := 0; i < indicatorCount; i++ {
			if i > 0 {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
			x = buf.WriteString(x, y, indicators[i].text, indicators[i].fg, bg, 0)
		}
		for fx := x; fx < ox+width; fx++ {
			buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
		}
	}
}

// adaptiveIndentWidth spends spare columns on familiar, roomy guides, then
// reclaims only guide whitespace when deeper rows need readable labels. The
// reserved rail remains one column per ancestor even at the minimum width.
// Using the whole visible tree's maximum depth keeps columns aligned on scroll.
func adaptiveIndentWidth(width, depth, preferred int) int {
	if depth <= 0 || width <= 0 {
		return preferred
	}
	labelBudget := min(24, max(8, width/2))
	guideBudget := width - labelBudget - 8 // branch, icon, and trailing status
	return min(preferred, max(0, guideBudget/depth-1))
}

// writeNameHighlighted paints a (possibly truncated) node name rune by rune,
// switching matched runes to the accent foreground with bold. Positions are
// rune indices into the ORIGINAL name; truncation keeps a prefix plus a
// trailing ellipsis, so positions beyond the kept prefix are dropped and the
// ellipsis itself is never highlighted.
func writeNameHighlighted(buf *surface.CellBuffer, x, y int, nameText string, truncated bool, positions []int, fg, matchFG cell.Color, bg cell.Color, attr cell.Attrs) int {
	runes := []rune(nameText)
	kept := len(runes)
	if truncated && kept > 0 {
		kept--
	}
	pi := 0
	for ri, r := range runes {
		for pi < len(positions) && positions[pi] < ri {
			pi++
		}
		matched := ri < kept && pi < len(positions) && positions[pi] == ri
		if matched {
			x = buf.WriteString(x, y, string(r), matchFG, bg, attr|cell.AttrBold)
		} else {
			x = buf.WriteString(x, y, string(r), fg, bg, attr)
		}
	}
	return x
}

func gitColor(tokens *ui.TokenSet, st GitStatus) cell.Color {
	switch st {
	case GitStaged:
		return tokens.TreeGitAdded()
	case GitDeleted, GitConflicted:
		return tokens.TreeGitDeleted()
	case GitUntracked:
		return tokens.TreeGitUntracked()
	default:
		return tokens.TreeGitModified()
	}
}

// HeaderMark is one workspace-status chip for the explorer search row.
type HeaderMark struct {
	Text string
	FG   cell.Color
}

// HeaderMarks returns the aggregated git letter and diagnostic counts that
// used to paint on the workspace root row. The explorer search badge uses
// the same glyphs and colors.
func HeaderMarks(root *TreeNode, cfg PaintConfig, tokens *ui.TokenSet) []HeaderMark {
	if root == nil || tokens == nil {
		return nil
	}
	var marks []HeaderMark
	if cfg.ShowGitStatus {
		git := root.GitStatus
		if git == GitNone {
			git = root.AggregateStatus
		}
		if s := StatusString(git); s != "" {
			marks = append(marks, HeaderMark{Text: s, FG: gitColor(tokens, git)})
		}
	}
	if cfg.ShowDiagnostics && root.DiagnosticCount > 0 {
		if root.DiagErrors > 0 {
			marks = append(marks, HeaderMark{Text: "✕" + itoa(root.DiagErrors), FG: tokens.TreeDiagError()})
		}
		if root.DiagWarnings > 0 {
			marks = append(marks, HeaderMark{Text: "⚠" + itoa(root.DiagWarnings), FG: tokens.TreeDiagWarning()})
		}
	}
	return marks
}

// TreeViewportContext is the single owner of sticky ancestry and guide offset.
// Shared ancestry permits compact guides; a mixed-branch viewport keeps its
// normal top-row breadcrumb without shifting any row's absolute indentation.
type TreeViewportContext struct {
	Breadcrumb string
	BaseDepth  int
	labels     []string
	paths      []string
	width      int
}

// PathAt resolves a breadcrumb label to its folder. Separators do not click.
func (c TreeViewportContext) PathAt(x int) string {
	sep := theme.DefaultGlyphs().BreadcrumbSep
	if sep == "" {
		sep = "▸"
	}
	if index := ui.BreadcrumbIndex(c.labels, " "+sep+" ", x, c.width); index >= 0 {
		return c.paths[index]
	}
	return ""
}

func ViewportContext(model *FileTreeModel, rows, width int, icons *IconResolver) TreeViewportContext {
	result := TreeViewportContext{width: width}
	if model == nil {
		return result
	}
	ancestor, shared := model.ViewportAncestor(rows)
	if ancestor == nil {
		return result
	}
	if shared {
		result.BaseDepth = ancestor.Depth + 1
	}
	for p := ancestor; p != nil && p != model.Root; p = p.Parent {
		label := p.Name
		if icons != nil {
			if icon := icons.ResolveIcon(p.Name, true, false, true).Icon; icon != "" {
				label = icon + " " + label
			}
		}
		result.labels = append(result.labels, label)
		result.paths = append(result.paths, p.Path)
	}
	slices.Reverse(result.labels)
	slices.Reverse(result.paths)
	sep := theme.DefaultGlyphs().BreadcrumbSep
	if sep == "" {
		sep = "▸"
	}
	separator := " " + sep + " "
	separatorW := rendering.VisibleWidth(separator)
	used := separatorW * (len(result.labels) - 1)
	for _, label := range result.labels {
		used += rendering.VisibleWidth(label)
	}
	hidden := false
	for len(result.labels) > 1 && (used > width || hidden && used+1+separatorW > width) {
		used -= rendering.VisibleWidth(result.labels[0]) + separatorW
		result.labels, result.paths = result.labels[1:], result.paths[1:]
		hidden = true
	}
	// Prefer the nearest parent over a prefix marker on exceptionally narrow
	// grids. The ellipsis has no path and cannot capture a click.
	if hidden && used+1+separatorW <= width {
		result.labels = append([]string{"…"}, result.labels...)
		result.paths = append([]string{""}, result.paths...)
	}
	result.Breadcrumb = rendering.TruncateLine(strings.Join(result.labels, separator), width)
	return result
}

// BreadcrumbLine builds the sticky common-ancestor path for a scrolled tree.
func BreadcrumbLine(model *FileTreeModel, width int) string {
	if model == nil {
		return ""
	}
	return ViewportContext(model, model.VisibleHeight(), width, nil).Breadcrumb
}

// BreadcrumbLineWithIcons uses the same icon policy as native tree rows.
func BreadcrumbLineWithIcons(model *FileTreeModel, width int, icons *IconResolver) string {
	if model == nil {
		return ""
	}
	return ViewportContext(model, model.VisibleHeight(), width, icons).Breadcrumb
}

// SyncScrollModel copies model scroll state into a ui.ScrollModel.
func SyncScrollModel(model *FileTreeModel, scroll *ui.ScrollModel, viewport int) {
	if model == nil || scroll == nil {
		return
	}
	n := len(model.VisibleNodes())
	scroll.SetViewport(viewport)
	scroll.SetContent(n)
	scroll.ScrollTo(model.ScrollOffset())
}

// ApplyScrollToModel writes scroll model offset back to the file tree model.
func ApplyScrollToModel(model *FileTreeModel, scroll *ui.ScrollModel) {
	if model == nil || scroll == nil {
		return
	}
	model.SetScrollOffset(scroll.Offset())
}

// SidebarContentWidth returns row width excluding gutter and scrollbar columns.
func SidebarContentWidth(totalWidth int) int {
	w := totalWidth - 2
	if w < 1 {
		return 1
	}
	return w
}

func isLastChild(node *TreeNode) bool {
	if node.Parent == nil {
		return true
	}
	children := node.Parent.Children
	if len(children) == 0 {
		return true
	}
	return children[len(children)-1] == node
}

func hasSiblingBelow(node *TreeNode, depth int) bool {
	if depth >= node.Depth {
		return false
	}
	childInPath := node
	targetDepth := depth + 1
	for childInPath.Depth > targetDepth {
		childInPath = childInPath.Parent
		if childInPath == nil {
			return false
		}
	}
	parent := childInPath.Parent
	if parent == nil {
		return false
	}
	found := false
	for _, child := range parent.Children {
		if child == childInPath {
			found = true
			continue
		}
		if found {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
