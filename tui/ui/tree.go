package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TreeItem is one hierarchical row for the Tree control (payload-based painter path).
type TreeItem struct {
	ID      string
	Label   string
	Depth   int
	Payload any
}

// TreeRow is a fully resolved presentation row for default shell painting.
type TreeRow struct {
	ID           string
	Label        string
	Depth        int
	IsDir        bool
	Expanded     bool
	Loading      bool
	Icon         string
	IconFG       cell.Color
	GitGlyph     string
	GitFG        cell.Color
	DiagErrors   int
	DiagWarnings int
	DiagHints    int
	Selected     bool
	Cursor       bool
	Hovered      bool
}

// TreeRowState is the interactive state for a painted tree row.
type TreeRowState struct {
	Cursor   bool
	Hovered  bool
	Selected bool
}

// TreeRowPainter paints one tree row into a cell buffer.
type TreeRowPainter func(buf *surface.CellBuffer, ox, oy, rowY, width int, item TreeItem, st TreeRowState, tokens *TokenSet)

// Tree is a scrollable hierarchical list with optional custom row painting.
type Tree struct {
	Base
	items            []TreeItem
	rows             []TreeRow
	sel              *SelectionModel
	scroll           *ScrollModel
	tokens           *TokenSet
	hoverIdx         int
	painter          TreeRowPainter
	breadcrumb       string
	activate         string
	toggle           string
	useRows          bool
	selectionCleared bool
	surfaceRole      SemanticColor
}

// NewTree creates a tree control.
func NewTree(id ComponentID, tokens *TokenSet) *Tree {
	return &Tree{
		Base:        NewBase(id),
		sel:         NewSelectionModel(false),
		scroll:      NewScrollModel(),
		tokens:      tokens,
		hoverIdx:    -1,
		activate:    "tree.activate",
		toggle:      "tree.toggle",
		surfaceRole: ColorBg,
	}
}

func (t *Tree) SetTokens(ts *TokenSet) { t.tokens = ts }

// SetSurfaceBackground selects the background painted behind rows. Shell trees
// keep ColorBg; popup trees use ColorPopupBg so empty rows and reserved gutter
// cells match the WindowFrame body.
func (t *Tree) SetSurfaceBackground(role SemanticColor) { t.surfaceRole = role }

// SetRowPainter installs a custom row painter (file-tree icons/git/diags).
func (t *Tree) SetRowPainter(p TreeRowPainter) { t.painter = p }

// SetItems replaces visible items (painter path).
func (t *Tree) SetItems(items []TreeItem) {
	t.useRows = false
	t.rows = nil
	t.items = append(t.items[:0], items...)
	t.sel.SetCount(len(items))
	if t.selectionCleared {
		t.sel.ClearSelection()
	}
	t.scroll.SetContent(len(items))
	if len(items) > 0 && t.sel.Index() < 0 && !t.selectionCleared {
		t.sel.SetIndex(0)
	}
	t.scroll.EnsureVisible(t.sel.Index())
}

// SetRows replaces presentation rows (default paint path used by shelltree).
func (t *Tree) SetRows(rows []TreeRow) {
	t.useRows = true
	t.items = nil
	t.rows = append(t.rows[:0], rows...)
	t.sel.SetCount(len(rows))
	t.scroll.SetContent(len(rows))
	cursor := -1
	for i, r := range rows {
		if r.Cursor {
			cursor = i
			break
		}
	}
	if cursor >= 0 {
		t.selectionCleared = false
		t.sel.SetIndex(cursor)
	} else if t.selectionCleared {
		t.sel.ClearSelection()
	} else if len(rows) > 0 && t.sel.Index() < 0 {
		t.sel.SetIndex(0)
	}
	t.scroll.EnsureVisible(t.sel.Index())
}

// SelectIndex selects a row by index.
func (t *Tree) SelectIndex(idx int) {
	if t.sel.SetIndex(idx) {
		t.selectionCleared = false
	}
	t.scroll.EnsureVisible(idx)
}

// ClearSelection removes the row cursor without changing rows or scroll.
func (t *Tree) ClearSelection() {
	t.selectionCleared = true
	t.sel.ClearSelection()
}

// SyncHover sets the hovered row index for paint ( -1 clears ).
// Call this when hover is owned by an external model (filetree) rather than Tree.Handle.
func (t *Tree) SyncHover(idx int) {
	if idx < -1 {
		idx = -1
	}
	if n := t.count(); idx >= n {
		idx = -1
	}
	t.hoverIdx = idx
}

// HoverIndex returns the hovered row index (-1 if none).
func (t *Tree) HoverIndex() int { return t.hoverIdx }

// SetBreadcrumb sets the sticky header line (empty to hide).
func (t *Tree) SetBreadcrumb(s string) { t.breadcrumb = s }

// Breadcrumb returns the sticky header text.
func (t *Tree) Breadcrumb() string { return t.breadcrumb }

func (t *Tree) Scroll() *ScrollModel       { return t.scroll }
func (t *Tree) Selection() *SelectionModel { return t.sel }
func (t *Tree) Items() []TreeItem          { return t.items }

func (t *Tree) ActiveID() string {
	i := t.sel.Index()
	if t.useRows {
		if i < 0 || i >= len(t.rows) {
			return ""
		}
		return t.rows[i].ID
	}
	if i < 0 || i >= len(t.items) {
		return ""
	}
	return t.items[i].ID
}

func (t *Tree) count() int {
	if t.useRows {
		return len(t.rows)
	}
	return len(t.items)
}

func (t *Tree) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (t *Tree) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	vp := r.H
	if t.breadcrumb != "" && vp > 0 {
		vp--
	}
	t.scroll.SetViewport(max(1, vp))
	t.scroll.EnsureVisible(t.sel.Index())
}

func (t *Tree) Paint(buf *surface.CellBuffer, ox, oy int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() || buf == nil {
		return
	}
	bg := t.color(t.surfaceRole)
	FillRect(buf, ox, oy, r, cell.BlankCell(bg))

	y0 := oy + r.Y
	contentY := 0
	if t.breadcrumb != "" {
		muted := t.color(ColorMuted)
		text := rendering.TruncateLine(t.breadcrumb, r.W)
		buf.WriteString(ox+r.X, y0, text, muted, bg, 0)
		contentY = 1
	}

	start := t.scroll.Offset()
	visH := r.H - contentY
	n := t.count()
	end := min(n, start+visH)
	for i := start; i < end; i++ {
		rowY := contentY + (i - start)
		cursor := ctx.Focused && (i == t.sel.Index() || (t.useRows && t.rows[i].Cursor))
		hovered := i == t.hoverIdx || (t.useRows && t.rows[i].Hovered)
		if t.useRows {
			t.paintTreeRow(buf, ox+r.X, oy+r.Y+rowY, r.W, t.rows[i], cursor, hovered)
			continue
		}
		item := t.items[i]
		st := TreeRowState{
			Cursor:  cursor,
			Hovered: hovered,
		}
		if t.painter != nil {
			t.painter(buf, ox+r.X, oy+r.Y, rowY, r.W, item, st, t.tokens)
			continue
		}
		t.paintDefaultRow(buf, ox+r.X, oy+r.Y+rowY, r.W, item, st)
	}
}

func (t *Tree) paintTreeRow(buf *surface.CellBuffer, x, y, width int, row TreeRow, cursor, hovered bool) {
	bg := t.color(t.surfaceRole)
	fg := t.color(ColorFg)
	muted := t.color(ColorMuted)
	attr := cell.Attrs(0)
	if cursor || row.Selected {
		if t.tokens != nil {
			bg = t.tokens.TreeCursorBg()
			fg = t.tokens.TreeCursorFg()
		} else {
			bg = t.color(ColorSelection)
		}
		attr = cell.AttrBold
	} else if hovered {
		if t.tokens != nil {
			bg = t.tokens.TreeHoverBg()
		} else {
			bg = t.color(ColorHover)
		}
	}
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	cx, remain := x, width
	indent := row.Depth * 2
	if indent > remain {
		indent = remain
	}
	if indent > 0 {
		buf.WriteString(cx, y, stringsRepeat(" ", indent), muted, bg, 0)
		cx += indent
		remain -= indent
	}
	if row.IsDir && remain > 2 {
		g := DefaultGlyphsFromTokens(t.tokens)
		chev := string(FirstRune(g.TreeCollapsed, '▸')) + " "
		if row.Expanded {
			chev = string(FirstRune(g.TreeExpanded, '▾')) + " "
		}
		if row.Loading {
			chev = "… "
		}
		buf.WriteString(cx, y, chev, muted, bg, 0)
		cw := rendering.VisibleWidth(chev)
		cx += cw
		remain -= cw
	}
	if row.Icon != "" && remain > 2 {
		iconFG := row.IconFG
		if iconFG.IsDefault() {
			iconFG = muted
		}
		icon := row.Icon + " "
		buf.WriteString(cx, y, rendering.TruncateLine(icon, remain), iconFG, bg, 0)
		iw := rendering.VisibleWidth(icon)
		cx += iw
		remain -= iw
	}
	diagW := 0
	if row.DiagErrors > 0 {
		diagW += rendering.VisibleWidth("✕"+itoaSimple(row.DiagErrors)) + 1
	}
	if row.DiagWarnings > 0 {
		diagW += rendering.VisibleWidth("⚠"+itoaSimple(row.DiagWarnings)) + 1
	}
	gitW := 0
	if row.GitGlyph != "" {
		gitW = rendering.VisibleWidth(row.GitGlyph) + 1
	}
	labelBudget := remain - diagW - gitW
	if labelBudget < 1 {
		labelBudget = 1
	}
	label := rendering.TruncateVisual(row.Label, labelBudget, "…")
	buf.WriteString(cx, y, label, fg, bg, attr)
	cx += rendering.VisibleWidth(label)
	remain = x + width - cx
	if row.GitGlyph != "" && remain > 0 {
		gfg := row.GitFG
		if gfg.IsDefault() {
			gfg = muted
		}
		buf.WriteString(cx, y, " "+row.GitGlyph, gfg, bg, 0)
		cx += 1 + rendering.VisibleWidth(row.GitGlyph)
		remain = x + width - cx
	}
	if row.DiagErrors > 0 && remain > 0 {
		s := "✕" + itoaSimple(row.DiagErrors)
		errC := t.color(ColorError)
		if t.tokens != nil {
			errC = t.tokens.TreeDiagError()
		}
		buf.WriteString(cx, y, " "+s, errC, bg, 0)
		cx += 1 + rendering.VisibleWidth(s)
		remain = x + width - cx
	}
	if row.DiagWarnings > 0 && remain > 0 {
		s := "⚠" + itoaSimple(row.DiagWarnings)
		warnC := t.color(ColorWarning)
		if t.tokens != nil {
			warnC = t.tokens.TreeDiagWarning()
		}
		buf.WriteString(cx, y, " "+s, warnC, bg, 0)
	}
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (t *Tree) paintDefaultRow(buf *surface.CellBuffer, x, y, width int, item TreeItem, st TreeRowState) {
	bg := t.color(t.surfaceRole)
	fg := t.color(ColorFg)
	muted := t.color(ColorMuted)
	attr := cell.Attrs(0)
	if st.Cursor {
		bg = t.color(ColorSelection)
		attr = cell.AttrBold
	} else if st.Hovered {
		bg = t.color(ColorHover)
	}
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	indent := item.Depth * 2
	if indent > width {
		indent = width
	}
	if indent > 0 {
		buf.WriteString(x, y, stringsRepeat(" ", indent), muted, bg, 0)
	}
	label := rendering.TruncateVisual(item.Label, max(1, width-indent), "…")
	buf.WriteString(x+indent, y, label, fg, bg, attr)
}

func (t *Tree) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventKey:
		if t.count() == 0 {
			return EventResult{}
		}
		switch {
		case evt.Key.Code == tea.KeyUp || evt.Key.String() == "k":
			t.selectionCleared = false
			t.sel.MoveUp()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Action: &Action{ID: "tree.select", Payload: t.ActiveID()}, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyDown || evt.Key.String() == "j":
			t.selectionCleared = false
			t.sel.MoveDown()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Action: &Action{ID: "tree.select", Payload: t.ActiveID()}, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyEnter:
			return EventResult{Handled: true, Action: &Action{ID: t.activate, Payload: t.ActiveID()}, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyPgUp:
			t.selectionCleared = false
			t.sel.PageUp(t.scroll.Viewport())
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyPgDown:
			t.selectionCleared = false
			t.sel.PageDown(t.scroll.Viewport())
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyHome:
			t.selectionCleared = false
			t.sel.Home()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case evt.Key.Code == tea.KeyEnd:
			t.selectionCleared = false
			t.sel.End()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
	case EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			t.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			t.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case EventMouseMotion:
		idx := t.rowAt(ctx.LocalY)
		if idx != t.hoverIdx {
			t.hoverIdx = idx
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return EventResult{}
		}
		idx := t.rowAt(ctx.LocalY)
		if idx < 0 {
			return EventResult{Handled: true}
		}
		t.sel.SetIndex(idx)
		t.selectionCleared = false
		t.scroll.EnsureVisible(idx)
		return EventResult{Handled: true, Action: &Action{ID: t.activate, Payload: t.ActiveID()}, Invalidate: InvalidateFocus | InvalidatePaint}
	}
	return EventResult{}
}

func (t *Tree) rowAt(localY int) int {
	if t.breadcrumb != "" {
		if localY <= 0 {
			return -1
		}
		localY--
	}
	if localY < 0 {
		return -1
	}
	idx := t.scroll.Offset() + localY
	if idx < 0 || idx >= t.count() {
		return -1
	}
	return idx
}

func (t *Tree) color(role SemanticColor) cell.Color {
	if t.tokens == nil {
		return cell.NoColor
	}
	return t.tokens.Color(role)
}

func (t *Tree) Semantics() Semantics {
	return Semantics{Role: RoleTree, Label: "File tree", Value: t.ActiveID(), Selected: t.sel.Index() >= 0}
}

func stringsRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
