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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
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
//   - Sticky breadcrumb header when scrolled deep into the tree
type TreeRenderer struct {
	model        *FileTreeModel
	theme        *theme.TreeColors
	styles       *theme.TreeStyles
	config       *theme.TreeConfig
	iconResolver *IconResolver

	width            int
	height           int
	scrollOffset     int
	maxWidth         int
	sb               scrollbar.ScrollBar
	sbTrackHeight    int // effective scrollbar track height (0 = use view height)
	lastScrollOffset int // for scrollbar change detection
	lastTotalNodes   int // for scrollbar change detection

	breadcrumbActive bool // true when scrollOffset > 0, a breadcrumb line replaces the first content line

	hscrollOffset int // horizontal scroll offset for deeply nested items
	maxContentW   int // widest visible line for horizontal scroll tracking
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
		sb:           scrollbar.New(0),
	}
}

// defaultTreeConfig returns sensible defaults for TreeConfig when no theme
// is provided.
func defaultTreeConfig() *theme.TreeConfig {
	return &theme.TreeConfig{
		IndentWidth:   2,
		ShowIcons:     true,
		ShowGitStatus: true,
		ShowDiagnostics: true,
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
		SelectedBg:   lipgloss.Color("#264040"),
		SelectedFg:   lipgloss.Color("#e6edf3"),
		CursorBg:     lipgloss.Color("#2d4f4f"),
		CursorFg:     lipgloss.Color("#e6edf3"),
		Hover:        lipgloss.Color("#21262d"),
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
		DiagError:    lipgloss.Color("#ff7b72"),
		DiagWarning:  lipgloss.Color("#d29922"),
		DiagInfo:     lipgloss.Color("#8b949e"),
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
// truncated to fit.  hOff is the horizontal scroll offset — when > 0,
// indent/connector columns are reduced and the name gets more room.
// isCursor and isSelected control background highlighting.
func (r *TreeRenderer) formatLine(node *TreeNode, index int, width int, hOff int, isCursor, isSelected, isHovered bool) string {
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

	// ---- background style for indent/connector cells ----
	//
	// Background priority (highest → lowest):
	//   1. cursor (the keyboard-focused row)         — CursorBg
	//   2. selected (multi-select highlight)         — SelectedBg
	//   3. hovered (mouse cursor over the row)       — Hover
	//   4. none                                       — transparent
	//
	// Hover is the weakest emphasis layer — it just tells the user
	// "if you click here, this is what you'll get". Cursor and
	// selected both win over hover because they're the active
	// "this is what you're working on" indicators.
	bgFn := func(s lipgloss.Style) lipgloss.Style {
		switch {
		case isCursor:
			return s.Background(colors.CursorBg)
		case isSelected:
			return s.Background(colors.SelectedBg)
		case isHovered:
			return s.Background(colors.Hover)
		}
		return s
	}

	// ---- indentation & connector ----
	indentColor := colors.IndentLine
	indentStyle := bgFn(lipgloss.NewStyle().Foreground(indentColor))
	noColorStyle := bgFn(lipgloss.NewStyle())
	indentW := cfg.IndentWidth

	remainingHOff := hOff

	for d := 0; d < node.Depth; d++ {
		colW := 1 + indentW
		if remainingHOff >= colW {
			remainingHOff -= colW
			continue
		}
		if remainingHOff > 0 {
			// Partially visible indent column — skip the remainder.
			remainingHOff = 0
		}
		if hasSiblingBelow(node, d) {
			b.WriteString(indentStyle.Render("│"))
		} else {
			b.WriteString(noColorStyle.Render(" "))
		}
		b.WriteString(noColorStyle.Render(strings.Repeat(" ", indentW)))
	}

	// Connector at the current level.
	if node.Depth >= 0 {
		if remainingHOff >= 4 {
			remainingHOff -= 4
		} else {
			if remainingHOff > 0 {
				remainingHOff = 0
			}
			if isLastChild(node) {
				b.WriteString(indentStyle.Render("└── "))
			} else {
				b.WriteString(indentStyle.Render("├── "))
			}
		}
	}

	// ---- expand/collapse indicator ----
	if node.IsDir && !(cfg.ShowIcons && r.iconResolver != nil) {
		var indicator string
		if r.iconResolver != nil && r.iconResolver.UseNerdFont {
			if node.IsExpanded {
				indicator = "\uf07c"
			} else {
				indicator = "\uf07b"
			}
		} else {
			if node.IsExpanded {
				indicator = "▾"
			} else {
				indicator = "▸"
			}
		}
		indW := rendering.VisibleWidth(indicator)
		if remainingHOff >= indW+1 {
			remainingHOff -= indW + 1
		} else {
			if remainingHOff > 0 {
				remainingHOff = 0
			}
			dirStyle := lipgloss.NewStyle().Foreground(colors.DirFg)
			switch {
			case isCursor:
				dirStyle = dirStyle.Background(colors.CursorBg)
			case isSelected:
				dirStyle = dirStyle.Background(colors.SelectedBg)
			case isHovered:
				dirStyle = dirStyle.Background(colors.Hover)
			}
			b.WriteString(dirStyle.Render(indicator))
			b.WriteString(noColorStyle.Render(" "))
		}
	}

	// ---- file type icon ----
	if cfg.ShowIcons && r.iconResolver != nil {
		iconRes := r.iconResolver.ResolveIcon(node.Name, node.IsDir, node.IsSymlink, node.IsExpanded)
		iconCol := iconRes.Color
		if iconCol == nil {
			iconCol = colors.FileFg
		}
		if iconRes.Icon != "" {
			icW := rendering.VisibleWidth(iconRes.Icon)
			if remainingHOff >= icW+1 {
				remainingHOff -= icW + 1
			} else {
				if remainingHOff > 0 {
					remainingHOff = 0
				}
			iconStyle := lipgloss.NewStyle().Foreground(iconCol)
			switch {
			case isCursor:
				iconStyle = iconStyle.Background(colors.CursorBg)
			case isSelected:
				iconStyle = iconStyle.Background(colors.SelectedBg)
			case isHovered:
				iconStyle = iconStyle.Background(colors.Hover)
			}
			b.WriteString(iconStyle.Render(iconRes.Icon))
				b.WriteString(noColorStyle.Render(" "))
			}
		}
	}

	// ---- file / directory name ----
	usedW := rendering.VisibleWidth(rendering.StripAnsi(b.String()))
	gitReserveW := 0
	if cfg.ShowGitStatus {
		displayStatus := node.GitStatus
		if node.IsDir && displayStatus == GitNone && node.AggregateStatus != GitNone {
			displayStatus = node.AggregateStatus
		}
		if displayStatus != GitNone {
			gitReserveW = rendering.VisibleWidth(StatusString(displayStatus)) + 2
		}
	}
	diagReserveW := 0
	if cfg.ShowDiagnostics && node.DiagnosticCount > 0 {
		diagReserveW = 6 // " ✕N ⚠N" approximate, min space
		if node.DiagErrors > 0 {
			ew := rendering.VisibleWidth(itoa(node.DiagErrors))
			diagReserveW += 1 + ew
		}
		if node.DiagWarnings > 0 {
			ww := rendering.VisibleWidth(itoa(node.DiagWarnings))
			diagReserveW += 1 + ww
		}
	}
	nameMaxW := width - usedW - gitReserveW - diagReserveW
	if nameMaxW < 3 {
		nameMaxW = 3
	}
	nameText := node.Name
	if rendering.VisibleWidth(node.Name) > nameMaxW {
		nameText = rendering.TruncateVisual(node.Name, nameMaxW, "…")
	}

	var nameStyle lipgloss.Style
	if node.IsDir {
		nameStyle = lipgloss.NewStyle().Foreground(colors.DirFg).Bold(true)
	} else if node.IsSymlink {
		nameStyle = lipgloss.NewStyle().Foreground(colors.SymlinkFg)
	} else {
		nameStyle = lipgloss.NewStyle().Foreground(colors.FileFg)
	}
	switch {
	case isCursor:
		nameStyle = nameStyle.Background(colors.CursorBg)
	case isSelected:
		nameStyle = nameStyle.Background(colors.SelectedBg)
	case isHovered:
		nameStyle = nameStyle.Background(colors.Hover)
	}
	b.WriteString(nameStyle.Render(nameText))

	// ---- right-aligned decorations: git status + diagnostic counts ----
	var decorations []string

	// Git status.
	displayStatus := node.GitStatus
	if node.IsDir && displayStatus == GitNone && node.AggregateStatus != GitNone {
		displayStatus = node.AggregateStatus
	}
	if cfg.ShowGitStatus && displayStatus != GitNone {
		gitStr := StatusString(displayStatus)
		if gitStr != "" {
			var gitStyle lipgloss.Style
			switch displayStatus {
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
			switch {
			case isCursor:
				gitStyle = gitStyle.Background(colors.CursorBg)
			case isSelected:
				gitStyle = gitStyle.Background(colors.SelectedBg)
			case isHovered:
				gitStyle = gitStyle.Background(colors.Hover)
			}
			decorations = append(decorations, gitStyle.Render(gitStr))
		}
	}

	// Diagnostic counts (real, from LSP — not fake).
	if cfg.ShowDiagnostics && node.DiagnosticCount > 0 {
		if node.DiagErrors > 0 {
			errStyle := lipgloss.NewStyle().Foreground(colors.DiagError)
			switch {
			case isCursor:
				errStyle = errStyle.Background(colors.CursorBg)
			case isSelected:
				errStyle = errStyle.Background(colors.SelectedBg)
			case isHovered:
				errStyle = errStyle.Background(colors.Hover)
			}
			decorations = append(decorations, errStyle.Render("✕"+itoa(node.DiagErrors)))
		}
		if node.DiagWarnings > 0 {
			warnStyle := lipgloss.NewStyle().Foreground(colors.DiagWarning)
			switch {
			case isCursor:
				warnStyle = warnStyle.Background(colors.CursorBg)
			case isSelected:
				warnStyle = warnStyle.Background(colors.SelectedBg)
			case isHovered:
				warnStyle = warnStyle.Background(colors.Hover)
			}
			decorations = append(decorations, warnStyle.Render("⚠"+itoa(node.DiagWarnings)))
		}
	}

	if len(decorations) > 0 {
		decoStr := noColorStyle.Render(" ") + strings.Join(decorations, noColorStyle.Render(" "))
		raw := b.String()
		contentW := rendering.VisibleWidth(rendering.StripAnsi(raw))
		decoW := rendering.VisibleWidth(rendering.StripAnsi(decoStr))
		decoStart := width - decoW
		if decoStart > contentW {
			pad := decoStart - contentW
			b.WriteString(noColorStyle.Render(strings.Repeat(" ", pad)))
			b.WriteString(decoStr)
		}
	}

	// ---- ensure total width ----
	raw := b.String()
	visibleW := rendering.VisibleWidth(rendering.StripAnsi(raw))
	if visibleW < width {
		pad := noColorStyle.Render(strings.Repeat(" ", width-visibleW))
		b.WriteString(pad)
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// RenderToLines – ANSI string slice
// ---------------------------------------------------------------------------

// breadcrumbLine renders the sticky parent-hierarchy header shown when the
// tree is scrolled past the root.  It displays the chain of ancestor directory
// names leading to the first visible node, using '▸' as a separator.
func (r *TreeRenderer) breadcrumbLine(width int) string {
	nodes := r.model.VisibleNodes()
	if r.scrollOffset <= 0 || r.scrollOffset >= len(nodes) {
		return strings.Repeat(" ", width)
	}

	colors := r.theme
	if colors == nil {
		colors = defaultTreeColors()
	}

	first := nodes[r.scrollOffset]
	var names []string
	for p := first.Parent; p != nil; p = p.Parent {
		names = append([]string{p.Name}, names...)
	}
	if len(names) == 0 {
		return strings.Repeat(" ", width)
	}

	bc := strings.Join(names, " ▸ ")
	style := lipgloss.NewStyle().Foreground(colors.DirFg).Bold(true)
	rendered := style.Render(bc)
	visW := rendering.VisibleWidth(rendering.StripAnsi(rendered))
	if visW < width {
		rendered += strings.Repeat(" ", width-visW)
	}
	if visW > width {
		rendered = rendering.TruncateVisual(rendered, width, "…")
	}
	return rendered
}

// RenderToLines renders the visible portion of the tree (from scrollOffset
// to scrollOffset+height) as a slice of ANSI strings.
//
// When scrollOffset > 0, the first rendered line is a sticky breadcrumb
// header showing the parent-directory chain of the first visible node.
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

	// Sync scroll offset from the model (single source of truth).
	r.scrollOffset = r.model.ScrollOffset()

	total := len(nodes)
	if r.scrollOffset >= total && total > 0 {
		r.scrollOffset = total - 1
	}
	if r.scrollOffset < 0 {
		r.scrollOffset = 0
	}

	// Show breadcrumb when scrolled past the root.
	r.breadcrumbActive = r.scrollOffset > 0 && total > 0

	// Sync horizontal scroll offset from model.
	r.hscrollOffset = r.model.HScrollOffset()

	// Update scrollbar state — only call SetScrollInfo when position or
	// content size actually changes to avoid resetting the fade timer on
	// every render frame.
	sbW := scrollbar.ScrollbarWidth
	contentW := width - sbW
	trackH := r.sbTrackHeight
	if trackH <= 0 {
		trackH = height
	}
	r.sb.SetTrackHeight(trackH)
	if r.scrollOffset != r.lastScrollOffset || total != r.lastTotalNodes {
		r.sb.SetScrollInfo(total, trackH, r.scrollOffset)
		r.lastScrollOffset = r.scrollOffset
		r.lastTotalNodes = total
	} else {
		r.sb.Touch()
	}
	sbView := r.sb.View()
	sbLines := strings.Split(sbView, "\n")

	bc := int(0)
	if r.breadcrumbActive {
		bc = 1
	}

	// Track maximum content width for horizontal scroll.
	r.maxContentW = 0
	iW := 2
	if r.config != nil && r.config.IndentWidth > 0 {
		iW = r.config.IndentWidth
	}
	for _, node := range nodes {
		d := node.Depth
		if d < 0 {
			d = 0
		}
		w := d*(iW+1) + 4 + rendering.VisibleWidth(node.Name) + 3
		if w > r.maxContentW {
			r.maxContentW = w
		}
	}

	hoverIdx := r.model.Hover.Index
	lines := make([]string, height)
	for i := 0; i < height; i++ {
		var line string
		if r.breadcrumbActive && i == 0 {
			line = r.breadcrumbLine(contentW)
		} else {
			nodeIdx := r.scrollOffset + i - bc
			if nodeIdx >= 0 && nodeIdx < total {
				node := nodes[nodeIdx]
				isCursor := nodeIdx == cursorIdx
				isSelected := node.Path == selectedPath && !isCursor
				isHovered := nodeIdx == hoverIdx
				line = r.formatLine(node, nodeIdx, contentW, r.hscrollOffset, isCursor, isSelected, isHovered)
			} else {
				line = strings.Repeat(" ", contentW)
			}
		}
		sbChar := " "
		if i < len(sbLines) && sbLines[i] != "" {
			sbChar = sbLines[i]
		}
		lines[i] = line + sbChar
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
	hoverIdx := r.model.Hover.Index
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
		isHovered := nodeIdx == hoverIdx

		// Determine background colour for this row.
		// Priority: cursor > selected > hovered. See formatLine for
		// the rationale behind this ordering.
		bg := cell.NoColor
		switch {
		case isCursor:
			bg = tc(colors.CursorBg, cell.Color{Type: cell.ColorPalette, Palette: 236})
		case isSelected:
			bg = tc(colors.SelectedBg, cell.Color{Type: cell.ColorPalette, Palette: 235})
		case isHovered:
			bg = tc(colors.Hover, cell.Color{Type: cell.ColorPalette, Palette: 237})
		}

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
		if node.Depth >= 0 {
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
		displayStatus := node.GitStatus
		if node.IsDir && displayStatus == GitNone && node.AggregateStatus != GitNone {
			displayStatus = node.AggregateStatus
		}
		if cfg.ShowGitStatus && displayStatus != GitNone {
			gitStr := StatusString(displayStatus)
			if gitStr != "" {
				var gitCellColor cell.Color
				switch displayStatus {
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
				gitW := rendering.VisibleWidth(gitStr)
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
// Gentle edge-based scrolling: only scrolls when the cursor is strictly
// outside the viewport — scroll up by 1 when cursor is above the top,
// scroll down by 1 when cursor is below the bottom.  Page-sized movements
// (HandleScrollbarMsg, GoToTop, etc.) set scrollOffset directly.
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

	// If cursor is strictly above the viewport, scroll up by 1.
	if cursorIdx < r.scrollOffset {
		r.scrollOffset = cursorIdx
		return
	}

	// If cursor is strictly below the viewport, scroll down by 1.
	if cursorIdx >= r.scrollOffset+r.height {
		r.scrollOffset = cursorIdx - r.height + 1
		if r.scrollOffset < 0 {
			r.scrollOffset = 0
		}
		return
	}
}

// GetScrollbarInfo returns the total item count, the number of visible items,
// and the current scroll offset — all parameters needed by the scrollbar
// widget. Uses the model as the source of truth for scroll offset.
func (r *TreeRenderer) GetScrollbarInfo() (totalItems, visibleItems, scrollOff int) {
	nodes := r.model.VisibleNodes()
	return len(nodes), r.height, r.model.ScrollOffset()
}

// HandleScrollbarMsg forwards a mouse message to the scrollbar widget for
// click-to-jump and drag-scrolling. Returns a tea.Cmd for fade/hide tick
// timers, or nil if no action was taken.
// The scrollbar's Y coordinate is expected to be content-relative
// (Y=0 = first content row).
func (r *TreeRenderer) HandleScrollbarMsg(msg tea.Msg) tea.Cmd {
	updated, cmd := r.sb.Update(msg)
	r.sb = updated
	if r.sb.ConsumeScrollChanged() {
		r.model.SetScrollOffset(r.sb.GetScrollY())
		r.scrollOffset = r.model.ScrollOffset()
	}
	return cmd
}

// HandleScrollbarTick forwards fade/hide timer ticks to the scrollbar.
func (r *TreeRenderer) HandleScrollbarTick(msg tea.Msg) tea.Cmd {
	updated, cmd := r.sb.Update(msg)
	r.sb = updated
	return cmd
}

// SetScrollbarContentHeight sets the effective scrollbar track height.
// This should be the number of visible tree content rows on screen.
// Pass 0 to reset to the view height.
func (r *TreeRenderer) SetScrollbarContentHeight(h int) {
	r.sbTrackHeight = h
}

// SetSize updates the renderer's allocated width and height before rendering.
// This is used by the differential compositor when the sidebar surface is
// resized independently of the rest of the screen.
func (r *TreeRenderer) SetSize(width, height int) {
	if width != r.width || height != r.height {
		r.width = width
		r.height = height
		r.lastScrollOffset = -1 // force scrollbar recalculation
	}
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

// itoa converts an integer to a string. Avoids fmt.Sprintf overhead.
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
