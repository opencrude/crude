package ui

import (
	"math"
	"time"
)

// Strip-first tab drag.
//
// A drag that stays inside the strip reorders it: the pressed tab follows the
// pointer and the other tabs slide out of its way. Only when the pointer
// leaves the strip does the host see a placement drag (tabs.drag with
// InStrip false), so pane splits never compete with reordering. The strip
// never changes its own items: a reorder is proposed with tabs.reorder and
// the host, which owns tab order, publishes the new items.
//
// Motion follows the sortable-list model (dnd-kit, SortableJS): each tab is
// laid out at its target slot and painted with a transient offset from where
// it was last shown, eased to zero.

const tabSlideDuration = 140 * time.Millisecond

// TabReorder proposes moving tab ID before BeforeID within the same strip.
// An empty BeforeID moves the tab to the end.
type TabReorder struct {
	ID       string
	BeforeID string
}

type tabSlide struct {
	from, to float64
	start    time.Time
}

func (s tabSlide) at(now time.Time) float64 {
	if s.from == s.to {
		return s.to
	}
	p := float64(now.Sub(s.start)) / float64(tabSlideDuration)
	if p >= 1 {
		return s.to
	}
	p = math.Max(0, p)
	rest := 1 - p
	eased := 1 - rest*rest*rest // ease-out cubic
	return s.from + (s.to-s.from)*eased
}

func (s tabSlide) settled(now time.Time) bool {
	return s.from == s.to || now.Sub(s.start) >= tabSlideDuration
}

func (t *Tabs) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// NeedsAnimation reports a drag in progress or a tab still sliding.
func (t *Tabs) NeedsAnimation() bool {
	if t.dragging {
		return true
	}
	now := t.clock()
	for _, slide := range t.slides {
		if !slide.settled(now) {
			return true
		}
	}
	return false
}

func (t *Tabs) indexOfID(id string) int {
	for index, item := range t.items {
		if item.ID == id {
			return index
		}
	}
	return -1
}

func rangeWidth(r tabRange) int { return r.end - r.labelStart }

// visibleOrder lists visible item indices in strip order, excluding skip.
func (t *Tabs) visibleOrder(skip int) []int {
	order := make([]int, 0, len(t.ranges))
	for index, r := range t.ranges {
		if index != skip && r.start >= 0 && r.end > r.start {
			order = append(order, index)
		}
	}
	return order
}

func (t *Tabs) firstLabelStart() int {
	for _, r := range t.ranges {
		if r.start >= 0 && r.end > r.start {
			return r.labelStart
		}
	}
	return 0
}

// reorderSlot is the position among the other visible tabs where the dragged
// tab would land: before every tab whose center lies right of its center.
func (t *Tabs) reorderSlot(drag int) int {
	others := t.visibleOrder(drag)
	center := float64(t.dragLocalX-t.grabOffset) + float64(rangeWidth(t.ranges[drag]))/2
	slot, x := 0, t.firstLabelStart()
	for _, index := range others {
		w := rangeWidth(t.ranges[index])
		if float64(x)+float64(w)/2 < center {
			slot++
		}
		x += w + tabGap
	}
	return slot
}

// reorderTarget names the tab the dragged tab is placed before.
func (t *Tabs) reorderTarget(drag, slot int) string {
	others := t.visibleOrder(drag)
	if slot < len(others) {
		return t.items[others[slot]].ID
	}
	if len(others) == 0 {
		return ""
	}
	// After the last visible tab: before the next hidden tab, if any.
	for index := others[len(others)-1] + 1; index < len(t.items); index++ {
		if index != drag {
			return t.items[index].ID
		}
	}
	return ""
}

// layoutRanges returns where each tab belongs this frame: ordinary ranges, or
// the previewed order while the pressed tab is dragged inside the strip.
func (t *Tabs) layoutRanges() []tabRange {
	drag := t.indexOfID(t.pressID)
	if !t.dragging || !t.inStrip || drag < 0 || drag >= len(t.ranges) || t.ranges[drag].start < 0 {
		return t.ranges
	}
	others := t.visibleOrder(drag)
	slot := t.reorderSlot(drag)
	order := make([]int, 0, len(others)+1)
	order = append(order, others[:slot]...)
	order = append(order, drag)
	order = append(order, others[slot:]...)
	out := append([]tabRange(nil), t.ranges...)
	x := t.firstLabelStart()
	for _, index := range order {
		r := t.ranges[index]
		w := rangeWidth(r)
		out[index] = tabRange{start: x, end: x + w, closeStart: x + (r.closeStart - r.labelStart), labelStart: x}
		x += w + tabGap
	}
	return out
}

// paintRanges applies each tab's eased offset to its layout range. The
// dragged tab follows the pointer while it is inside the strip.
func (t *Tabs) paintRanges(width int) []tabRange {
	layout := t.layoutRanges()
	if !t.dragging && !t.settling(t.clock()) {
		t.slides = nil
		return layout
	}
	now := t.clock()
	if t.slides == nil {
		t.slides = make(map[string]tabSlide, len(t.items))
	}
	out := append([]tabRange(nil), layout...)
	drag := t.indexOfID(t.pressID)
	for index, r := range layout {
		if index >= len(t.items) || r.start < 0 || r.end <= r.start {
			continue
		}
		id := t.items[index].ID
		target := float64(r.labelStart)
		lifted := t.dragging && t.inStrip && index == drag
		if lifted {
			// The lifted tab follows the pointer, clamped to the strip.
			x := float64(min(max(0, t.dragLocalX-t.grabOffset), max(0, width-rangeWidth(r))))
			t.slides[id] = tabSlide{from: x, to: x, start: now}
		} else if slide, ok := t.slides[id]; !ok {
			t.slides[id] = tabSlide{from: target, to: target, start: now}
		} else if slide.to != target {
			t.slides[id] = tabSlide{from: slide.at(now), to: target, start: now}
		}
		shown := int(math.Round(t.slides[id].at(now)))
		if !lifted && shown == r.labelStart {
			continue
		}
		shift := shown - r.labelStart
		out[index] = tabRange{start: r.labelStart + shift, end: r.end + shift, closeStart: r.closeStart + shift, labelStart: r.labelStart + shift}
	}
	return out
}

// seedSlides records where every tab is shown when a drag starts, so the
// first previewed order already slides instead of jumping.
func (t *Tabs) seedSlides() {
	now := t.clock()
	t.slides = make(map[string]tabSlide, len(t.items))
	for index, r := range t.ranges {
		if index < len(t.items) && r.start >= 0 && r.end > r.start {
			x := float64(r.labelStart)
			t.slides[t.items[index].ID] = tabSlide{from: x, to: x, start: now}
		}
	}
}

// settling keeps slides alive after a drop until they reach their slots.
func (t *Tabs) settling(now time.Time) bool {
	return now.Before(t.settleUntil)
}

func (t *Tabs) beginSettle() {
	t.settleUntil = t.clock().Add(2 * tabSlideDuration)
}

// dragMotion updates the strip-local drag and reports whether the pointer is
// inside the strip.
func (t *Tabs) dragMotion(ctx EventContext) bool {
	r := t.Bounds()
	t.dragLocalX = ctx.LocalX
	t.inStrip = ctx.LocalY >= 0 && ctx.LocalY < max(1, r.H) && ctx.LocalX >= 0 && ctx.LocalX < r.W
	return t.inStrip
}
