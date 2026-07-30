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
}

func NewTabs(id ComponentID, tokens *TokenSet) *Tabs {
	return &Tabs{Base: NewBase(id), active: -1, hover: -1, tokens: tokens}
}

func (t *Tabs) SetTokens(tokens *TokenSet) { t.tokens = tokens }

// SetChrome selects popup or shell color roles.
func (t *Tabs) SetChrome(chrome TabsChrome) { t.chrome = chrome }

// SetBarFocused marks keyboard focus on the tab strip (shell separator accent).
func (t *Tabs) SetBarFocused(focused bool) { t.barFocused = focused }

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
		// Shell chrome matches the old horizontal tab bar: label only (no
		// icon). Icons caused tofu/`?` and width skew on many terminals.
		if t.chrome != TabsShell && item.Icon != "" {
			w += rendering.VisibleWidth(item.Icon) + 1
		}
		if item.Attention {
			w += 2
		}
		if item.Closable {
			w += 2
		}
		if t.chrome == TabsShell && i == t.active {
			w += 2 // "❯ "
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
		if w <= 0 {
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
		label := item.Label
		if t.chrome != TabsShell && item.Icon != "" {
			label = item.Icon + " " + label
		}
		if item.Attention {
			label = "• " + label
			itemFG = accent
		}
		close := ""
		if item.Closable {
			close = " ×"
		}
		marker := ""
		if t.chrome == TabsShell && i == t.active {
			marker = "❯ "
		}
		available := max(1, w-1-rendering.VisibleWidth(marker)-rendering.VisibleWidth(close))
		label = rendering.TruncateVisual(label, available, "…")
		FillRect(buf, 0, 0, Rect{X: originX + r.X + hit.start, Y: originY + r.Y, W: w, H: 1}, cell.BlankCell(itemBG))
		x := originX + r.X + hit.start
		y := originY + r.Y
		buf.WriteString(x, y, " ", itemFG, itemBG, attr)
		x++
		if marker != "" {
			buf.WriteString(x, y, marker, accent, itemBG, attr)
			x += rendering.VisibleWidth(marker)
		}
		rest := label
		pad := w - (x - (originX + r.X)) - rendering.VisibleWidth(close)
		if pad > 0 {
			rest += strings.Repeat(" ", pad)
		}
		rest += close
		buf.WriteString(x, y, rest, itemFG, itemBG, attr)
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
