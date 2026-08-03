package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TableColumnAlign selects cell text alignment within a column.
type TableColumnAlign uint8

const (
	TableAlignLeft TableColumnAlign = iota
	TableAlignCenter
	TableAlignRight
)

// TableColumn is one column definition for a Table. Width sizing reuses the
// Grid track rules: GridTrackFixed, GridTrackFraction, or GridTrackAuto
// (widest cell / header label).
type TableColumn struct {
	ID    string
	Label string
	Track GridTrack
	Align TableColumnAlign
}

// TableCell is one cell value.
type TableCell struct {
	Text string
	Role SemanticColor // default ColorFg when zero
	Attr cell.Attrs
}

// TableRow is one row of cells.
type TableRow struct {
	ID      string
	Cells   []TableCell
	Payload any
}

// Table is a columnar collection sharing Selection + Scroll models with List.
// Rows are uniform height (1); Page/Home/End and ensure-visible behavior come
// from the shared models. A sticky header row is painted above the scroll
// region when ShowHeader is enabled.
type Table struct {
	Base
	cols       []TableColumn
	rows       []TableRow
	sel        *SelectionModel
	scroll     *ScrollModel
	tokens     *TokenSet
	showHeader bool
	widths     []int
	colX       []int
	hoverIdx   int
	activate   string
}

// NewTable creates a table (clamp navigation, not wrap).
func NewTable(id ComponentID, tokens *TokenSet) *Table {
	return &Table{
		Base:     NewBase(id),
		sel:      NewSelectionModel(false),
		scroll:   NewScrollModel(),
		tokens:   tokens,
		hoverIdx: -1,
		activate: "table.activate",
	}
}

// SetTokens updates theme tokens.
func (t *Table) SetTokens(ts *TokenSet) { t.tokens = ts }

// SetColumns replaces the column definitions.
func (t *Table) SetColumns(cols []TableColumn) {
	t.cols = append(t.cols[:0], cols...)
	t.recomputeWidths()
}

// SetShowHeader toggles the sticky header row.
func (t *Table) SetShowHeader(show bool) { t.showHeader = show }

// SetRows replaces rows and selects the first row if needed.
func (t *Table) SetRows(rows []TableRow) {
	t.rows = append(t.rows[:0], rows...)
	t.sel.SetCount(len(rows))
	t.scroll.SetContent(len(rows))
	if len(rows) > 0 && t.sel.Index() < 0 {
		t.sel.SetIndex(0)
	}
	t.scroll.EnsureVisible(t.sel.Index())
	t.recomputeWidths()
}

// SelectIndex selects row idx.
func (t *Table) SelectIndex(idx int) bool {
	if !t.sel.SetIndex(idx) {
		return false
	}
	t.scroll.EnsureVisible(idx)
	return true
}

// Rows returns the rows.
func (t *Table) Rows() []TableRow { return t.rows }

// Columns returns the column definitions.
func (t *Table) Columns() []TableColumn { return t.cols }

// SelectedIndex returns the selection index.
func (t *Table) SelectedIndex() int { return t.sel.Index() }

// Selected returns the selected row and ok.
func (t *Table) Selected() (TableRow, bool) {
	i := t.sel.Index()
	if i < 0 || i >= len(t.rows) {
		return TableRow{}, false
	}
	return t.rows[i], true
}

// SetActivateAction sets the action ID emitted on activation.
func (t *Table) SetActivateAction(id string) { t.activate = id }

// Selection returns the shared selection model.
func (t *Table) Selection() *SelectionModel { return t.sel }

// Scroll returns the shared scroll model.
func (t *Table) Scroll() *ScrollModel { return t.scroll }

func (t *Table) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (t *Table) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	vp := r.H
	if t.showHeader && vp > 0 {
		vp--
	}
	t.scroll.SetViewport(max(1, vp))
	t.scroll.EnsureVisible(t.sel.Index())
	t.recomputeWidths()
}

// recomputeWidths resolves column widths from GridTrack rules and the bounds.
func (t *Table) recomputeWidths() {
	availW := t.Bounds().W
	if availW <= 0 {
		availW = 80
	}
	n := len(t.cols)
	widths := make([]int, n)
	colX := make([]int, n)
	consumed := 0
	totalFrac := 0
	fracWeight := make([]int, n)
	for i, c := range t.cols {
		switch c.Track.Kind {
		case GridTrackFixed:
			w := c.Track.Size
			if w < 1 {
				w = 1
			}
			widths[i] = w
			consumed += w
		case GridTrackFraction:
			w := max(1, c.Track.Size)
			fracWeight[i] = w
			totalFrac += w
		default: // GridTrackAuto: widest header label / cell
			w := 1
			if t.showHeader {
				w = max(w, rendering.VisibleWidth(c.Label))
			}
			for _, row := range t.rows {
				if i < len(row.Cells) {
					w = max(w, rendering.VisibleWidth(row.Cells[i].Text))
				}
			}
			widths[i] = w
			consumed += w
		}
	}
	if totalFrac > 0 {
		remain := availW - consumed
		if remain < 0 {
			remain = 0
		}
		assigned := 0
		lastFrac := -1
		for i := range t.cols {
			if fracWeight[i] == 0 {
				continue
			}
			lastFrac = i
			w := remain * fracWeight[i] / totalFrac
			widths[i] = w
			assigned += w
		}
		if lastFrac >= 0 && remain-assigned > 0 {
			widths[lastFrac] += remain - assigned
		}
	}
	for i := range widths {
		if widths[i] < 1 {
			widths[i] = 1
		}
	}
	x := 0
	for i := range widths {
		colX[i] = x
		x += widths[i]
	}
	t.widths = widths
	t.colX = colX
}

func (t *Table) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() || len(t.rows) == 0 {
		return
	}
	bg := t.color(ColorPopupBg)
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))

	y := originY + r.Y
	contentY := 0
	if t.showHeader {
		t.paintHeader(buf, originX+r.X, y, bg)
		contentY = 1
	}

	off := t.scroll.Offset()
	vh := t.scroll.Viewport()
	for row := 0; row < vh; row++ {
		idx := off + row
		if idx >= len(t.rows) {
			break
		}
		rowY := originY + r.Y + contentY + row
		st := StateDefault
		if idx == t.sel.Index() {
			if ctx.Focused {
				st = StateFocused
			} else {
				st = StateSelected
			}
		} else if idx == t.hoverIdx {
			st = StateHovered
		}
		fg, rowBG := cell.NoColor, cell.NoColor
		if t.tokens != nil {
			fg, rowBG = t.tokens.ListRowColors(st)
		}
		if !rowBG.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: originX + r.X, Y: rowY, W: r.W, H: 1}, cell.BlankCell(rowBG))
		}
		attr := cell.Attrs(0)
		if idx == t.sel.Index() {
			attr = cell.AttrBold
		}
		t.paintRowCells(buf, originX+r.X, rowY, fg, rowBG, attr, t.rows[idx])
	}
}

func (t *Table) paintHeader(buf *surface.CellBuffer, x, y int, bg cell.Color) {
	headBG := t.color(ColorPopupSubtleBg)
	headFG := t.color(ColorMuted)
	FillRect(buf, 0, 0, Rect{X: x, Y: y, W: t.Bounds().W, H: 1}, cell.BlankCell(headBG))
	for c := range t.cols {
		col := t.cols[c]
		w := t.widths[c]
		if w < 1 {
			continue
		}
		text := rendering.TruncateLine(col.Label, w)
		t.paintCell(buf, x+t.colX[c], y, w, text, col.Align, headFG, headBG, cell.AttrBold)
	}
}

func (t *Table) paintRowCells(buf *surface.CellBuffer, x, y int, fg, bg cell.Color, attr cell.Attrs, row TableRow) {
	for c := range t.cols {
		w := t.widths[c]
		if w < 1 {
			continue
		}
		var text string
		role := ColorFg
		cattr := attr
		if c < len(row.Cells) {
			text = row.Cells[c].Text
			if row.Cells[c].Role != ColorFg && t.tokens != nil {
				role = row.Cells[c].Role
			}
			cattr = attr | row.Cells[c].Attr
		}
		cf := fg
		if role != ColorFg && t.tokens != nil {
			cf = t.tokens.Color(role)
		}
		t.paintCell(buf, x+t.colX[c], y, w, text, t.cols[c].Align, cf, bg, cattr)
	}
}

func (t *Table) paintCell(buf *surface.CellBuffer, x, y, width int, text string, align TableColumnAlign, fg, bg cell.Color, attr cell.Attrs) {
	text = rendering.TruncateLine(text, width)
	tw := rendering.VisibleWidth(text)
	if tw > width {
		tw = width
	}
	var cx int
	switch align {
	case TableAlignRight:
		cx = width - tw
	case TableAlignCenter:
		cx = (width - tw) / 2
	}
	if cx < 0 {
		cx = 0
	}
	buf.WriteString(x+cx, y, text, fg, bg, attr)
}

func (t *Table) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventKey:
		if len(t.rows) == 0 {
			return EventResult{}
		}
		switch evt.Key.Code {
		case tea.KeyUp:
			t.sel.MoveUp()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyDown:
			t.sel.MoveDown()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgUp:
			t.sel.PageUp(t.scroll.Viewport())
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgDown:
			t.sel.PageDown(t.scroll.Viewport())
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyHome:
			t.sel.Home()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnd:
			t.sel.End()
			t.scroll.EnsureVisible(t.sel.Index())
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnter:
			row, ok := t.Selected()
			if !ok {
				return EventResult{Handled: true}
			}
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: t.activate, Payload: row},
				Invalidate: InvalidatePaint,
			}
		}
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
		t.scroll.EnsureVisible(idx)
		row := t.rows[idx]
		return EventResult{
			Handled:    true,
			Action:     &Action{ID: t.activate, Payload: row},
			Invalidate: InvalidatePaint | InvalidateFocus,
		}
	case EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			t.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			t.scroll.ScrollBy(3)
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	}
	return EventResult{}
}

func (t *Table) rowAt(localY int) int {
	if t.showHeader {
		if localY <= 0 {
			return -1
		}
		localY--
	}
	if localY < 0 {
		return -1
	}
	idx := t.scroll.Offset() + localY
	if idx < 0 || idx >= len(t.rows) {
		return -1
	}
	return idx
}

func (t *Table) color(role SemanticColor) cell.Color {
	if t.tokens == nil {
		return cell.NoColor
	}
	return t.tokens.Color(role)
}

func (t *Table) Semantics() Semantics {
	row, _ := t.Selected()
	return Semantics{Role: RoleList, Label: string(t.ID()), Value: row.ID, Selected: t.sel.Index() >= 0}
}
