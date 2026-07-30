package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// VirtItem is a variable-height list item.
type VirtItem struct {
	ID      string
	Height  int // rows; min 1
	Label   string
	Detail  string
	Icon    string
	Payload any
}

// RowPainter paints one visible screen row of an item.
// localRow is 0-based within the item (0 = first row of the item).
// Multi-height items receive one call per visible continuation row.
type RowPainter func(buf *surface.CellBuffer, x, y, width int, item VirtItem, localRow int, state ControlState, ctx PaintContext, tokens *TokenSet)

// VirtualList is a variable-height selectable list with shared Selection + Scroll.
// Height is measured in rows; scroll offset is in rows (not item indices).
type VirtualList struct {
	Base
	items                  []VirtItem
	sel                    *SelectionModel
	scroll                 *ScrollModel // content = total rows
	tokens                 *TokenSet
	activate               string
	hoverIdx               int
	rowPainter             RowPainter
	bottomPadding          int // trailing blank rows (chat gravity)
	pointerPolicy          PointerPolicy
	selectionAnchorsScroll bool // when true, selection changes reveal rows (launcher/FIF)
	// prefix[i] = starting row of item i; prefix[len] = total rows
	prefix []int
}

// NewVirtualList creates a variable-height list.
func NewVirtualList(id ComponentID, tokens *TokenSet) *VirtualList {
	return &VirtualList{
		Base:                   NewBase(id),
		sel:                    NewSelectionModel(false),
		scroll:                 NewScrollModel(),
		tokens:                 tokens,
		activate:               "virtlist.activate",
		hoverIdx:               -1,
		selectionAnchorsScroll: true,
	}
}

// SetTokens updates theme tokens.
func (v *VirtualList) SetTokens(ts *TokenSet) { v.tokens = ts }

// SetItems replaces items and rebuilds the height index.
// When stick-to-bottom is armed, the viewport stays pinned after the rebuild.
func (v *VirtualList) SetItems(items []VirtItem) {
	v.items = items
	v.rebuildPrefix()
	v.sel.SetCount(len(items))
	if len(items) > 0 && v.sel.Index() < 0 {
		v.sel.SetIndex(0)
	}
	v.applyScrollAfterContentChange()
}

// Items returns items.
func (v *VirtualList) Items() []VirtItem { return v.items }

// SetActivateAction sets the action ID emitted on activation.
func (v *VirtualList) SetActivateAction(id string) { v.activate = id }

// SetRowPainter replaces the default item label painting with a custom delegate.
// Hit testing, selection, scrolling, and empty-state handling remain shared.
// The painter is invoked once per visible row of each item (including continuation rows).
func (v *VirtualList) SetRowPainter(fn RowPainter) { v.rowPainter = fn }

// SetPointerPolicy configures Host pointer routing for paint-only lists whose
// parent owns text selection, wheel policy, or other pointer semantics.
func (v *VirtualList) SetPointerPolicy(p PointerPolicy) { v.pointerPolicy = p }

// SetSelectionAnchorsScroll controls whether selection changes and content/layout
// updates scroll to keep the selected item visible. Default is true (launcher,
// find-in-files). Paint-only consumers such as shellchat set false because the
// parent owns scroll/selection semantics.
func (v *VirtualList) SetSelectionAnchorsScroll(anchor bool) {
	v.selectionAnchorsScroll = anchor
	if anchor {
		v.ensureSelectedVisible()
	}
}

// SelectionAnchorsScroll reports whether selection anchors scroll position.
func (v *VirtualList) SelectionAnchorsScroll() bool { return v.selectionAnchorsScroll }

// PointerPolicy implements PointerPolicyProvider.
func (v *VirtualList) PointerPolicy() PointerPolicy { return v.pointerPolicy }

// SetBottomPadding adds trailing blank content rows (message-list gravity).
func (v *VirtualList) SetBottomPadding(n int) {
	if n < 0 {
		n = 0
	}
	v.bottomPadding = n
	v.rebuildPrefix()
}

// BottomPadding returns trailing blank rows.
func (v *VirtualList) BottomPadding() int { return v.bottomPadding }

// StickToBottom scrolls to the end and arms auto-scroll.
func (v *VirtualList) StickToBottom() { v.scroll.StickToBottom() }

// ApplyAutoScroll pins to bottom when auto-scroll is armed / near bottom.
func (v *VirtualList) ApplyAutoScroll() { v.scroll.ApplyAutoScroll() }

// SelectIndex selects idx and ensures its rows are visible.
func (v *VirtualList) SelectIndex(idx int) bool {
	if !v.sel.SetIndex(idx) {
		return false
	}
	if v.selectionAnchorsScroll {
		v.ensureSelectedVisible()
	}
	return true
}

// SelectedIndex returns the selection.
func (v *VirtualList) SelectedIndex() int { return v.sel.Index() }

// Selected returns the selected item.
func (v *VirtualList) Selected() (VirtItem, bool) {
	i := v.sel.Index()
	if i < 0 || i >= len(v.items) {
		return VirtItem{}, false
	}
	return v.items[i], true
}

// Selection returns the selection model.
func (v *VirtualList) Selection() *SelectionModel { return v.sel }

// Scroll returns the scroll model (row-based).
func (v *VirtualList) Scroll() *ScrollModel { return v.scroll }

func (v *VirtualList) rebuildPrefix() {
	v.prefix = make([]int, len(v.items)+1)
	total := 0
	for i, it := range v.items {
		v.prefix[i] = total
		h := it.Height
		if h < 1 {
			h = 1
		}
		total += h
	}
	v.prefix[len(v.items)] = total
	v.scroll.SetContent(total + v.bottomPadding)
}

// ItemIndexAtRow returns the item index containing absRow, or -1 for padding/empty.
func (v *VirtualList) ItemIndexAtRow(absRow int) int {
	return v.itemAtRow(absRow)
}

func (v *VirtualList) itemAtRow(row int) int {
	// binary search prefix; rows past last item (bottom padding) return -1
	lo, hi := 0, len(v.items)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		start := v.prefix[mid]
		end := v.prefix[mid+1]
		if row < start {
			hi = mid - 1
		} else if row >= end {
			lo = mid + 1
		} else {
			return mid
		}
	}
	return -1
}

func (v *VirtualList) ensureSelectedVisible() {
	i := v.sel.Index()
	if i < 0 || i >= len(v.items) {
		return
	}
	start := v.prefix[i]
	end := v.prefix[i+1]
	off := v.scroll.Offset()
	vh := v.scroll.Viewport()
	if start < off {
		v.scroll.ScrollTo(start)
	} else if end > off+vh {
		v.scroll.ScrollTo(end - vh)
	}
}

func (v *VirtualList) applyScrollAfterContentChange() {
	if v.scroll.AutoScroll() {
		v.scroll.StickToBottom()
		return
	}
	if v.selectionAnchorsScroll {
		v.ensureSelectedVisible()
		return
	}
	v.scroll.Clamp()
}

func (v *VirtualList) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: cs.MaxH})
}

func (v *VirtualList) Layout(_ LayoutContext, r Rect) {
	v.SetBounds(r)
	v.scroll.SetViewport(r.H)
	if v.selectionAnchorsScroll {
		v.ensureSelectedVisible()
	} else if v.scroll.AutoScroll() {
		v.scroll.StickToBottom()
	} else {
		v.scroll.Clamp()
	}
}

func (v *VirtualList) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := v.Bounds()
	if r.Empty() || len(v.items) == 0 {
		return
	}
	off := v.scroll.Offset()
	vh := v.scroll.Viewport()
	for row := 0; row < vh; row++ {
		absRow := off + row
		idx := v.itemAtRow(absRow)
		y := originY + r.Y + row
		x := originX + r.X
		if idx < 0 {
			continue
		}
		itemStart := v.prefix[idx]
		localRow := absRow - itemStart
		item := v.items[idx]
		st := StateDefault
		if idx == v.sel.Index() {
			if ctx.Focused {
				st = StateFocused
			} else {
				st = StateSelected
			}
		} else if idx == v.hoverIdx {
			st = StateHovered
		}
		fg, bg := cell.NoColor, cell.NoColor
		if v.tokens != nil {
			fg, bg = v.tokens.ListRowColors(st)
		}
		if !bg.IsDefault() {
			FillRect(buf, 0, 0, Rect{X: x, Y: y, W: r.W, H: 1}, cell.BlankCell(bg))
		}
		if v.rowPainter != nil {
			v.rowPainter(buf, x, y, r.W, item, localRow, st, ctx, v.tokens)
			continue
		}
		// Default paint: label on first row only; continuation rows keep selection bg.
		if localRow != 0 {
			continue
		}
		marker := "  "
		if idx == v.sel.Index() {
			marker = "❯ "
		}
		line := marker
		if item.Icon != "" {
			line += item.Icon + " "
		}
		line += item.Label
		if item.Detail != "" {
			gap := r.W - rendering.VisibleWidth(line) - rendering.VisibleWidth(item.Detail) - 1
			if gap < 1 {
				gap = 1
			}
			line += strings.Repeat(" ", gap) + item.Detail
		}
		line = rendering.TruncateLine(line, r.W)
		attr := cell.Attrs(0)
		if idx == v.sel.Index() {
			attr = cell.AttrBold
		}
		buf.WriteString(x, y, line, fg, bg, attr)
	}
}

func (v *VirtualList) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.Code {
		case tea.KeyUp:
			v.sel.MoveUp()
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyDown:
			v.sel.MoveDown()
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgUp:
			v.sel.PageUp(max(1, v.scroll.Viewport()/2))
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyPgDown:
			v.sel.PageDown(max(1, v.scroll.Viewport()/2))
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyHome:
			v.sel.Home()
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnd:
			v.sel.End()
			v.scroll.StickToBottom()
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyEnter:
			item, ok := v.Selected()
			if !ok {
				return EventResult{Handled: true}
			}
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: v.activate, Payload: item},
				Invalidate: InvalidatePaint,
			}
		}
	case tea.MouseWheelMsg:
		if m.Button == tea.MouseWheelUp {
			v.scroll.ScrollBy(-3)
			v.scroll.SetAutoScroll(false)
		} else if m.Button == tea.MouseWheelDown {
			v.scroll.ScrollBy(3)
			_ = v.scroll.ShouldAutoScroll()
		}
		return EventResult{Handled: true, Invalidate: InvalidatePaint}
	case tea.MouseClickMsg:
		if m.Button != tea.MouseLeft {
			return EventResult{}
		}
		absRow := v.scroll.Offset() + ctx.LocalY
		idx := v.itemAtRow(absRow)
		if idx >= 0 {
			v.sel.SetIndex(idx)
			v.scroll.SetAutoScroll(false)
			if v.selectionAnchorsScroll {
				v.ensureSelectedVisible()
			}
			item := v.items[idx]
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: v.activate, Payload: item},
				Invalidate: InvalidatePaint | InvalidateFocus,
			}
		}
	case tea.MouseMotionMsg:
		absRow := v.scroll.Offset() + ctx.LocalY
		idx := v.itemAtRow(absRow)
		if idx != v.hoverIdx {
			v.hoverIdx = idx
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	}
	return EventResult{}
}

func (v *VirtualList) Semantics() Semantics {
	item, _ := v.Selected()
	return Semantics{Role: RoleList, Label: string(v.ID()), Value: item.Label}
}
