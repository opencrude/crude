// Package shelltree provides the native file-tree sidebar shell surface.
package shelltree

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "shelltree"
	FilterID    ui.ComponentID = "shelltree/filter"
	TreeID      ui.ComponentID = "shelltree/tree"
	ScrollbarID ui.ComponentID = "shelltree/scrollbar"
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
		cmd := v.owner.model.Update(filetree.TreeKeyPressMsg{Key: evt.Key})
		v.owner.syncFromModel()
		return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
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
	s.filter.field.SetPrompt("/ ")
	s.filter.field.SetPlaceholder("Filter files")
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
}

// SetModel rebinds the file tree model.
func (s *Screen) SetModel(model *filetree.FileTreeModel) {
	s.model = model
	s.applyConfig()
	s.rebuildTree()
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
	kids := make([]ui.Component, 0, 3)
	if s.filterRowActive() && s.filter != nil {
		kids = append(kids, s.filter)
	}
	if s.tree != nil {
		kids = append(kids, s.tree)
	}
	if s.scrollbar != nil {
		kids = append(kids, s.scrollbar)
	}
	return kids
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

func (s *Screen) filterRowActive() bool {
	return s.model != nil && s.model.FilterActive()
}

func (s *Screen) headerRows() int {
	rows := 0
	if s.filterRowActive() {
		rows++
	}
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
	if rows := s.headerRows(); rows > 0 && treeH > 0 {
		if rows >= treeH {
			treeH = 1
		} else {
			treeH -= rows
		}
	}
	treeY := 0
	if s.filterRowActive() {
		treeY = 1
	}
	s.tree.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: treeY, W: treeW, H: maxInt(1, treeH)})
	if s.filterRowActive() && s.filter != nil {
		s.filter.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: treeW, H: 1})
	}
	if s.scrollbar != nil && treePanelW >= 1 {
		s.scrollbar.Layout(ui.LayoutContext{}, ui.Rect{X: treePanelW - 1, Y: 0, W: 1, H: r.H})
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
	contentW := filetree.SidebarContentWidth(s.width)
	s.breadcrumb = filetree.BreadcrumbLine(s.model, contentW)
	s.tree.tree.SetBreadcrumb(s.breadcrumb)
	filetree.SyncScrollModel(s.model, s.tree.tree.Scroll(), s.viewportHeight())
	if s.scrollbar != nil {
		s.scrollbar.SetScroll(s.tree.tree.Scroll())
	}
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
	// Children (tree + scrollbar) are painted by Host.paintNode; only the
	// divider chrome is Screen-owned so it stays above the scrollbar.
	if b.W > 1 {
		divX := ox + b.X + b.W - 1
		divFG := s.tokens.TreeBorderFg()
		divRune := ui.FirstRune(s.tokens.Glyphs().BorderV, '│')
		for y := 0; y < b.H; y++ {
			buf.Set(divX, oy+b.Y+y, cell.NewCell(divRune, divFG, bg, 0))
		}
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	treePanelW := s.width
	if ctx.LocalX >= filetree.SidebarContentWidth(treePanelW) {
		if s.scrollbar != nil && s.tree != nil && s.tree.tree != nil {
			res := s.scrollbar.Handle(ctx, evt)
			if res.Handled {
				filetree.ApplyScrollToModel(s.model, s.tree.tree.Scroll())
				s.syncFromModel()
			}
			return res
		}
	}
	if s.tree != nil {
		return s.tree.Handle(ctx, evt)
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

func (f *treeFilterField) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if f.owner == nil || f.owner.model == nil {
		return ui.EventResult{}
	}
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	cmd := f.owner.model.Update(filetree.TreeKeyPressMsg{Key: evt.Key})
	f.owner.syncFromModel()
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
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
