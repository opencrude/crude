// Package shelltree provides the native file-tree sidebar shell surface.
package shelltree

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "shelltree"
	FilterID    ui.ComponentID = "shelltree/filter"
	TreeID      ui.ComponentID = "shelltree/tree"
	ScrollbarID ui.ComponentID = "shelltree/scrollbar"
	DividerID   ui.ComponentID = "shelltree/divider"
)

// Screen paints the workspace file tree, gutter, scrollbar, and divider.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	model      *filetree.FileTreeModel
	icons      *filetree.IconResolver
	cfg        filetree.PaintConfig
	width      int
	height     int
	visible    bool
	breadcrumb string

	filter    *treeFilterField
	tree      *fileTreeView
	scrollbar *ui.ScrollbarControl
	split     *ui.SplitModel
	divider   *ui.SplitDivider
}

type treeFilterField struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

type fileTreeView struct {
	ui.Base
	owner *Screen
	tree  *ui.Tree
}

func newFileTreeView(owner *Screen, tree *ui.Tree) *fileTreeView {
	return &fileTreeView{Base: ui.NewBase(TreeID), owner: owner, tree: tree}
}

func (v *fileTreeView) Children() []ui.Component { return nil }

func (v *fileTreeView) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return v.tree.Measure(ui.LayoutContext{}, cs)
}

func (v *fileTreeView) Layout(ctx ui.LayoutContext, r ui.Rect) {
	v.SetBounds(r)
	v.tree.Layout(ctx, r)
}

func (v *fileTreeView) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	v.tree.Paint(buf, ox, oy, ctx)
	v.paintFilterEmptyState(buf, ox, oy)
}

// paintFilterEmptyState shows a hint row when an active filter matches
// nothing, so the sidebar does not silently go blank.
func (v *fileTreeView) paintFilterEmptyState(buf *surface.CellBuffer, ox, oy int) {
	if v.owner == nil || v.owner.model == nil || buf == nil {
		return
	}
	model := v.owner.model
	if model.Filter() == "" || len(model.VisibleNodes()) > 0 {
		return
	}
	r := v.Bounds()
	if r.Empty() {
		return
	}
	tokens := v.owner.tokens
	text := rendering.TruncateLine("No matches", max(1, r.W-1))
	buf.WriteString(ox+r.X+1, oy+r.Y, text, tokens.Color(ui.ColorMuted), tokens.Color(ui.ColorBg), 0)
}

func (v *fileTreeView) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if v.owner == nil || v.owner.model == nil {
		return ui.EventResult{}
	}
	localY := ctx.LocalY
	// ctx.LocalY comes from the Host hit tree relative to the tree view's own
	// screen rect. The shell places that rect BELOW the filter row (treeY=1),
	// and the filter row is a sibling painted by the Screen above this rect —
	// it is not inside the tree view's bounds. So Host-local Y 0 is already
	// the first tree row and no filter offset may be applied. Only the
	// breadcrumb is painted by ui.Tree inside the tree view's own rect (as its
	// first row), so that one offset translates Host-local row into the
	// filetree model's item-row space.
	if v.owner.breadcrumb != "" {
		if localY == 0 {
			v.owner.clearHover()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		localY--
	}
	switch evt.Kind {
	case ui.EventKey:
		filterWas := v.owner.model.FilterActive()
		cmd := v.owner.model.Update(filetree.TreeKeyPressMsg{Key: evt.Key})
		v.owner.syncFromModel()
		inv := ui.InvalidatePaint
		var action *ui.Action
		if !filterWas && v.owner.model.FilterActive() {
			action = &ui.Action{ID: "ui.focus", Payload: FilterID}
			inv |= ui.InvalidateFocus
		}
		return ui.EventResult{Handled: true, Cmd: cmd, Action: action, Invalidate: inv}
	default:
		cmd := v.owner.model.Update(filetree.TreeMouseMsg{Mouse: translateMouseY(evt, localY)})
		v.owner.syncFromModel()
		return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
	}
}

func (v *fileTreeView) Semantics() ui.Semantics { return v.tree.Semantics() }

func translateMouseY(evt ui.Event, y int) tea.Msg {
	switch evt.Kind {
	case ui.EventMouseClick:
		m := evt.MouseClick
		return tea.MouseClickMsg{X: m.X, Y: y, Button: m.Button}
	case ui.EventMouseMotion:
		m := evt.MouseMotion
		return tea.MouseMotionMsg{X: m.X, Y: y, Button: m.Button}
	case ui.EventMouseRelease:
		m := evt.MouseRelease
		return tea.MouseReleaseMsg{X: m.X, Y: y, Button: m.Button}
	case ui.EventMouseWheel:
		m := evt.MouseWheel
		return tea.MouseWheelMsg{X: m.X, Y: y, Button: m.Button}
	}
	return evt.Custom
}

// New creates a shell tree screen bound to model.
func New(th *theme.Theme, model *filetree.FileTreeModel) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:    ui.NewBase(ScreenID),
		th:      th,
		tokens:  tokens,
		model:   model,
		visible: true,
	}
	s.applyConfig()
	s.filter = &treeFilterField{
		Base:  ui.NewBase(FilterID),
		field: ui.NewFilterField(FilterID, tokens),
		owner: s,
	}
	s.filter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.filter.field.SetPromptPlacement(ui.PromptEnd)
	s.syncSearchPlaceholder()
	s.filter.field.OnChanged(func(value string) {
		if s.model == nil {
			return
		}
		if !s.model.FilterActive() {
			_ = s.model.EnterFilterMode()
		}
		s.model.SetFilter(value)
		s.syncFromModel()
	})
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	s.divider.SetForeground(tokens.TreeBorderFg())
	s.rebuildTree()
	return s
}

func (s *Screen) applyConfig() {
	s.cfg = filetree.PaintConfigFromTheme(s.th)
	if s.model != nil {
		s.icons = s.model.IconResolver()
		if s.icons != nil {
			s.icons.ApplyTheme(&s.th.Tree.Colors)
			s.icons.SetGlyphs(s.th.Glyphs)
		}
	}
}

func (s *Screen) rebuildTree() {
	tree := ui.NewTree(TreeID+"/list", s.tokens)
	tree.SetRowPainter(filetree.NewTreeRowPainter(s.model, s.icons, s.cfg))
	s.tree = newFileTreeView(s, tree)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, tree.Scroll(), s.tokens)
	s.scrollbar.OnScroll(func(_, _ int) {
		if s.model == nil || s.tree == nil || s.tree.tree == nil {
			return
		}
		filetree.ApplyScrollToModel(s.model, s.tree.tree.Scroll())
	})
}

// SetModel rebinds the file tree model.
func (s *Screen) SetModel(model *filetree.FileTreeModel) {
	s.model = model
	s.applyConfig()
	s.rebuildTree()
	s.syncSearchPlaceholder()
	s.syncFromModel()
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.tree != nil && s.tree.tree != nil {
		s.tree.tree.SetTokens(tokens)
		s.tree.tree.SetRowPainter(filetree.NewTreeRowPainter(s.model, s.icons, s.cfg))
	}
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	if s.filter != nil && s.filter.field != nil {
		s.filter.field.SetTokens(tokens)
		s.filter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.filter.field.SetPromptPlacement(ui.PromptEnd)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
		if tokens != nil {
			s.divider.SetForeground(tokens.TreeBorderFg())
		}
	}
}

// SetTheme replaces the active theme without rebuilding the component tree.
// Replacing children here used to leave zero-bound tree nodes when the host
// only invalidated paint (color-only preset switches), blanking the sidebar.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
	s.applyConfig()
	s.SetTokens(s.tokens)
}

func (s *Screen) SetWidth(w int)    { s.width = w }
func (s *Screen) SetVisible(v bool) { s.visible = v }
func (s *Screen) Visible() bool     { return s.visible }

func (s *Screen) TreeControl() *ui.Tree {
	if s.tree == nil {
		return nil
	}
	return s.tree.tree
}

func (s *Screen) Children() []ui.Component {
	kids := make([]ui.Component, 0, 4)
	if s.filter != nil {
		kids = append(kids, s.filter)
	}
	if s.tree != nil {
		kids = append(kids, s.tree)
	}
	if s.scrollbar != nil {
		kids = append(kids, s.scrollbar)
	}
	if s.divider != nil {
		kids = append(kids, s.divider)
	}
	return kids
}

// SetSplitMaxFirst caps sidebar drag at half the screen (viewmodel supplies
// the live terminal width).
func (s *Screen) SetSplitMaxFirst(n int) {
	if s.split != nil {
		s.split.SetMaxFirst(n)
	}
}

// SplitFirst returns the first-pane size after a divider drag, or 0 when the
// model is still following the laid-out slot width.
func (s *Screen) SplitFirst() int {
	if s.split == nil {
		return 0
	}
	return s.split.FirstSize()
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	if r.W > 1 {
		s.width = r.W - 1
	} else if r.W > 0 {
		s.width = r.W
	}
	if r.H > 0 {
		s.height = r.H
	}
	if s.model != nil {
		s.model.Update(filetree.TreeHeightMsg{Height: s.viewportHeight()})
	}
	s.layoutChildren(r)
	s.syncFromModel()
}

func (s *Screen) headerRows() int {
	rows := panelchrome.HeaderRows
	if s.breadcrumb != "" {
		rows++
	}
	return rows
}

func (s *Screen) viewportHeight() int {
	h := s.height
	if h < 1 {
		h = 1
	}
	if rows := s.headerRows(); rows > 0 && h > 0 {
		if rows >= h {
			return 1
		}
		h -= rows
	}
	return h
}

func (s *Screen) layoutChildren(r ui.Rect) {
	if s.tree == nil {
		return
	}
	treePanelW := r.W
	if r.W > 1 {
		treePanelW = r.W - 1
	}
	treeW := filetree.SidebarContentWidth(treePanelW)
	if treeW < 1 {
		treeW = 1
	}
	treeH := r.H
	if panelchrome.HeaderRows > 0 && treeH > 0 {
		if panelchrome.HeaderRows >= treeH {
			treeH = 1
		} else {
			treeH -= panelchrome.HeaderRows
		}
	}
	barW := 0
	if s.scrollbar != nil && s.scrollbar.NeededForViewport(treeH) {
		barW = min(ui.ScrollbarWidth, treePanelW)
	}
	treeX := panelchrome.PadLeft
	if treeX >= treeW {
		treeX = 0
	}
	s.tree.Layout(ui.LayoutContext{}, ui.Rect{
		X: treeX, Y: panelchrome.HeaderRows, W: maxInt(1, treeW-treeX), H: maxInt(1, treeH),
	})
	if s.filter != nil {
		s.filter.Layout(ui.LayoutContext{}, s.headerTitle().FieldRect(treeW))
	}
	if s.scrollbar != nil && treePanelW >= 1 {
		s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{
			X: treePanelW - barW,
			Y: panelchrome.HeaderRows,
			W: barW,
			H: maxInt(1, r.H-panelchrome.HeaderRows),
		})
	}
	if s.divider != nil && r.W > 0 {
		if s.split != nil && !s.split.Dragging() {
			s.split.SetFirstSize(s.width)
		}
		s.divider.SetCurrentFirst(s.width)
		s.divider.Layout(ui.LayoutContext{}, ui.Rect{X: r.W - 1, Y: 0, W: 1, H: r.H})
	}
}

func (s *Screen) syncFromModel() {
	if s.model == nil || s.tree == nil || s.tree.tree == nil {
		return
	}
	s.tree.tree.SetItems(filetree.TreeItems(s.model))
	cursor := s.model.CursorIndex()
	if cursor >= 0 {
		s.tree.tree.SelectIndex(cursor)
	}
	// Hover lives on the filetree model; ui.Tree.Handle never sees mouse
	// (fileTreeView routes to the model). Sync paint hover from the model.
	s.tree.tree.SyncHover(s.model.Hover.Index)
	if s.filter != nil && s.model != nil {
		s.filter.field.SetValue(s.model.Filter())
	}
	s.syncSearchPlaceholder()
	contentW := filetree.SidebarContentWidth(s.width)
	s.breadcrumb = filetree.BreadcrumbLineWithIcons(s.model, contentW, s.icons)
	s.tree.tree.SetBreadcrumb(s.breadcrumb)
	filetree.SyncScrollModel(s.model, s.tree.tree.Scroll(), s.viewportHeight())
	if s.scrollbar != nil {
		s.scrollbar.SetScroll(s.tree.tree.Scroll())
	}
}

func (s *Screen) syncSearchPlaceholder() {
	if s.filter == nil || s.filter.field == nil {
		return
	}
	label := ""
	if s.model != nil {
		label = s.model.WorkspaceLabel()
	}
	if label == "" {
		s.filter.field.SetPlaceholder("Search files")
		return
	}
	s.filter.field.SetPlaceholder("Search in " + label)
}

func (s *Screen) clearHover() {
	if s.model == nil {
		return
	}
	s.model.Hover.Clear()
	if s.tree != nil && s.tree.tree != nil {
		s.tree.tree.SyncHover(-1)
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil {
		return
	}
	bg := s.tokens.Color(ui.ColorBg)
	if !bg.IsDefault() {
		// FillRect adds origin — pass bounds in component-local coords.
		ui.FillRect(buf, ox, oy, b, cell.BlankCell(bg))
	}
	s.syncFromModel()
	innerW := b.W
	if innerW > 1 {
		innerW--
	}
	panelchrome.PaintTitle(buf, ox, oy, ui.Rect{X: b.X, Y: b.Y, W: panelchrome.BodyWidth(innerW), H: b.H}, s.tokens, s.headerTitle())
	// Children (tree, scrollbar, divider) are painted by Host.paintNode.
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.tree != nil {
		return s.tree.Handle(ui.ChildEventContext(ctx, s.tree.Bounds()), evt)
	}
	return ui.EventResult{}
}

func (s *Screen) Semantics() ui.Semantics {
	if s.tree != nil {
		return s.tree.Semantics()
	}
	return ui.Semantics{Role: ui.RoleTree, Label: "File tree"}
}

func (s *Screen) ContentWidth() int {
	return filetree.SidebarContentWidth(s.width)
}

// PathAtLocalY resolves a screen-local row to the visible file-tree path.
// The icon+search header and sticky breadcrumb rows are chrome and never
// resolve to a file. This is the canonical hit path for adaptive context menus.
func (s *Screen) PathAtLocalY(localY int) string {
	if s.model == nil {
		return ""
	}
	row := localY - panelchrome.HeaderRows
	if row < 0 {
		return ""
	}
	if s.breadcrumb != "" {
		if row == 0 {
			return ""
		}
		row--
	}
	idx := s.model.ScrollOffset() + row
	nodes := s.model.VisibleNodes()
	if idx < 0 || idx >= len(nodes) || nodes[idx] == nil {
		return ""
	}
	return nodes[idx].Path
}

// BodyTop is the first row owned by the tree control (before an optional
// sticky breadcrumb).
func (s *Screen) BodyTop() int { return panelchrome.HeaderRows }

func (s *Screen) headerTitle() panelchrome.Title {
	title := panelchrome.Title{Icon: s.explorerIcon()}
	if s.model != nil {
		for _, m := range filetree.HeaderMarks(s.model.Root, s.cfg, s.tokens) {
			title.BadgeParts = append(title.BadgeParts, panelchrome.BadgePart{Text: m.Text, FG: m.FG})
		}
		if s.model.Filter() != "" {
			title.BadgeParts = append(title.BadgeParts, panelchrome.BadgePart{
				Text: strconv.Itoa(len(s.model.VisibleNodes())),
			})
		}
	}
	return title
}

func (s *Screen) explorerIcon() string {
	if s.icons != nil && s.model != nil && s.model.Root != nil {
		icon := s.icons.ResolveIcon(s.model.Root.Name, true, false, true).Icon
		if icon != "" {
			return icon
		}
	}
	if panelchrome.NerdFont(s.th) {
		return "\uf07b"
	}
	return "▤"
}

func (s *Screen) PaintTestBuffer(w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	s.ResizeLayout(w, h)
	host := ui.NewHost(s.th, s)
	host.Resize(w, h)
	host.Layout()
	host.Paint(buf)
	return buf
}

func (s *Screen) ResizeLayout(w, h int) {
	s.width = w
	s.height = h
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
}

func PlainRow(buf *surface.CellBuffer, y, maxW int) string {
	if buf == nil {
		return ""
	}
	var b strings.Builder
	for x := 0; x < maxW && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func (f *treeFilterField) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return ui.Size{W: cs.MaxW, H: 1}
}

func (f *treeFilterField) Layout(_ ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	if f.field != nil {
		f.field.Layout(ui.LayoutContext{}, ui.Rect{W: r.W, H: r.H})
	}
}

func (f *treeFilterField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	if f.field == nil || f.owner == nil {
		return
	}
	tokens := f.owner.tokens
	f.field.SetBackgroundColor(tokens.Color(ui.ColorSubPaneBg))
	f.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}

func (f *treeFilterField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if f.owner == nil || f.owner.model == nil {
		return ui.EventResult{}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		_ = f.owner.model.EnterFilterMode()
		ctx.Focused = true
		res := f.field.Handle(ctx, evt)
		res.Handled = true
		res.Invalidate |= ui.InvalidateFocus | ui.InvalidatePaint
		return res
	}
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd, tea.KeyEnter:
			cmd := f.owner.model.Update(filetree.TreeKeyPressMsg{Key: evt.Key})
			f.owner.syncFromModel()
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		case tea.KeyEsc:
			if f.field.Value() != "" {
				f.owner.model.ClearFilter()
				f.field.SetValue("")
				f.owner.syncFromModel()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
			f.owner.model.ClearFilter()
			f.owner.syncFromModel()
			return ui.EventResult{
				Handled:    true,
				Action:     &ui.Action{ID: "ui.focus", Payload: TreeID},
				Invalidate: ui.InvalidateFocus | ui.InvalidatePaint,
			}
		case 'r', '.':
			if evt.Key.Mod.Contains(tea.ModCtrl) {
				cmd := f.owner.model.Update(filetree.TreeKeyPressMsg{Key: evt.Key})
				return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
			}
		}
	}
	ctx.Focused = true
	res := f.field.Handle(ctx, evt)
	f.owner.syncFromModel()
	return res
}

func (f *treeFilterField) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Filter files"}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
