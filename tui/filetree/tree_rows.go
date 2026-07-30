package filetree

import (
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
	ShowIcons       bool
	ShowGitStatus   bool
	ShowDiagnostics bool
	UseNerdFont     bool
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
		if model != nil {
			remainingHOff = model.HScrollOffset()
		}
		for d := 0; d < node.Depth; d++ {
			colW := 1 + cfg.IndentWidth
			if remainingHOff >= colW {
				remainingHOff -= colW
				continue
			}
			if remainingHOff > 0 {
				remainingHOff = 0
			}
			if hasSiblingBelow(node, d) {
				buf.Set(x, y, cell.NewCell('│', indentFG, bg, 0))
			} else {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			}
			x++
			for s := 0; s < cfg.IndentWidth; s++ {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
		}
		if node.Depth >= 0 {
			if remainingHOff >= 4 {
				remainingHOff -= 4
			} else {
				if remainingHOff > 0 {
					remainingHOff = 0
				}
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
		}

		if node.IsDir && !(cfg.ShowIcons && icons != nil) {
			indicator := "▸"
			if node.IsExpanded {
				indicator = "▾"
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
		gitReserve, diagReserve := 0, 0
		if cfg.ShowGitStatus && displayGit != GitNone {
			gitReserve = rendering.VisibleWidth(StatusString(displayGit)) + 2
		}
		if cfg.ShowDiagnostics && node.DiagnosticCount > 0 {
			if node.DiagErrors > 0 {
				diagReserve += 1 + rendering.VisibleWidth(itoa(node.DiagErrors))
			}
			if node.DiagWarnings > 0 {
				diagReserve += 1 + rendering.VisibleWidth(itoa(node.DiagWarnings))
			}
		}
		nameMax := width - (x - ox) - gitReserve - diagReserve
		if nameMax < 3 {
			nameMax = 3
		}
		nameText := node.Name
		if rendering.VisibleWidth(node.Name) > nameMax {
			nameText = rendering.TruncateVisual(node.Name, nameMax, "…")
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
		x = buf.WriteString(x, y, nameText, nameFG, bg, nameAttr)

		if cfg.ShowGitStatus && displayGit != GitNone {
			gitStr := StatusString(displayGit)
			if gitStr != "" {
				gitFG := gitColor(tokens, displayGit)
				contentW := x - ox
				gitW := rendering.VisibleWidth(gitStr)
				gitX := ox + width - gitW - 1
				if gitX > contentW {
					for fx := x; fx < gitX; fx++ {
						buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
					}
					buf.WriteString(gitX, y, gitStr, gitFG, bg, 0)
					x = gitX + gitW
				}
			}
		}
		if cfg.ShowDiagnostics && node.DiagnosticCount > 0 {
			if node.DiagErrors > 0 {
				s := "✕" + itoa(node.DiagErrors)
				buf.WriteString(x, y, " "+s, tokens.TreeDiagError(), bg, 0)
				x += 1 + rendering.VisibleWidth(s)
			}
			if node.DiagWarnings > 0 {
				s := "⚠" + itoa(node.DiagWarnings)
				buf.WriteString(x, y, " "+s, tokens.TreeDiagWarning(), bg, 0)
			}
		}
		for fx := x; fx < ox+width; fx++ {
			buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
		}
	}
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

// BreadcrumbLine builds the sticky parent path header for a scrolled tree.
func BreadcrumbLine(model *FileTreeModel, width int) string {
	if model == nil || model.ScrollOffset() <= 0 {
		return ""
	}
	nodes := model.VisibleNodes()
	off := model.ScrollOffset()
	if off >= len(nodes) {
		return ""
	}
	first := nodes[off]
	var names []string
	for p := first.Parent; p != nil; p = p.Parent {
		names = append([]string{p.Name}, names...)
	}
	if len(names) == 0 {
		return ""
	}
	return rendering.TruncateLine(strings.Join(names, " ▸ "), width)
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
