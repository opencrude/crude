// Package filetree provides the file tree sidebar widget.
//
// This file implements the tree rendering system: indentation with connectors,
// icons, git status decorations, selection/cursor highlight, and scroll
// management.
package filetree

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

// ---------------------------------------------------------------------------
// TreeRenderer
// ---------------------------------------------------------------------------

// TreeRenderer renders a FileTreeModel into a Surface or ANSI string lines.
//
// It handles:
//   - Indentation with tree connectors (│, ├──, └──)
//   - Expand/collapse indicators (▸/▾ or NerdFont folder icons)
//   - File type icons via IconResolver
//   - File/directory name styling (bold for dirs, symlink colours)
//   - Right-aligned git status decoration
//   - Selection and cursor highlight (full-width background)
//   - Vertical scrolling with VSCode-style context around cursor
type TreeRenderer struct {
	model        *FileTreeModel
	theme        *theme.TreeColors
	styles       *theme.TreeStyles
	config       *theme.TreeConfig
	iconResolver *IconResolver

	width        int
	height       int
	scrollOffset int
	maxWidth     int
}

// NewTreeRenderer creates a TreeRenderer with the given model and icon
// resolver. Theme, styles, and config are initially nil – call ApplyTheme to
// set them. Defaults are used when theme fields are nil.
func NewTreeRenderer(model *FileTreeModel, iconResolver *IconResolver) *TreeRenderer {
	cfg := defaultTreeConfig()
	return &TreeRenderer{
		model:        model,
		iconResolver: iconResolver,
		config:       cfg,
	}
}

// defaultTreeConfig returns sensible defaults for TreeConfig when no theme
// is provided.
func defaultTreeConfig() *theme.TreeConfig {
	return &theme.TreeConfig{
		IndentWidth:   2,
		ShowIcons:     true,
		ShowGitStatus: true,
		ShowHidden:    false,
		DefaultWidth:  30,
		MinWidth:      20,
		MaxWidth:      60,
		UseNerdFont:   nil,
	}
}

// defaultTreeColors returns sensible defaults when no theme is provided.
func defaultTreeColors() *theme.TreeColors {
	// These match the DefaultDark theme but are hard-coded so the renderer
	// works without any theme at all.
	return &theme.TreeColors{
		DirFg:        lipgloss.Color("#79c0ff"),
		FileFg:       lipgloss.Color("#e6edf3"),
		SymlinkFg:    lipgloss.Color("#a0a8b7"),
		SelectedBg:   lipgloss.Color("#1e2a3a"),
		SelectedFg:   lipgloss.Color("#e6edf3"),
		CursorBg:     lipgloss.Color("#161b22"),
		CursorFg:     lipgloss.Color("#e6edf3"),
		IndentLine:   lipgloss.Color("#30363d"),
		GitModified:  lipgloss.Color("#d29922"),
		GitAdded:     lipgloss.Color("#7ee787"),
		GitDeleted:   lipgloss.Color("#ff7b72"),
		GitUntracked: lipgloss.Color("#636d83"),
		GitConflicted: lipgloss.Color("#ff7b72"),
		IconDir:      lipgloss.Color("#f0883e"),
		IconFile:     lipgloss.Color("#8b949e"),
		IconSymlink:  lipgloss.Color("#79c0ff"),
		BorderFg:     lipgloss.Color("#30363d"),
	}
}

// defaultTreeStyles returns lipgloss styles matching the default dark colours.
func defaultTreeStyles() *theme.TreeStyles {
	tc := defaultTreeColors()
	return &theme.TreeStyles{
		DirStyle:         lipgloss.NewStyle().Foreground(tc.DirFg).Bold(true),
		FileStyle:        lipgloss.NewStyle().Foreground(tc.FileFg),
		SelectedStyle:    lipgloss.NewStyle().Foreground(tc.SelectedFg).Background(tc.SelectedBg),
		CursorStyle:      lipgloss.NewStyle().Foreground(tc.CursorFg).Background(tc.CursorBg),
		GitModifiedStyle: lipgloss.NewStyle().Foreground(tc.GitModified),
		GitAddedStyle:    lipgloss.NewStyle().Foreground(tc.GitAdded),
		GitDeletedStyle:  lipgloss.NewStyle().Foreground(tc.GitDeleted),
		GitUntrackedStyle: lipgloss.NewStyle().Foreground(tc.GitUntracked).Italic(true),
	}
}

// ---------------------------------------------------------------------------
// ApplyTheme
// ---------------------------------------------------------------------------

// ApplyTheme extracts tree-specific colours, styles, and config from the
// given theme and applies them to the renderer. It also propagates theme
// icon colours to the IconResolver.
func (r *TreeRenderer) ApplyTheme(t *theme.Theme) {
	if t == nil {
		return
	}
	r.theme = &t.Tree.Colors
	r.styles = &t.Tree.Styles
	r.config = &t.Tree.Config
	if r.iconResolver != nil {
		r.iconResolver.ApplyTheme(r.theme)
	}
}

// ---------------------------------------------------------------------------
// Helper – colour conversion
// ---------------------------------------------------------------------------

// cellColor converts an image/color.Color (from the theme) to a cell.Color.
// If c is nil or produces a default colour, fallback is returned instead.
func cellColor(c color.Color, fallback cell.Color) cell.Color {
	if c == nil {
		return fallback
	}
	conv := rendering.ThemeColorToCell(c)
	if conv.IsDefault() {
		return fallback
	}
	return conv
}

// ---------------------------------------------------------------------------
// isLastChild / hasSiblingBelow
// ---------------------------------------------------------------------------

// isLastChild returns true when node is the last child of its parent.
// If node has no parent (root), returns true.
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

// hasSiblingBelow checks whether the ancestor of node at the given depth
// (depth must be < node.Depth) has a sibling that appears after it in its
// parent's children list.  This controls whether a vertical continuation
// line (│) is drawn at that indentation level.
//
// Example: for a node C (depth=3), hasSiblingBelow(C, 0) walks up 2 levels
// to depth=1 (A) and checks whether A has a sibling after it (E).
func hasSiblingBelow(node *TreeNode, depth int) bool {
	if depth >= node.Depth {
		return false
	}

	// Walk up to the node at depth+1 (the child whose siblings we inspect).
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

	// Scan for childInPath in the parent's children, then check for any
	// following sibling.
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

// ---------------------------------------------------------------------------
// formatLine – ANSI string builder
// ---------------------------------------------------------------------------

// formatLine renders a single tree row as an ANSI string.  It combines
// indentation, connector, expand/collapse indicator, file icon, name, and
// right-aligned git status into one styled string.
//
// width is the available column width; the returned string is padded or
// truncated to fit.  isCursor and isSelected control background highlighting.
func (r *TreeRenderer) formatLine(node *TreeNode, index int, width int, isCursor, isSelected bool) string {
	if node == nil {
		return strings.Repeat(" ", width)
	}

	var b strings.Builder

	// ---- colour & style helpers ----
	colors := r.theme
	if colors == nil {
		colors = defaultTreeColors()
	}
	cfg := r.config
	if cfg == nil {
		cfg = defaultTreeConfig()
	}

	// ---- indentation & connector ----
	indentColor := colors.IndentLine
	indentStyle := lipgloss.NewStyle().Foreground(indentColor)
	indentW := cfg.IndentWidth

	for d := 0; d < node.Depth; d++ {
		if hasSiblingBelow(node, d) {
			b.WriteString(indentStyle.Render("│"))
		} else {
			b.WriteString(" ")
		}
		b.WriteString(strings.Repeat(" ", indentW))
	}

	// Connector at the current level.
	if node.Depth > 0 {
		if isLastChild(node) {
			b.WriteString(indentStyle.Render("└"))
		} else {
			b.WriteString(indentStyle.Render("├"))
		}
		b.WriteString(indentStyle.Render("── "))
	}

	// ---- background highlight (cursor / selection) ----
	bgStyle := lipgloss.NewStyle()
	if isCursor {
		bgStyle = lipgloss.NewStyle().Background(colors.CursorBg)
	} else if isSelected {
		bgStyle = lipgloss.NewStyle().Background(colors.SelectedBg)
	}

	// ---- expand/collapse indicator ----
	// When icons are enabled and the resolver is available, the file-type
	// icon section below handles directory icons (including expand/collapse
	// state) via ResolveIcon. Skip this section in that case to avoid
	// rendering the directory icon twice.
	if node.IsDir && !(cfg.ShowIcons && r.iconResolver != nil) {
		var indicator string
		if r.iconResolver != nil && r.iconResolver.UseNerdFont {
			if node.IsExpanded {
				//  (nf-md-folder_open)
				indicator = "\uf07c"
			} else {
				//  (nf-md-folder)
				indicator = "\uf07b"
			}
		} else {
			if node.IsExpanded {
				indicator = "▾"
			} else {
				indicator = "▸"
			}
		}
		dirStyle := lipgloss.NewStyle().Foreground(colors.DirFg)
		if isCursor {
			dirStyle = dirStyle.Background(colors.CursorBg)
		} else if isSelected {
			dirStyle = dirStyle.Background(colors.SelectedBg)
		}
		b.WriteString(dirStyle.Render(indicator))
		b.WriteString(" ")
	}

	// ---- file type icon ----
	if cfg.ShowIcons && r.iconResolver != nil {
		iconRes := r.iconResolver.ResolveIcon(node.Name, node.IsDir, node.IsSymlink, node.IsExpanded)
		iconCol := iconRes.Color
		if iconCol == nil {
			iconCol = colors.FileFg
		}
		iconStyle := lipgloss.NewStyle().Foreground(iconCol)
		if isCursor {
			iconStyle = iconStyle.Background(colors.CursorBg)
		} else if isSelected {
			iconStyle = iconStyle.Background(colors.SelectedBg)
		}
		if iconRes.Icon != "" {
			b.WriteString(iconStyle.Render(iconRes.Icon))
			b.WriteString(" ")
		}
	}

	// ---- file / directory name ----
	var nameStyle lipgloss.Style
	if node.IsDir {
		nameStyle = lipgloss.NewStyle().Foreground(colors.DirFg).Bold(true)
	} else if node.IsSymlink {
		nameStyle = lipgloss.NewStyle().Foreground(colors.SymlinkFg)
	} else {
		nameStyle = lipgloss.NewStyle().Foreground(colors.FileFg)
	}
	if isCursor {
		nameStyle = nameStyle.Background(colors.CursorBg)
	} else if isSelected {
		nameStyle = nameStyle.Background(colors.SelectedBg)
	}
	b.WriteString(nameStyle.Render(node.Name))

	// ---- git status decoration (right-aligned) ----
	if cfg.ShowGitStatus && node.GitStatus != GitNone {
		gitStr := StatusString(node.GitStatus)
		if gitStr != "" {
			var gitStyle lipgloss.Style
			switch node.GitStatus {
			case GitModified:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitModified)
			case GitStaged:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitAdded)
			case GitDeleted:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitDeleted)
			case GitUntracked:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitUntracked)
			case GitConflicted:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitConflicted)
			case GitRenamed:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitModified)
			default:
				gitStyle = lipgloss.NewStyle().Foreground(colors.GitModified)
			}
			if isCursor {
				gitStyle = gitStyle.Background(colors.CursorBg)
			} else if isSelected {
				gitStyle = gitStyle.Background(colors.SelectedBg)
			}

			// Calculate the column where the git indicator should start
			// (right-aligned).
			raw := b.String()
			contentW := rendering.StringWidth(rendering.StripAnsi(raw))
			gitW := rendering.StringWidth(gitStr)

			// Leave at least 1 space of padding.
			padNeeded := 1
			gitStart := width - gitW - padNeeded
			if gitStart > contentW {
				pad := gitStart - contentW
				b.WriteString(strings.Repeat(" ", pad))
				b.WriteString(gitStyle.Render(gitStr))
			}
		}
	}

	// ---- ensure total width ----
	raw := b.String()
	visibleW := rendering.StringWidth(rendering.StripAnsi(raw))
	if visibleW < width {
		pad := strings.Repeat(" ", width-visibleW)
		if isCursor {
			pad = bgStyle.Render(pad)
		} else if isSelected {
			pad = bgStyle.Render(pad)
		}
		b.WriteString(pad)
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// RenderToLines – ANSI string slice
// ---------------------------------------------------------------------------

// RenderToLines renders the visible portion of the tree (from scrollOffset
// to scrollOffset+height) as a slice of ANSI strings.
//
// This is the simpler rendering path, useful for debugging or for Bubble
// Tea View() implementations that work with ANSI strings.  Each line is
// padded to the given width.
func (r *TreeRenderer) RenderToLines(width, height int) []string {
	if height <= 0 {
		return nil
	}

	nodes := r.model.VisibleNodes()
	cursorIdx := r.model.CursorIndex()
	selectedPath := ""
	if sel := r.model.SelectedNode(); sel != nil {
		selectedPath = sel.Path
	}

	// Clamp scroll offset.
	total := len(nodes)
	if r.scrollOffset >= total && total > 0 {
		r.scrollOffset = total - 1
	}
	if r.scrollOffset < 0 {
		r.scrollOffset = 0
	}

	lines := make([]string, height)
	for i := 0; i < height; i++ {
		nodeIdx := r.scrollOffset + i
		if nodeIdx >= total {
			lines[i] = strings.Repeat(" ", width)
			continue
		}
		node := nodes[nodeIdx]
		isCursor := nodeIdx == cursorIdx
		isSelected := node.Path == selectedPath && !isCursor
		lines[i] = r.formatLine(node, nodeIdx, width, isCursor, isSelected)
	}

	return lines
}

// ---------------------------------------------------------------------------
// Render – surface Widget pattern
// ---------------------------------------------------------------------------

// Render draws the visible portion of the tree into the given Surface.
//
// This implements the Widget interface rendering pattern. It writes
// individual cells to the surface buffer, handling indentation, connectors,
// icons, name styling, git status, and background highlights.
//
// The surface is pre-sized to the allocated space. Only the visible window
// (scrollOffset → scrollOffset+height) is rendered.
func (r *TreeRenderer) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	buf := surf.Buffer()
	w := surf.Width()
	h := surf.Height()
	if w <= 0 || h <= 0 {
		return
	}

	nodes := r.model.VisibleNodes()
	cursorIdx := r.model.CursorIndex()
	selectedPath := ""
	if sel := r.model.SelectedNode(); sel != nil {
		selectedPath = sel.Path
	}

	colors := r.theme
	if colors == nil {
		colors = defaultTreeColors()
	}
	cfg := r.config
	if cfg == nil {
		cfg = defaultTreeConfig()
	}
	indentW := cfg.IndentWidth

	// Resolvers.
	iconRes := r.iconResolver

	// ---- helper: convert theme colour to cell colour ----
	tc := func(c color.Color, fb cell.Color) cell.Color {
		return cellColor(c, fb)
	}

	// ---- render loop ----
	for i := 0; i < h; i++ {
		nodeIdx := r.scrollOffset + i
		if nodeIdx >= len(nodes) {
			// Fill remaining rows with transparent blanks.
			for x := 0; x < w; x++ {
				buf.Set(x, i, cell.TransparentBlank())
			}
			continue
		}
		node := nodes[nodeIdx]
		isCursor := nodeIdx == cursorIdx
		isSelected := node.Path == selectedPath && !isCursor

		// Determine background colour for this row.
		bg := cell.NoColor
		if isCursor {
			bg = tc(colors.CursorBg, cell.Color{Type: cell.ColorPalette, Palette: 236})
		} else if isSelected {
			bg = tc(colors.SelectedBg, cell.Color{Type: cell.ColorPalette, Palette: 235})
		}
		_ = bg // used when writing cells

		x := 0
		y := i

		// ---- indentation & connector ----
		indentCellColor := tc(colors.IndentLine, cell.Color{Type: cell.ColorPalette, Palette: 237})

		for d := 0; d < node.Depth; d++ {
			if hasSiblingBelow(node, d) {
				buf.Set(x, y, cell.NewCell('│', indentCellColor, bg, 0))
			} else {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			}
			x++
			for s := 0; s < indentW; s++ {
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
		}

		// Connector at the current node's depth.
		if node.Depth > 0 {
			if isLastChild(node) {
				buf.Set(x, y, cell.NewCell('└', indentCellColor, bg, 0))
			} else {
				buf.Set(x, y, cell.NewCell('├', indentCellColor, bg, 0))
			}
			x++
			buf.Set(x, y, cell.NewCell('─', indentCellColor, bg, 0))
			x++
			buf.Set(x, y, cell.NewCell('─', indentCellColor, bg, 0))
			x++
			buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			x++
		}

		// ---- expand/collapse indicator ----
		// When icons are enabled and the resolver is available, the file-type
		// icon section below handles directory icons (including expand/collapse
		// state) via ResolveIcon. Skip this section in that case to avoid
		// rendering the directory icon twice.
		if node.IsDir && !(cfg.ShowIcons && iconRes != nil) {
			var indicator string
			if iconRes != nil && iconRes.UseNerdFont {
				if node.IsExpanded {
					indicator = "\uf07c" // 
				} else {
					indicator = "\uf07b" // 
				}
			} else {
				if node.IsExpanded {
					indicator = "▾"
				} else {
					indicator = "▸"
				}
			}
			dirCellColor := tc(colors.DirFg, cell.NewTrueColor(121, 192, 255))
			x = buf.WriteString(x, y, indicator, dirCellColor, bg, 0)
			buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
			x++
		}

		// ---- file type icon ----
		if cfg.ShowIcons && iconRes != nil {
			iconResult := iconRes.ResolveIcon(node.Name, node.IsDir, node.IsSymlink, node.IsExpanded)
			iconCellColor := tc(iconResult.Color, cell.NoColor)
			if iconCellColor.IsDefault() {
				iconCellColor = tc(colors.FileFg, cell.NewTrueColor(139, 148, 158))
			}
			if iconResult.Icon != "" {
				x = buf.WriteString(x, y, iconResult.Icon, iconCellColor, bg, 0)
				buf.Set(x, y, cell.NewCell(' ', cell.NoColor, bg, 0))
				x++
			}
		}

		// ---- file / directory name ----
		var nameCellColor cell.Color
		nameAttr := cell.Attrs(0)
		if node.IsDir {
			nameCellColor = tc(colors.DirFg, cell.NewTrueColor(121, 192, 255))
			nameAttr = cell.AttrBold
		} else if node.IsSymlink {
			nameCellColor = tc(colors.SymlinkFg, cell.NewTrueColor(160, 168, 183))
		} else {
			nameCellColor = tc(colors.FileFg, cell.NewTrueColor(230, 237, 243))
		}
		x = buf.WriteString(x, y, node.Name, nameCellColor, bg, nameAttr)

		// ---- git status decoration (right-aligned) ----
		if cfg.ShowGitStatus && node.GitStatus != GitNone {
			gitStr := StatusString(node.GitStatus)
			if gitStr != "" {
				var gitCellColor cell.Color
				switch node.GitStatus {
				case GitModified:
					gitCellColor = tc(colors.GitModified, cell.NewTrueColor(210, 153, 34))
				case GitStaged:
					gitCellColor = tc(colors.GitAdded, cell.NewTrueColor(126, 231, 135))
				case GitDeleted:
					gitCellColor = tc(colors.GitDeleted, cell.NewTrueColor(255, 123, 114))
				case GitUntracked:
					gitCellColor = tc(colors.GitUntracked, cell.NewTrueColor(99, 109, 131))
				case GitConflicted:
					gitCellColor = tc(colors.GitConflicted, cell.NewTrueColor(255, 123, 114))
				case GitRenamed:
					gitCellColor = tc(colors.GitModified, cell.NewTrueColor(210, 153, 34))
				default:
					gitCellColor = tc(colors.GitModified, cell.NewTrueColor(210, 153, 34))
				}

				// Compute right-aligned position.
				gitW := len([]rune(gitStr))
				gitX := w - gitW - 1 // one space of right padding
				if gitX > x {
					// Fill gap between name and git indicator.
					for fx := x; fx < gitX; fx++ {
						buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
					}
					buf.WriteString(gitX, y, gitStr, gitCellColor, bg, 0)
					x = gitX + gitW
				}
			}
		}

		// ---- fill remaining cells on this row with background ----
		for fx := x; fx < w; fx++ {
			buf.Set(fx, y, cell.NewCell(' ', cell.NoColor, bg, 0))
		}
	}
}

// ---------------------------------------------------------------------------
// scroll management
// ---------------------------------------------------------------------------

// Measure reports the preferred size given available space.
// This implements the widget.Widget interface.
func (r *TreeRenderer) Measure(availW, availH int) (int, int) {
	return r.width, r.height
}

// ComputeLayout calculates the scroll offset based on the cursor position and
// returns the number of visible items that fit in the given height.
//
// The logic ensures the cursor is visible after layout:
//   - If the cursor is above the viewport, scroll up to bring it into view
//     with 3 lines of context above.
//   - If the cursor is below the viewport, scroll down to bring it into view
//     with 3 lines of context below.
func (r *TreeRenderer) ComputeLayout(width, height int) int {
	if width > r.maxWidth {
		r.maxWidth = width
	}
	r.width = width
	r.height = height

	r.ScrollToCursor()

	nodes := r.model.VisibleNodes()
	visible := height
	if r.scrollOffset+visible > len(nodes) {
		visible = len(nodes) - r.scrollOffset
	}
	if visible < 0 {
		visible = 0
	}
	return visible
}

// ScrollToCursor adjusts scrollOffset so that the cursor item is visible.
//
// VSCode-style: keeps 3 lines of context above/below the cursor when
// possible.  If the cursor is already visible, no adjustment is made.
func (r *TreeRenderer) ScrollToCursor() {
	nodes := r.model.VisibleNodes()
	if len(nodes) == 0 {
		r.scrollOffset = 0
		return
	}

	cursorIdx := r.model.CursorIndex()

	// Clamp cursor to valid range.
	if cursorIdx < 0 {
		cursorIdx = 0
	}
	if cursorIdx >= len(nodes) {
		cursorIdx = len(nodes) - 1
	}

	contextLines := 3

	// If cursor is above the viewport, scroll up.
	if cursorIdx < r.scrollOffset+contextLines {
		r.scrollOffset = cursorIdx - contextLines
		if r.scrollOffset < 0 {
			r.scrollOffset = 0
		}
		return
	}

	// If cursor is below the viewport, scroll down.
	if cursorIdx >= r.scrollOffset+r.height-contextLines {
		r.scrollOffset = cursorIdx - r.height + contextLines + 1
		if r.scrollOffset < 0 {
			r.scrollOffset = 0
		}
		return
	}
}

// GetScrollbarInfo returns the total item count, the number of visible items,
// and the current scroll offset — all parameters needed by the scrollbar
// widget.
func (r *TreeRenderer) GetScrollbarInfo() (totalItems, visibleItems, scrollOff int) {
	nodes := r.model.VisibleNodes()
	return len(nodes), r.height, r.scrollOffset
}

// ---------------------------------------------------------------------------
// helpers for external use
// ---------------------------------------------------------------------------

// Width returns the configured render width.
func (r *TreeRenderer) Width() int {
	return r.width
}

// Height returns the configured render height.
func (r *TreeRenderer) Height() int {
	return r.height
}

// ScrollOffset returns the current vertical scroll position.
func (r *TreeRenderer) ScrollOffset() int {
	return r.scrollOffset
}

// SetScrollOffset sets the vertical scroll position. It is clamped to the
// valid range [0, max(0, len-nodes - height)].
func (r *TreeRenderer) SetScrollOffset(off int) {
	nodes := r.model.VisibleNodes()
	maxOff := len(nodes) - r.height
	if maxOff < 0 {
		maxOff = 0
	}
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	r.scrollOffset = off
}

// String returns a human-readable description of the renderer's state.
func (r *TreeRenderer) String() string {
	nodes := r.model.VisibleNodes()
	return fmt.Sprintf("TreeRenderer{total=%d, offset=%d, h=%d, w=%d}",
		len(nodes), r.scrollOffset, r.height, r.width)
}
