package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// TabsChrome selects the background the strip sits on: shell chrome uses the
// shell background, popup chrome the popup background. It never changes the
// strip's structure — every tab bar in the app shares one look.
type TabsChrome uint8

const (
	TabsPopup TabsChrome = iota
	TabsShell
)

// Tab sizing policy (one rule for every surface):
//   - Content-sized: width = label + attention marker + close affordance.
//   - Long labels ellipsis-truncate at tabMaxLabelW (28).
//   - Overflow: shrink all tabs proportionally down to tabMinW (8).
//   - If still too narrow: horizontally scroll so the active tab stays visible.
const (
	tabMaxLabelW = 28
	tabMinW      = 8 // shrink floor; never a padding width
	tabGap       = 2 // column between tabs
)

// TabItem is one entry in a Tabs control. Tab bars paint labels only: an icon
// here would be a tofu risk (the legacy shell tab bar deliberately omitted the
// window tab icon), and every consumer is label-first.
type TabItem struct {
	ID        string
	Label     string
	Closable  bool
	Attention bool
}

type tabRange struct {
	// start/end bound the tab's cell: hover fill, hit area, and active
	// underline. labelStart is where the label content is drawn, which is the
	// cell start except for the first visible tab, whose cell reaches back to
	// the bar's left edge while its content keeps the bar's leading inset.
	start, end, closeStart, labelStart int
}

// Tabs is a horizontal retained tab strip with keyboard and mouse parity.
//
// Structure (the Status Overview mode strip is the reference):
//
//	row 0: labels — active accent+bold, others muted, hover fills the tab
//	row 1: baseline rule — border ink, accent when the strip is focused, and
//	       accent under the active tab so the selection reads at a glance
//
// The strip owns its baseline. Parents may publish a wider rule row (RuleBleed)
// so a docked strip's rule still runs edge to edge, exactly like Form and
// ActionBar consumers do with their own rules.
type Tabs struct {
	Base
	items      []TabItem
	active     int
	hover      int
	tokens     *TokenSet
	ranges     []tabRange
	chrome     TabsChrome
	barFocused bool
	scroll     int // first visible tab index when the strip overflows past min widths
	ruleBleed  bool
	bleedX     int
	bleedW     int
	labelInset int
}

func NewTabs(id ComponentID, tokens *TokenSet) *Tabs {
	return &Tabs{Base: NewBase(id), active: -1, hover: -1, tokens: tokens}
}

func (t *Tabs) SetTokens(tokens *TokenSet) { t.tokens = tokens }

// SetChrome selects the strip's background palette (shell or popup).
func (t *Tabs) SetChrome(chrome TabsChrome) { t.chrome = chrome }

// SetBarFocused marks keyboard focus on the tab strip: the baseline rule turns
// accent-heavy so focus is distinct from hover and from the active tab.
func (t *Tabs) SetBarFocused(focused bool) { t.barFocused = focused }

// SetRuleFullBleed allows the baseline rule to span the parent-published row
// geometry instead of the strip's own bounds (docked surfaces opt in).
func (t *Tabs) SetRuleFullBleed(v bool) { t.ruleBleed = v }

// SetRuleBleed is called by the parent at layout time with the deck row
// geometry: offsetX columns sit left of the strip origin, width is the full
// row width. Mirrors Form/ActionBar/panel-deck rule publishing.
func (t *Tabs) SetRuleBleed(offsetX, width int) { t.bleedX, t.bleedW = offsetX, width }

// SetLabelInset insets the label row from the strip's left edge while the
// baseline rule keeps spanning the full row from that edge. This is the shape a
// bar takes when its labels need breathing room beside neighbouring chrome (the
// main tab bar beside the navigation rail): the labels, their hit areas, the
// hover fill, and the active underline shift together, so the assignment stays
// legible and nothing is left floating left of the rule.
func (t *Tabs) SetLabelInset(n int) { t.labelInset = max(0, n) }

// LabelInset returns the label-row inset.
func (t *Tabs) LabelInset() int { return t.labelInset }

// ruleSpan resolves the baseline rule columns for a strip of contentW columns.
func (t *Tabs) ruleSpan(contentW int) (x, w int) {
	if t.ruleBleed && t.bleedW-t.bleedX > contentW {
		return -t.bleedX, t.bleedW
	}
	return 0, contentW
}

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

// StripHeight is the row count every tab bar reserves: labels + baseline.
const StripHeight = 2

func (t *Tabs) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: StripHeight})
}

func (t *Tabs) Layout(_ LayoutContext, r Rect) {
	t.SetBounds(r)
	t.ranges = t.computeRanges(r.W)
}

// idealTabWidth is the content-sized width: ordinary strips never pad short
// labels out to a fixed slot, so the active underline matches the label.
func (t *Tabs) idealTabWidth(i int, label string) int {
	item := t.items[i]
	w := rendering.VisibleWidth(label)
	if item.Attention {
		w += 2 // "• "
	}
	if item.Closable {
		w += rendering.VisibleWidth(" ×")
	}
	return max(1, w)
}

func (t *Tabs) displayLabel(i int) string {
	item := t.items[i]
	label := item.Label
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

	// Labels are laid out in the content width left of the row after the
	// label-row inset, then offset back into strip coordinates so the baseline
	// keeps spanning the whole row.
	inset := t.labelInset
	if inset > width-1 {
		inset = max(0, width-1)
	}
	avail := width - inset

	ideal := make([]int, n)
	total := 0
	for i := range t.items {
		ideal[i] = t.idealTabWidth(i, t.displayLabel(i))
		total += ideal[i]
	}
	gaps := n - 1
	total += gaps * tabGap

	widths := append([]int(nil), ideal...)
	if total > avail {
		// Shrink proportionally down to tabMinW.
		available := max(1, avail-gaps*tabGap)
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
		if sum+gaps*tabGap > avail {
			for i := range widths {
				widths[i] = tabMinW
			}
		}
	}

	sum := 0
	for _, w := range widths {
		sum += w
	}
	needScroll := sum+gaps*tabGap > avail
	if !needScroll {
		t.scroll = 0
	} else {
		t.ensureActiveVisible(widths, avail)
	}

	x := inset
	for i := 0; i < n; i++ {
		if i < t.scroll {
			ranges[i] = tabRange{start: -1, end: -1, closeStart: -1, labelStart: -1}
			continue
		}
		w := widths[i]
		if x >= width {
			ranges[i] = tabRange{start: -1, end: -1, closeStart: -1, labelStart: -1}
			continue
		}
		end := min(width, x+w)
		closeStart := end
		if t.items[i].Closable {
			closeW := rendering.VisibleWidth(" ×")
			closeStart = max(x, end-closeW)
		}
		ranges[i] = tabRange{start: x, end: end, closeStart: closeStart, labelStart: x}
		x = end + tabGap
	}
	if inset > 0 && t.scroll < n && ranges[t.scroll].start == inset {
		// The leading inset is bar-level breathing room, not part of a tab: the
		// first visible tab still owns every column up to the bar's left edge,
		// so its hover fill, hit area, and active underline reach that edge
		// while its label content stays inset.
		ranges[t.scroll].start = 0
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

// focused reports whether the strip paints its focus treatment: the shell
// forwards bar focus, every other surface relies on Host focus.
func (t *Tabs) focused(ctx PaintContext) bool { return t.barFocused || ctx.Focused }

func (t *Tabs) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := t.Bounds()
	if r.Empty() {
		return
	}
	bg := t.color(ColorBg)
	if t.chrome != TabsShell {
		bg = t.color(ColorPopupBg)
	}
	muted := t.color(ColorMuted)
	accent := t.color(ColorAccent)
	hover := t.color(ColorHover)
	border := t.color(ColorBorder)
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))

	// Baseline row: one rule for the whole strip, wider than the strip when
	// the parent published a rule row.
	glyph := FirstRune(DefaultGlyphsFromTokens(t.tokens).BorderH, '-')
	if r.H > 1 {
		ruleX, ruleW := t.ruleSpan(r.W)
		ruleFG, attr := border, cell.Attrs(0)
		if t.focused(ctx) {
			ruleFG, attr = accent, cell.AttrBold
			glyph = FirstRune("━", '-')
		}
		for x := 0; x < ruleW; x++ {
			buf.Set(originX+r.X+ruleX+x, originY+r.Y+1, cell.NewCell(glyph, ruleFG, bg, attr))
		}
	}

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
		switch i {
		case t.active:
			itemFG, attr = accent, cell.AttrBold
		case t.hover:
			itemBG = hover
		}

		label := t.displayLabel(i)
		close := ""
		if item.Closable {
			close = " ×"
		}
		closeW := rendering.VisibleWidth(close)
		label = rendering.TruncateVisual(label, max(1, hit.end-hit.labelStart-closeW), "…")
		labelW := rendering.VisibleWidth(label)

		FillRect(buf, 0, 0, Rect{X: originX + r.X + hit.start, Y: originY + r.Y, W: w, H: 1}, cell.BlankCell(itemBG))
		// Content starts at labelStart, not the cell start: the first visible
		// tab's cell reaches the bar's left edge so its fill and underline do,
		// while its label keeps the bar's leading inset.
		x := originX + r.X + hit.labelStart
		y := originY + r.Y
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
		// The active tab owns its slice of the baseline: same slot as the
		// rule, accent ink, so the selected mode is unambiguous even when the
		// strip itself is unfocused.
		if i == t.active && r.H > 1 {
			for underlineX := hit.start; underlineX < hit.end; underlineX++ {
				buf.Set(originX+r.X+underlineX, originY+r.Y+1, cell.NewCell(glyph, accent, bg, cell.AttrBold))
			}
		}
	}
}

func (t *Tabs) Handle(ctx EventContext, evt Event) EventResult {
	switch evt.Kind {
	case EventKey:
		if len(t.items) == 0 {
			return EventResult{}
		}
		var next int
		switch {
		case evt.Key.Code == tea.KeyLeft || evt.Key.String() == "ctrl+[":
			next = (t.active - 1 + len(t.items)) % len(t.items)
		case evt.Key.Code == tea.KeyRight || evt.Key.String() == "ctrl+]":
			next = (t.active + 1) % len(t.items)
		case evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeyDown || evt.Key.Code == tea.KeyTab:
			return EventResult{Handled: true, Action: &Action{ID: "tabs.enter", Payload: t.ActiveID()}, Invalidate: InvalidateFocus | InvalidatePaint}
		default:
			return EventResult{}
		}
		t.active = next
		if r := t.Bounds(); !r.Empty() {
			t.ranges = t.computeRanges(r.W)
		}
		return EventResult{Handled: true, Action: &Action{ID: "tabs.activate", Payload: t.ActiveID()}, Invalidate: InvalidateLayout | InvalidatePaint}
	case EventMouseMotion:
		index, _ := t.hit(ctx.LocalX)
		if index != t.hover {
			t.hover = index
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: true}
	case EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
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
	return Semantics{Role: RoleGroup, Label: "Tabs", Value: t.ActiveID(), Selected: t.active >= 0}
}
