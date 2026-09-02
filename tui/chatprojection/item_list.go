package chatprojection

import (
	"sort"

	"crude/tui/perf"
)

type renderItem struct {
	ID       string
	Height   int
	Dirty    bool
	Rendered []string
	Native   []PaintLine
	PaneRows int
	Data     interface{}
	// ToggleRow is the row index within Rendered (including any leading
	// spacer) that owns the disclosure affordance; -1 means none.
	ToggleRow int
}

type heightIndex struct {
	prefixSums []int
	total      int
}

func (hi *heightIndex) Rebuild(items []renderItem) {
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

func (hi *heightIndex) ItemAtLine(globalLine int) (itemIdx, localLine int) {
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

func (hi *heightIndex) LineOfItem(itemIdx int) int {
	n := len(hi.prefixSums) - 1
	if n <= 0 {
		return 0
	}
	itemIdx = clamp(itemIdx, 0, n-1)
	return hi.prefixSums[itemIdx]
}

func (hi *heightIndex) TotalHeight() int {
	return hi.total
}

func (hi *heightIndex) Len() int {
	return len(hi.prefixSums) - 1
}

type itemList struct {
	items         []renderItem
	index         heightIndex
	scrollY       int
	viewportH     int
	overscan      int
	indexDirty    bool
	bottomPadding int
	lastRenderFn  func(idx int, item *renderItem, width int) []string
	lastRenderW   int
}

func newItemList() *itemList {
	return &itemList{
		overscan:      4,
		scrollY:       0,
		bottomPadding: 0,
	}
}

func (vl *itemList) SetItems(items []renderItem) {
	vl.items = items
	vl.indexDirty = true
}

func (vl *itemList) Items() []renderItem {
	return vl.items
}

func (vl *itemList) Item(idx int) *renderItem {
	if idx < 0 || idx >= len(vl.items) {
		return nil
	}
	return &vl.items[idx]
}

func (vl *itemList) ItemCount() int {
	return len(vl.items)
}

func (vl *itemList) AppendItem(item renderItem) {
	vl.items = append(vl.items, item)
	vl.indexDirty = true
}

func (vl *itemList) UpdateItemAt(idx int, item renderItem) {
	if idx < 0 || idx >= len(vl.items) {
		return
	}
	vl.items[idx] = item
	vl.indexDirty = true
}

func (vl *itemList) InvalidateItem(id string) {
	perf.MarkItemInvalidation()
	for i := range vl.items {
		if vl.items[i].ID == id {
			vl.items[i].Dirty = true
			vl.indexDirty = true
			return
		}
	}
}

func (vl *itemList) RenameItemID(oldID, newID string) {
	for i := range vl.items {
		if vl.items[i].ID == oldID {
			vl.items[i].ID = newID
			vl.items[i].Dirty = true
			vl.indexDirty = true
			return
		}
	}
}

func (vl *itemList) ItemHeight(id string) (int, bool) {
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

func (vl *itemList) InvalidateAll() {
	perf.MarkCacheInvalidation()
	for i := range vl.items {
		vl.items[i].Dirty = true
	}
	vl.indexDirty = true
}

func (vl *itemList) InvalidateHeights() {
	for i := range vl.items {
		vl.items[i].Dirty = true
	}
	vl.indexDirty = true
}

// EnsureHeights renders all items that need it and updates their heights so
// the height index is fully correct. Unlike RenderVisible which only processes
// visible-range items, this covers the entire list. Called before operations
// that depend on accurate global line indices (e.g. selection).
// Items are rendered if Dirty OR if Rendered is nil (e.g. items created from
// cache that had CachedLines but no Rendered data).
func (vl *itemList) EnsureHeights(renderFn func(idx int, item *renderItem, width int) []string, width int) {
	for i := range vl.items {
		item := &vl.items[i]
		if item.Dirty || item.Rendered == nil {
			rendered := renderFn(i, item, width)
			item.Rendered = rendered
			item.Height = len(rendered)
			item.Dirty = false
		}
	}
	vl.indexDirty = true
}

func (vl *itemList) BuildIndex() {
	if vl.indexDirty {
		vl.index.Rebuild(vl.items)
		vl.indexDirty = false
	}
}

func (vl *itemList) ItemAtLine(globalLine int) (itemIdx, localLine int) {
	vl.BuildIndex()
	return vl.index.ItemAtLine(globalLine)
}

func (vl *itemList) LineOfItem(itemIdx int) int {
	vl.BuildIndex()
	return vl.index.LineOfItem(itemIdx)
}

func (vl *itemList) TotalHeight() int {
	vl.BuildIndex()
	return vl.index.TotalHeight()
}

func (vl *itemList) VisibleRange() (firstItem, lastItem, skipLines int) {
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

func (vl *itemList) RenderRange() (firstItem, lastItem, skipLines int) {
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

func (vl *itemList) ScrollBy(lines int) {
	vl.scrollY += lines
	vl.ClampScroll()
}

func (vl *itemList) ScrollTo(y int) {
	vl.scrollY = y
	vl.ClampScroll()
}

func (vl *itemList) ScrollToBottom() {
	vl.scrollY = vl.MaxScroll()
}

func (vl *itemList) ScrollToItem(idx int) {
	vl.scrollY = vl.LineOfItem(idx)
	vl.ClampScroll()
}

func (vl *itemList) ScrollToGlobalLine(line int) {
	vl.scrollY = line
	vl.ClampScroll()
}

func (vl *itemList) ClampScroll() {
	if vl.viewportH <= 0 {
		vl.scrollY = 0
		return
	}
	vl.scrollY = clamp(vl.scrollY, 0, vl.MaxScroll())
}

func (vl *itemList) MaxScroll() int {
	vl.BuildIndex()
	return max(0, vl.index.TotalHeight()+vl.bottomPadding-vl.viewportH)
}

func (vl *itemList) ScrollY() int {
	return vl.scrollY
}

func (vl *itemList) SetViewport(h int) {
	vl.viewportH = h
	vl.ClampScroll()
}

func (vl *itemList) ViewportHeight() int {
	return vl.viewportH
}

func (vl *itemList) IsAtBottom() bool {
	return vl.scrollY >= vl.MaxScroll()
}

func (vl *itemList) IsAtTop() bool {
	return vl.scrollY == 0
}

func (vl *itemList) BottomPadding() int {
	return vl.bottomPadding
}

func (vl *itemList) SetBottomPadding(n int) {
	vl.bottomPadding = n
}

// RenderVisible renders dirty items within the visible range and returns
// the visible lines. Heights are recalculated during rendering.
func (vl *itemList) RenderVisible(renderFn func(idx int, item *renderItem, width int) []string, width int) []string {
	vl.lastRenderFn = renderFn
	vl.lastRenderW = width
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
func (vl *itemList) ExtractVisible() []string {
	vl.BuildIndex()

	if len(vl.items) == 0 || vl.viewportH <= 0 {
		return make([]string, vl.viewportH)
	}

	firstItem, lastItem, skipLines := vl.VisibleRange()
	return vl.assembleOutput(firstItem, lastItem, skipLines)
}

// ExtractVisibleNative returns PaintLines parallel to ExtractVisible(), using
// the same skip/pad/scroll arithmetic. Missing native rows are marked PreferANSI.
func (vl *itemList) ExtractVisibleNative() []PaintLine {
	vl.BuildIndex()

	if len(vl.items) == 0 || vl.viewportH <= 0 {
		out := make([]PaintLine, vl.viewportH)
		for i := range out {
			out[i] = buildPlainBodyLine("")
		}
		return out
	}

	firstItem, lastItem, skipLines := vl.VisibleRange()
	vl.repairNativeRows(firstItem, lastItem)
	return vl.assembleNativeOutput(firstItem, lastItem, skipLines)
}

func (vl *itemList) repairNativeRows(firstItem, lastItem int) {
	if vl.lastRenderFn == nil {
		return
	}
	for i := firstItem; i <= lastItem && i < len(vl.items); i++ {
		if i < 0 {
			continue
		}
		item := &vl.items[i]
		if len(item.Native) == len(item.Rendered) {
			continue
		}
		rendered := vl.lastRenderFn(i, item, vl.lastRenderW)
		item.Rendered = rendered
		item.Height = len(rendered)
		item.Dirty = false
		vl.indexDirty = true
	}
}

func (vl *itemList) assembleOutput(firstItem, lastItem, skipLines int) []string {
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

func (vl *itemList) assembleNativeOutput(firstItem, lastItem, skipLines int) []PaintLine {
	totalContent := vl.index.TotalHeight()

	var output []PaintLine
	accumulated := 0
	for i := firstItem; i <= lastItem; i++ {
		item := &vl.items[i]
		lines := item.Rendered
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
			if j < len(item.Native) {
				output = append(output, item.Native[j])
			} else {
				// The owning Renderer repairs this invariant on the next render.
				// Preserve ANSI styling for this frame rather than silently
				// converting a rich row to plain ColorFg.
				item.Dirty = true
				output = append(output, PaintLine{ANSIFallback: true})
			}
			accumulated++
			if accumulated >= vl.viewportH {
				goto done
			}
		}
	}
done:
	if accumulated < vl.viewportH {
		needed := vl.viewportH - accumulated
		blank := buildPlainBodyLine("")
		if totalContent > vl.viewportH {
			for i := 0; i < needed; i++ {
				output = append(output, blank)
			}
		} else {
			padding := make([]PaintLine, needed)
			for i := range padding {
				padding[i] = blank
			}
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
