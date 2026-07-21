package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TabItem is one entry in a Tabs control.
type TabItem struct {
	ID        string
	Label     string
	Closable  bool
	Attention bool
}

type tabRange struct {
	start, end, closeStart int
}

// Tabs is a horizontal retained tab strip with keyboard and mouse parity.
type Tabs struct {
	Base
	items  []TabItem
	active int
	hover  int
	tokens *TokenSet
	ranges []tabRange
}

func NewTabs(id ComponentID, tokens *TokenSet) *Tabs {
	return &Tabs{Base: NewBase(id), active: -1, hover: -1, tokens: tokens}
}

func (t *Tabs) SetTokens(tokens *TokenSet) { t.tokens = tokens }

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

func (t *Tabs) computeRanges(width int) []tabRange {
	ranges := make([]tabRange, len(t.items))
	if len(t.items) == 0 || width <= 0 {
		return ranges
	}
	ideal := make([]int, len(t.items))
	total := 0
	for i, item := range t.items {
		w := rendering.VisibleWidth(item.Label) + 2
		if item.Attention {
			w += 2
		}
		if item.Closable {
			w += 2
		}
		w = max(6, min(24, w))
		ideal[i] = w
		total += w
	}
	total += len(t.items) - 1
	if total > width {
		available := max(1, width-(len(t.items)-1))
		perTab := max(3, available/len(t.items))
		for i := range ideal {
			ideal[i] = min(ideal[i], perTab)
		}
	}
	x := 0
	for i, w := range ideal {
		end := min(width, x+w)
		closeStart := end
		if t.items[i].Closable {
			closeStart = max(x, end-2)
		}
		ranges[i] = tabRange{start: x, end: end, closeStart: closeStart}
		x = end + 1
		if x >= width {
			break
		}
	}
	return ranges
}

func (t *Tabs) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() {
		return
	}
	bg, fg, muted, accent, hover := t.color(ColorPopupBg), t.color(ColorFg), t.color(ColorMuted), t.color(ColorAccent), t.color(ColorHover)
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	for i, item := range t.items {
		if i >= len(t.ranges) {
			break
		}
		hit := t.ranges[i]
		w := hit.end - hit.start
		if w <= 0 {
			continue
		}
		itemFG, itemBG, attr := muted, bg, cell.Attrs(0)
		if i == t.active {
			itemFG, itemBG, attr = fg, t.color(ColorPopupSubtleBg), cell.AttrBold
		} else if i == t.hover || (ctx.Hovered && i == t.hover) {
			itemBG = hover
		}
		text := item.Label
		if item.Attention {
			text = "• " + text
			itemFG = accent
		}
		close := ""
		if item.Closable {
			close = " ×"
		}
		available := max(1, w-rendering.VisibleWidth(close)-1)
		text = rendering.TruncateVisual(text, available, "…")
		line := " " + text
		padding := w - rendering.VisibleWidth(line) - rendering.VisibleWidth(close)
		if padding > 0 {
			line += strings.Repeat(" ", padding)
		}
		line += close
		FillRect(buf, 0, 0, Rect{X: originX + r.X + hit.start, Y: originY + r.Y, W: w, H: 1}, cell.BlankCell(itemBG))
		buf.WriteString(originX+r.X+hit.start, originY+r.Y, rendering.TruncateVisual(line, w, ""), itemFG, itemBG, attr)
	}
}

func (t *Tabs) Handle(ctx EventContext, msg tea.Msg) EventResult {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if len(t.items) == 0 {
			return EventResult{}
		}
		next := t.active
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
		return EventResult{Handled: true, Action: &Action{ID: "tabs.activate", Payload: t.items[index].ID}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
	}
	return EventResult{}
}

func (t *Tabs) hit(x int) (int, bool) {
	for i, hit := range t.ranges {
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
