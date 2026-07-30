package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TabsChrome selects popup vs persistent shell styling.
type TabsChrome uint8

const (
	TabsPopup TabsChrome = iota
	TabsShell
)

// Tab sizing policy (shell + popup):
//   - Content-sized: width = leading pad + optional marker + label + close + trailing pad.
//   - Long labels ellipsis-truncate at tabMaxLabelW (28).
//   - Overflow: shrink all tabs proportionally down to tabMinW (8).
//   - If still too narrow: horizontally scroll so the active tab stays visible.
const (
	tabMaxLabelW = 28
	tabMinW      = 8
	tabLeadPad   = 1 // leading space
	tabGap       = 1 // column between tabs
)

// TabItem is one entry in a Tabs control.
type TabItem struct {
	ID        string
	Label     string
	Icon      string
	Closable  bool
	Attention bool
}

type tabRange struct {
	start, end, closeStart int
}

// Tabs is a horizontal retained tab strip with keyboard and mouse parity.
type Tabs struct {
	Base
	items      []TabItem
	active     int
	hover      int
	tokens     *TokenSet
	ranges     []tabRange
	chrome     TabsChrome
	barFocused bool
	scroll     int // first visible tab index when strip overflows past min widths
}

func NewTabs(id ComponentID, tokens *TokenSet) *Tabs {
	return &Tabs{Base: NewBase(id), active: -1, hover: -1, tokens: tokens}
}

func (t *Tabs) SetTokens(tokens *TokenSet) { t.tokens = tokens }

// SetChrome selects popup or shell color roles.
func (t *Tabs) SetChrome(chrome TabsChrome) { t.chrome = chrome }

// SetBarFocused marks keyboard focus on the tab strip (shell separator accent).
func (t *Tabs) SetBarFocused(focused bool) { t.barFocused = focused }

// SetScroll sets the first visible tab index (used by tests).
func (t *Tabs) SetScroll(offset int) { t.scroll = max(0, offset) }

// ScrollOffset returns the first visible tab index.
func (t *Tabs) ScrollOffset() int { return t.scroll }

// SyncHover sets hover index (-1 clears).
func (t *Tabs) SyncHover(index int) {
	if index < 0 || index >= len(t.items) {
		t.hover = -1
		return
	}
	t.hover = index
}

// HoverIndex returns the current hover index.
func (t *Tabs) HoverIndex() int { return t.hover }

// HitTest returns the tab index and whether the close affordance was hit.
func (t *Tabs) HitTest(localX int) (int, bool) { return t.hit(localX) }

// CloseLocalX returns the local X of the close glyph for tabIndex, or -1.
func (t *Tabs) CloseLocalX(tabIndex int) int {
	if tabIndex < 0 || tabIndex >= len(t.ranges) {
		return -1
	}
	hit := t.ranges[tabIndex]
	if hit.start < 0 || hit.end <= hit.start || !t.items[tabIndex].Closable {
		return -1
	}
	return hit.closeStart
}

func (t *Tabs) SetItems(items []TabItem, activeID string) {
	t.items = append(t.items[:0], items...)
	t.active = -1
	for i := range t.items {
		if t.items[i].ID == activeID {
			t.active = i
			break
		}
	}
	if t.active < 0 && len(t.items) > 0 {
		t.active = 0
	}
	if r := t.Bounds(); !r.Empty() {
		t.ranges = t.computeRanges(r.W)
	}
}

func (t *Tabs) ActiveID() string {
	if t.active < 0 || t.active >= len(t.items) {
		return ""
	}
	return t.items[t.active].ID
}

func (t *Tabs) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: 1})
}

func (t *Tabs) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	t.ranges = t.computeRanges(r.W)
}

func (t *Tabs) idealTabWidth(i int, label string) int {
	item := t.items[i]
	w := tabLeadPad + rendering.VisibleWidth(label)
	if t.chrome != TabsShell && item.Icon != "" {
		w += rendering.VisibleWidth(item.Icon) + 1
	}
	if item.Attention {
		w += 2 // "• "
	}
	if item.Closable {
		w += rendering.VisibleWidth(" ×")
	}
	if t.chrome == TabsShell && i == t.active {
		w += rendering.VisibleWidth("❯ ")
	}
	return max(tabMinW, w)
}

func (t *Tabs) displayLabel(i int) string {
	item := t.items[i]
	label := item.Label
	if t.chrome != TabsShell && item.Icon != "" {
		label = item.Icon + " " + label
	}
	if item.Attention {
		label = "• " + label
	}
	return rendering.TruncateVisual(label, tabMaxLabelW, "…")
}

func (t *Tabs) computeRanges(width int) []tabRange {
	n := len(t.items)
	ranges := make([]tabRange, n)
	if n == 0 || width <= 0 {
		t.scroll = 0
		return ranges
	}

	ideal := make([]int, n)
	total := 0
	for i := range t.items {
		ideal[i] = t.idealTabWidth(i, t.displayLabel(i))
		total += ideal[i]
	}
	gaps := n - 1
	total += gaps * tabGap

	widths := append([]int(nil), ideal...)
	if total > width {
		// Shrink proportionally down to tabMinW.
		available := max(1, width-gaps*tabGap)
		sumIdeal := 0
		for _, w := range ideal {
			sumIdeal += w
		}
		if sumIdeal <= 0 {
			sumIdeal = n
		}
		assigned := 0
		for i := range widths {
			if i == n-1 {
				widths[i] = max(tabMinW, available-assigned)
			} else {
				w := available * ideal[i] / sumIdeal
				if w < tabMinW {
					w = tabMinW
				}
				widths[i] = w
				assigned += w
			}
		}
		// If mins overflowed available, clamp everyone to min and scroll.
		sum := 0
		for _, w := range widths {
			sum += w
		}
		if sum+gaps*tabGap > width {
			for i := range widths {
				widths[i] = tabMinW
			}
		}
	}

	sum := 0
	for _, w := range widths {
		sum += w
	}
	needScroll := sum+gaps*tabGap > width
	if !needScroll {
		t.scroll = 0
	} else {
		t.ensureActiveVisible(widths, width)
	}

	x := 0
	for i := 0; i < n; i++ {
		if i < t.scroll {
			ranges[i] = tabRange{start: -1, end: -1, closeStart: -1}
			continue
		}
		w := widths[i]
		if x >= width {
			ranges[i] = tabRange{start: -1, end: -1, closeStart: -1}
			continue
		}
		end := min(width, x+w)
		closeStart := end
		if t.items[i].Closable {
			closeW := rendering.VisibleWidth(" ×")
			closeStart = max(x, end-closeW)
		}
		ranges[i] = tabRange{start: x, end: end, closeStart: closeStart}
		x = end + tabGap
	}
	return ranges
}

// ensureActiveVisible scrolls so the active tab fits in width at min sizes.
func (t *Tabs) ensureActiveVisible(widths []int, width int) {
	n := len(widths)
	if n == 0 || t.active < 0 {
		t.scroll = 0
		return
	}
	if t.scroll > t.active {
		t.scroll = t.active
	}
	for {
		x := 0
		fits := true
		activeEnd := -1
		for i := t.scroll; i < n; i++ {
			if i > t.scroll {
				x += tabGap
			}
			end := x + widths[i]
			if i == t.active {
				activeEnd = end
			}
			if end > width {
				fits = false
				break
			}
			x = end
		}
		if fits && activeEnd >= 0 && activeEnd <= width {
			return
		}
		if t.scroll >= t.active {
			// Cannot scroll further toward active; stop.
			return
		}
		// Active is past the right edge — advance scroll.
		t.scroll++
	}
}

func (t *Tabs) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() {
		return
	}
	var bg, fg, muted, accent, hover cell.Color
	if t.chrome == TabsShell {
		bg = t.color(ColorBg)
		fg = t.color(ColorFg)
		muted = t.color(ColorMuted)
		accent = t.color(ColorAccent)
		hover = t.color(ColorHover)
	} else {
		bg = t.color(ColorPopupBg)
		fg = t.color(ColorFg)
		muted = t.color(ColorMuted)
		accent = t.color(ColorAccent)
		hover = t.color(ColorHover)
	}
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	for i, item := range t.items {
		if i >= len(t.ranges) {
			break
		}
		hit := t.ranges[i]
		w := hit.end - hit.start
		if w <= 0 || hit.start < 0 {
			continue
		}
		itemFG, itemBG, attr := muted, bg, cell.Attrs(0)
		if i == t.active {
			if t.chrome == TabsShell {
				itemFG, attr = fg, cell.AttrBold
			} else {
				itemFG, itemBG, attr = fg, t.color(ColorPopupSubtleBg), cell.AttrBold
			}
		} else if i == t.hover || (ctx.Hovered && i == t.hover) {
			itemBG = hover
		}

		label := t.displayLabel(i)
		close := ""
		if item.Closable {
			close = " ×"
		}
		marker := ""
		if t.chrome == TabsShell && i == t.active {
			marker = "❯ "
		}
		closeW := rendering.VisibleWidth(close)
		markerW := rendering.VisibleWidth(marker)
		availForLabel := max(1, w-tabLeadPad-markerW-closeW)
		label = rendering.TruncateVisual(label, availForLabel, "…")
		labelW := rendering.VisibleWidth(label)

		FillRect(buf, 0, 0, Rect{X: originX + r.X + hit.start, Y: originY + r.Y, W: w, H: 1}, cell.BlankCell(itemBG))
		x := originX + r.X + hit.start
		y := originY + r.Y
		buf.WriteString(x, y, " ", itemFG, itemBG, attr)
		x++
		if marker != "" {
			buf.WriteString(x, y, marker, accent, itemBG, attr)
			x += markerW
		}
		buf.WriteString(x, y, label, itemFG, itemBG, attr)
		x += labelW
		pad := (originX + r.X + hit.end) - x - closeW
		if pad > 0 {
			buf.WriteString(x, y, strings.Repeat(" ", pad), itemFG, itemBG, attr)
			x += pad
		}
		if close != "" {
			buf.WriteString(x, y, close, itemFG, itemBG, attr)
		}
	}
}

func (t *Tabs) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if len(t.items) == 0 {
			return EventResult{}
		}
		var next int
		switch {
		case msg.Code == tea.KeyLeft || msg.String() == "ctrl+[":
			next = (t.active - 1 + len(t.items)) % len(t.items)
		case msg.Code == tea.KeyRight || msg.String() == "ctrl+]":
			next = (t.active + 1) % len(t.items)
		case msg.Code == tea.KeyEnter || msg.Code == tea.KeyDown || msg.Code == tea.KeyTab:
			return EventResult{Handled: true, Action: &Action{ID: "tabs.enter", Payload: t.ActiveID()}, Invalidate: InvalidateFocus | InvalidatePaint}
		default:
			return EventResult{}
		}
		t.active = next
		if r := t.Bounds(); !r.Empty() {
			t.ranges = t.computeRanges(r.W)
		}
		return EventResult{Handled: true, Action: &Action{ID: "tabs.activate", Payload: t.ActiveID()}, Invalidate: InvalidateLayout | InvalidatePaint}
	case tea.MouseMotionMsg:
		index, _ := t.hit(ctx.LocalX)
		if index != t.hover {
			t.hover = index
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return EventResult{}
		}
		index, closeHit := t.hit(ctx.LocalX)
		if index < 0 {
			return EventResult{Handled: true}
		}
		if closeHit {
			return EventResult{Handled: true, Action: &Action{ID: "tabs.close", Payload: t.items[index].ID}, Invalidate: InvalidateLayout | InvalidatePaint}
		}
		t.active = index
		if r := t.Bounds(); !r.Empty() {
			t.ranges = t.computeRanges(r.W)
		}
		return EventResult{Handled: true, Action: &Action{ID: "tabs.activate", Payload: t.items[index].ID}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
	}
	return EventResult{}
}

func (t *Tabs) hit(x int) (int, bool) {
	for i, hit := range t.ranges {
		if hit.start < 0 || hit.end <= hit.start {
			continue
		}
		if x >= hit.start && x < hit.end {
			return i, t.items[i].Closable && x >= hit.closeStart
		}
	}
	return -1, false
}

func (t *Tabs) color(role SemanticColor) cell.Color {
	if t.tokens == nil {
		return cell.NoColor
	}
	return t.tokens.Color(role)
}

func (t *Tabs) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: "Panel tabs", Value: t.ActiveID(), Selected: t.active >= 0}
}
