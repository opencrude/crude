package virtlist

import (
	"sort"
)

type Item struct {
	ID       string
	Height   int
	Dirty    bool
	Rendered []string
	Data     interface{}
}

type HeightIndex struct {
	prefixSums []int
	total      int
}

func (hi *HeightIndex) Rebuild(items []Item) {
	n := len(items)
	hi.prefixSums = make([]int, n+1)
	for i, item := range items {
		h := item.Height
		if h < 1 {
			h = 1
		}
		hi.prefixSums[i+1] = hi.prefixSums[i] + h
	}
	hi.total = hi.prefixSums[n]
}

func (hi *HeightIndex) ItemAtLine(globalLine int) (itemIdx, localLine int) {
	if len(hi.prefixSums) == 0 {
		return 0, 0
	}
	n := len(hi.prefixSums) - 1
	if n == 0 {
		return 0, 0
	}
	total := hi.total
	if total <= 0 {
		return 0, 0
	}
	if globalLine < 0 {
		return 0, 0
	}
	if globalLine >= total {
		return n - 1, hi.prefixSums[n] - hi.prefixSums[n-1] - 1
	}
	idx := sort.Search(n, func(i int) bool {
		return hi.prefixSums[i+1] > globalLine
	})
	localLine = globalLine - hi.prefixSums[idx]
	return idx, localLine
}

func (hi *HeightIndex) LineOfItem(itemIdx int) int {
	n := len(hi.prefixSums) - 1
	if n <= 0 {
		return 0
	}
	itemIdx = clamp(itemIdx, 0, n-1)
	return hi.prefixSums[itemIdx]
}

func (hi *HeightIndex) TotalHeight() int {
	return hi.total
}

func (hi *HeightIndex) Len() int {
	return len(hi.prefixSums) - 1
}

type VirtualList struct {
	items         []Item
	index         HeightIndex
	scrollY       int
	viewportH     int
	overscan      int
	indexDirty    bool
	bottomPadding int
}

func New() *VirtualList {
	return &VirtualList{
		overscan:      4,
		scrollY:       0,
		bottomPadding: 0,
	}
}

func (vl *VirtualList) SetItems(items []Item) {
	vl.items = items
	vl.indexDirty = true
}

func (vl *VirtualList) Items() []Item {
	return vl.items
}

func (vl *VirtualList) Item(idx int) *Item {
	if idx < 0 || idx >= len(vl.items) {
		return nil
	}
	return &vl.items[idx]
}

func (vl *VirtualList) ItemCount() int {
	return len(vl.items)
}

func (vl *VirtualList) AppendItem(item Item) {
	vl.items = append(vl.items, item)
	vl.indexDirty = true
}

func (vl *VirtualList) UpdateItemAt(idx int, item Item) {
	if idx < 0 || idx >= len(vl.items) {
		return
	}
	vl.items[idx] = item
	vl.indexDirty = true
}

func (vl *VirtualList) InvalidateItem(id string) {
	for i := range vl.items {
		if vl.items[i].ID == id {
			vl.items[i].Dirty = true
			vl.indexDirty = true
			return
		}
	}
}

func (vl *VirtualList) ItemHeight(id string) (int, bool) {
	for i := range vl.items {
		if vl.items[i].ID == id {
			if vl.items[i].Height > 0 {
				return vl.items[i].Height, true
			}
			return 0, false
		}
	}
	return 0, false
}

func (vl *VirtualList) InvalidateAll() {
	for i := range vl.items {
		vl.items[i].Dirty = true
	}
}

func (vl *VirtualList) InvalidateHeights() {
	for i := range vl.items {
		vl.items[i].Dirty = true
	}
	vl.indexDirty = true
}

func (vl *VirtualList) BuildIndex() {
	if vl.indexDirty {
		vl.index.Rebuild(vl.items)
		vl.indexDirty = false
	}
}

func (vl *VirtualList) ItemAtLine(globalLine int) (itemIdx, localLine int) {
	vl.BuildIndex()
	return vl.index.ItemAtLine(globalLine)
}

func (vl *VirtualList) LineOfItem(itemIdx int) int {
	vl.BuildIndex()
	return vl.index.LineOfItem(itemIdx)
}

func (vl *VirtualList) TotalHeight() int {
	vl.BuildIndex()
	return vl.index.TotalHeight()
}

func (vl *VirtualList) VisibleRange() (firstItem, lastItem, skipLines int) {
	vl.BuildIndex()
	if len(vl.items) == 0 || vl.viewportH <= 0 {
		return 0, -1, 0
	}
	firstItem, localLine := vl.index.ItemAtLine(vl.scrollY)
	skipLines = localLine
	accum := 0
	lastItem = firstItem
	for i := firstItem; i < len(vl.items); i++ {
		h := vl.items[i].Height
		if h < 1 {
			h = 1
		}
		accum += h
		if accum-localLine > vl.viewportH {
			lastItem = i
			break
		}
		lastItem = i
	}
	return firstItem, lastItem, skipLines
}

func (vl *VirtualList) RenderRange() (firstItem, lastItem, skipLines int) {
	first, last, skip := vl.VisibleRange()
	firstAdj := first - vl.overscan
	if firstAdj < 0 {
		firstAdj = 0
	}
	lastAdj := last + vl.overscan
	if lastAdj >= len(vl.items) {
		lastAdj = len(vl.items) - 1
	}
	return firstAdj, lastAdj, skip
}

func (vl *VirtualList) ScrollBy(lines int) {
	vl.scrollY += lines
	vl.ClampScroll()
}

func (vl *VirtualList) ScrollTo(y int) {
	vl.scrollY = y
	vl.ClampScroll()
}

func (vl *VirtualList) ScrollToBottom() {
	vl.scrollY = vl.MaxScroll()
}

func (vl *VirtualList) ScrollToItem(idx int) {
	vl.scrollY = vl.LineOfItem(idx)
	vl.ClampScroll()
}

func (vl *VirtualList) ScrollToGlobalLine(line int) {
	vl.scrollY = line
	vl.ClampScroll()
}

func (vl *VirtualList) ClampScroll() {
	if vl.viewportH <= 0 {
		vl.scrollY = 0
		return
	}
	vl.scrollY = clamp(vl.scrollY, 0, vl.MaxScroll())
}

func (vl *VirtualList) MaxScroll() int {
	vl.BuildIndex()
	return max(0, vl.index.TotalHeight()+vl.bottomPadding-vl.viewportH)
}

func (vl *VirtualList) ScrollY() int {
	return vl.scrollY
}

func (vl *VirtualList) SetViewport(h int) {
	vl.viewportH = h
	vl.ClampScroll()
}

func (vl *VirtualList) ViewportHeight() int {
	return vl.viewportH
}

func (vl *VirtualList) IsAtBottom() bool {
	return vl.scrollY >= vl.MaxScroll()
}

func (vl *VirtualList) IsAtTop() bool {
	return vl.scrollY == 0
}

func (vl *VirtualList) BottomPadding() int {
	return vl.bottomPadding
}

func (vl *VirtualList) SetBottomPadding(n int) {
	vl.bottomPadding = n
}

// RenderVisible renders dirty items within the visible range and returns
// the visible lines. Heights are recalculated during rendering.
func (vl *VirtualList) RenderVisible(renderFn func(idx int, item *Item, width int) []string, width int) []string {
	vl.BuildIndex()

	if len(vl.items) == 0 || vl.viewportH <= 0 {
		result := make([]string, vl.viewportH)
		return result
	}

	renderFirst, renderLast, _ := vl.RenderRange()

	for i := renderFirst; i <= renderLast; i++ {
		item := &vl.items[i]
		if item.Dirty {
			rendered := renderFn(i, item, width)
			item.Rendered = rendered
			item.Height = len(rendered)
			item.Dirty = false
		}
	}

	heightsChanged := false
	for i := renderFirst; i <= renderLast; i++ {
		h := vl.items[i].Height
		if h < 1 {
			h = 1
		}
		idxH := vl.index.prefixSums[i+1] - vl.index.prefixSums[i]
		if h != idxH {
			heightsChanged = true
			break
		}
	}

	if heightsChanged {
		vl.indexDirty = true
		vl.BuildIndex()
		renderFirst, renderLast, _ = vl.RenderRange()
		for i := renderFirst; i <= renderLast; i++ {
			item := &vl.items[i]
			if item.Dirty {
				rendered := renderFn(i, item, width)
				item.Rendered = rendered
				item.Height = len(rendered)
				item.Dirty = false
			}
		}
	}

	firstItem, lastItem, skipLines := vl.VisibleRange()
	return vl.assembleOutput(firstItem, lastItem, skipLines)
}

// ExtractVisible re-extracts visible lines from already-rendered items
// at the current scrollY without re-rendering. Used after adjusting
// scroll position when heights are already accurate.
func (vl *VirtualList) ExtractVisible() []string {
	vl.BuildIndex()

	if len(vl.items) == 0 || vl.viewportH <= 0 {
		return make([]string, vl.viewportH)
	}

	firstItem, lastItem, skipLines := vl.VisibleRange()
	return vl.assembleOutput(firstItem, lastItem, skipLines)
}

func (vl *VirtualList) assembleOutput(firstItem, lastItem, skipLines int) []string {
	totalContent := vl.index.TotalHeight()

	var output []string
	accumulated := 0
	for i := firstItem; i <= lastItem; i++ {
		lines := vl.items[i].Rendered
		skip := 0
		if i == firstItem {
			skip = skipLines
		}
		if skip < 0 {
			skip = 0
		}
		if skip > len(lines) {
			skip = len(lines)
		}
		for j := skip; j < len(lines); j++ {
			output = append(output, lines[j])
			accumulated++
			if accumulated >= vl.viewportH {
				goto done
			}
		}
	}
done:
	if accumulated < vl.viewportH {
		needed := vl.viewportH - accumulated
		if totalContent > vl.viewportH {
			// Overscroll: scrolled past content end (bottom padding area).
			// Append empty lines at the bottom.
			for i := 0; i < needed; i++ {
				output = append(output, "")
			}
		} else {
			// Gravity: content shorter than viewport.
			// Prepend empty lines so content sits at the bottom.
			padding := make([]string, needed)
			output = append(padding, output...)
		}
	}
	return output
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
