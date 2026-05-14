package virtlist

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func makeItems(heights ...int) []Item {
	items := make([]Item, len(heights))
	for i, h := range heights {
		items[i] = Item{ID: string(rune('A' + i)), Height: h, Dirty: true}
	}
	return items
}

func TestHeightIndex_Rebuild(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)

	if len(hi.prefixSums) != 4 {
		t.Fatalf("expected 4 prefix sums, got %d", len(hi.prefixSums))
	}
	expected := []int{0, 3, 8, 10}
	for i, v := range expected {
		if hi.prefixSums[i] != v {
			t.Errorf("prefixSums[%d] = %d, want %d", i, hi.prefixSums[i], v)
		}
	}
	if hi.total != 10 {
		t.Errorf("total = %d, want 10", hi.total)
	}
}

func TestHeightIndex_ItemAtLine(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)

	cases := []struct {
		line     int
		wantIdx  int
		wantLocal int
	}{
		{0, 0, 0},
		{2, 0, 2},
		{3, 1, 0},
		{7, 1, 4},
		{8, 2, 0},
		{9, 2, 1},
	}
	for _, tc := range cases {
		idx, local := hi.ItemAtLine(tc.line)
		if idx != tc.wantIdx || local != tc.wantLocal {
			t.Errorf("ItemAtLine(%d) = (%d, %d), want (%d, %d)", tc.line, idx, local, tc.wantIdx, tc.wantLocal)
		}
	}
}

func TestHeightIndex_ItemAtLine_Boundary(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)
	idx, local := hi.ItemAtLine(10)
	if idx != 2 || local != 1 {
		t.Errorf("ItemAtLine(total) = (%d, %d), want (2, 1)", idx, local)
	}
}

func TestHeightIndex_ItemAtLine_Negative(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)
	idx, local := hi.ItemAtLine(-5)
	if idx != 0 || local != 0 {
		t.Errorf("ItemAtLine(-5) = (%d, %d), want (0, 0)", idx, local)
	}
}

func TestHeightIndex_ItemAtLine_SingleItem(t *testing.T) {
	items := makeItems(5)
	var hi HeightIndex
	hi.Rebuild(items)
	idx, local := hi.ItemAtLine(3)
	if idx != 0 || local != 3 {
		t.Errorf("ItemAtLine(3) = (%d, %d), want (0, 3)", idx, local)
	}
}

func TestHeightIndex_ItemAtLine_Empty(t *testing.T) {
	var hi HeightIndex
	hi.Rebuild(nil)
	idx, local := hi.ItemAtLine(0)
	if idx != 0 || local != 0 {
		t.Errorf("ItemAtLine(0) on empty = (%d, %d), want (0, 0)", idx, local)
	}
}

func TestHeightIndex_LineOfItem(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)

	cases := []struct {
		idx  int
		want int
	}{
		{0, 0},
		{1, 3},
		{2, 8},
	}
	for _, tc := range cases {
		got := hi.LineOfItem(tc.idx)
		if got != tc.want {
			t.Errorf("LineOfItem(%d) = %d, want %d", tc.idx, got, tc.want)
		}
	}
}

func TestHeightIndex_LineOfItem_OutOfRange(t *testing.T) {
	items := makeItems(3, 5, 2)
	var hi HeightIndex
	hi.Rebuild(items)

	if got := hi.LineOfItem(-1); got != 0 {
		t.Errorf("LineOfItem(-1) = %d, want 0", got)
	}
	if got := hi.LineOfItem(3); got != 8 {
		t.Errorf("LineOfItem(3) = %d, want 8 (clamped to last valid index)", got)
	}
	if got := hi.LineOfItem(100); got != 8 {
		t.Errorf("LineOfItem(100) = %d, want 8 (clamped to last valid index)", got)
	}
}

func TestHeightIndex_HeightMinusOne(t *testing.T) {
	items := []Item{
		{ID: "A", Height: -1, Dirty: true},
		{ID: "B", Height: 3, Dirty: true},
	}
	var hi HeightIndex
	hi.Rebuild(items)
	if hi.total != 4 {
		t.Errorf("total = %d, want 4 (height -1 treated as 1)", hi.total)
	}
	if hi.prefixSums[1] != 1 {
		t.Errorf("prefixSums[1] = %d, want 1", hi.prefixSums[1])
	}
}

func TestNew(t *testing.T) {
	vl := New()
	if vl.overscan != 4 {
		t.Errorf("overscan = %d, want 4", vl.overscan)
	}
	if vl.ScrollY() != 0 {
		t.Errorf("scrollY = %d, want 0", vl.ScrollY())
	}
	if vl.ViewportHeight() != 0 {
		t.Errorf("viewportH = %d, want 0", vl.ViewportHeight())
	}
}

func TestSetItems(t *testing.T) {
	vl := New()
	items := makeItems(3, 5)
	vl.SetItems(items)
	if vl.ItemCount() != 2 {
		t.Errorf("ItemCount = %d, want 2", vl.ItemCount())
	}
	if !vl.indexDirty {
		t.Error("indexDirty should be true after SetItems")
	}
}

func TestAppendItem(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3))
	vl.AppendItem(Item{ID: "X", Height: 5, Dirty: true})
	if vl.ItemCount() != 2 {
		t.Errorf("ItemCount = %d, want 2", vl.ItemCount())
	}
	if !vl.indexDirty {
		t.Error("indexDirty should be true after AppendItem")
	}
}

func TestItemCount(t *testing.T) {
	vl := New()
	if vl.ItemCount() != 0 {
		t.Errorf("ItemCount = %d, want 0", vl.ItemCount())
	}
	vl.SetItems(makeItems(1, 2, 3))
	if vl.ItemCount() != 3 {
		t.Errorf("ItemCount = %d, want 3", vl.ItemCount())
	}
}

func TestItemAccessor(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5))
	if item := vl.Item(0); item == nil || item.Height != 3 {
		t.Errorf("Item(0) = %v, want height 3", item)
	}
	if item := vl.Item(1); item == nil || item.Height != 5 {
		t.Errorf("Item(1) = %v, want height 5", item)
	}
	if item := vl.Item(-1); item != nil {
		t.Errorf("Item(-1) = %v, want nil", item)
	}
	if item := vl.Item(2); item != nil {
		t.Errorf("Item(2) = %v, want nil", item)
	}
}

func TestVisibleRange_Basic(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.BuildIndex()
	first, last, skip := vl.VisibleRange()
	if first != 0 || skip != 0 {
		t.Errorf("first=%d skip=%d, want first=0 skip=0", first, skip)
	}
	if last < 2 {
		t.Errorf("last=%d, want at least 2 (item 2 is partially visible)", last)
	}
}

func TestVisibleRange_WithScroll(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollTo(3)
	first, last, skip := vl.VisibleRange()
	if first != 0 {
		t.Errorf("first=%d, want 0", first)
	}
	if skip != 3 {
		t.Errorf("skip=%d, want 3", skip)
	}
	if last < 2 {
		t.Errorf("last=%d, want at least 2", last)
	}
}

func TestVisibleRange_Overscan(t *testing.T) {
	vl := New()
	items := make([]Item, 10)
	for i := range items {
		items[i] = Item{ID: string(rune('A' + i)), Height: 3, Dirty: true}
	}
	vl.SetItems(items)
	vl.SetViewport(10)
	vl.ScrollTo(15)
	first, last, skip := vl.VisibleRange()
	if skip != 0 {
		t.Errorf("skip=%d, want 0", skip)
	}
	if first > 5 {
		t.Errorf("first=%d, want <=5 (exact visible range)", first)
	}
	if last < 7 {
		t.Errorf("last=%d, want >=7 (exact visible range)", last)
	}

	renderFirst, renderLast, _ := vl.RenderRange()
	if renderFirst > 1 {
		t.Errorf("renderFirst=%d, want <=1 (overscan extends above)", renderFirst)
	}
	if renderLast < 9 {
		t.Errorf("renderLast=%d, want >=9 (overscan extends below)", renderLast)
	}
}

func TestVisibleRange_ContentShorterThanViewport(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 2))
	vl.SetViewport(10)
	first, last, skip := vl.VisibleRange()
	if first != 0 || skip != 0 {
		t.Errorf("first=%d skip=%d, want 0 0", first, skip)
	}
	if last != 1 {
		t.Errorf("last=%d, want 1 (all items visible)", last)
	}
}

func TestVisibleRange_EmptyList(t *testing.T) {
	vl := New()
	vl.SetViewport(10)
	first, last, skip := vl.VisibleRange()
	if first != 0 || last != -1 || skip != 0 {
		t.Errorf("got (%d, %d, %d), want (0, -1, 0)", first, last, skip)
	}
}

func TestVisibleRange_ZeroViewport(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5))
	vl.SetViewport(0)
	first, last, skip := vl.VisibleRange()
	if first != 0 || last != -1 || skip != 0 {
		t.Errorf("got (%d, %d, %d), want (0, -1, 0)", first, last, skip)
	}
}

func TestVisibleRange_LastItemPartial(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 3, 3))
	vl.SetViewport(5)
	vl.ScrollTo(2)
	first, last, skip := vl.VisibleRange()
	if first != 0 {
		t.Errorf("first=%d, want 0", first)
	}
	if skip != 2 {
		t.Errorf("skip=%d, want 2", skip)
	}
	if last != 2 {
		t.Errorf("last=%d, want 2", last)
	}
}

func TestScrollBy_Positive(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollBy(7)
	if vl.ScrollY() != 7 {
		t.Errorf("ScrollY = %d, want 7", vl.ScrollY())
	}
}

func TestScrollBy_Negative(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollTo(10)
	vl.ScrollBy(-3)
	if vl.ScrollY() != 7 {
		t.Errorf("ScrollY = %d, want 7", vl.ScrollY())
	}
}

func TestScrollBy_Clamped(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollBy(-5)
	if vl.ScrollY() != 0 {
		t.Errorf("ScrollY after negative clamp = %d, want 0", vl.ScrollY())
	}
	vl.ScrollBy(1000)
	maxScroll := vl.MaxScroll()
	if vl.ScrollY() != maxScroll {
		t.Errorf("ScrollY after positive clamp = %d, want %d", vl.ScrollY(), maxScroll)
	}
}

func TestScrollToBottom(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollToBottom()
	if vl.ScrollY() != vl.MaxScroll() {
		t.Errorf("ScrollY = %d, want MaxScroll=%d", vl.ScrollY(), vl.MaxScroll())
	}
}

func TestScrollToItem(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollToItem(2)
	expected := vl.LineOfItem(2)
	if vl.ScrollY() != expected {
		t.Errorf("ScrollY = %d, want %d", vl.ScrollY(), expected)
	}
}

func TestIsAtBottom_IsAtTop(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	if !vl.IsAtTop() {
		t.Error("expected IsAtTop() = true at start")
	}
	if vl.IsAtBottom() {
		t.Error("expected IsAtBottom() = false at start")
	}
	vl.ScrollToBottom()
	if vl.IsAtTop() {
		t.Error("expected IsAtTop() = false at bottom")
	}
	if !vl.IsAtBottom() {
		t.Error("expected IsAtBottom() = true at bottom")
	}
	vl.ScrollTo(5)
	if vl.IsAtTop() {
		t.Error("expected IsAtTop() = false in middle")
	}
	if vl.IsAtBottom() {
		t.Error("expected IsAtBottom() = false in middle")
	}
}

func TestClampScroll_OnResize(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 2))
	vl.SetViewport(2)
	vl.ScrollToBottom()
	prev := vl.ScrollY()
	vl.SetViewport(100)
	if vl.ScrollY() != 0 {
		t.Errorf("ScrollY = %d after viewport exceeds content, want 0 (was %d)", vl.ScrollY(), prev)
	}
}

func TestRenderVisible_Basic(t *testing.T) {
	vl := New()
	items := make([]Item, 5)
	for i := range items {
		items[i] = Item{
			ID:     string(rune('A' + i)),
			Height: 5,
			Dirty:  true,
		}
	}
	vl.SetItems(items)
	vl.SetViewport(10)
	rendered := vl.RenderVisible(func(idx int, item *Item, width int) []string {
		lines := make([]string, item.Height)
		for j := range lines {
			lines[j] = string(rune('A'+idx)) + "-" + string(rune('0'+j))
		}
		return lines
	}, 80)
	if len(rendered) != 10 {
		t.Fatalf("len(rendered) = %d, want 10", len(rendered))
	}
}

func TestRenderVisible_LazyRendering(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 5, Dirty: false, Rendered: []string{"a0", "a1", "a2", "a3", "a4"}},
		{ID: "B", Height: 5, Dirty: true},
	}
	vl.SetItems(items)
	vl.SetViewport(10)
	called := 0
	vl.RenderVisible(func(idx int, item *Item, width int) []string {
		called++
		return []string{"b0", "b1", "b2", "b3", "b4"}
	}, 80)
	if called != 1 {
		t.Errorf("renderFn called %d times, want 1 (only dirty item)", called)
	}
}

func TestRenderVisible_RenderFnUpdatesHeight(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 3, Dirty: true},
		{ID: "B", Height: 3, Dirty: true},
	}
	vl.SetItems(items)
	vl.SetViewport(10)
	vl.RenderVisible(func(idx int, item *Item, width int) []string {
		return []string{"x0", "x1", "x2", "x3", "x4"}
	}, 80)
	if vl.items[0].Height != 5 {
		t.Errorf("item 0 height = %d, want 5 (updated by renderFn)", vl.items[0].Height)
	}
	if vl.items[1].Height != 5 {
		t.Errorf("item 1 height = %d, want 5 (updated by renderFn)", vl.items[1].Height)
	}
	vl.BuildIndex()
	if vl.TotalHeight() != 10 {
		t.Errorf("TotalHeight = %d, want 10 (after height update)", vl.TotalHeight())
	}
}

func TestRenderVisible_Padding(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 2))
	vl.SetViewport(10)
	rendered := vl.RenderVisible(func(idx int, item *Item, width int) []string {
		lines := make([]string, item.Height)
		return lines
	}, 80)
	if len(rendered) != 10 {
		t.Fatalf("len(rendered) = %d, want 10", len(rendered))
	}
	emptyCount := 0
	for _, line := range rendered {
		if line == "" {
			emptyCount++
		}
	}
	if emptyCount < 5 {
		t.Errorf("expected padding lines, got %d empty out of %d", emptyCount, len(rendered))
	}
}

func TestRenderVisible_SkipLines(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 5, Dirty: true},
		{ID: "B", Height: 5, Dirty: true},
		{ID: "C", Height: 5, Dirty: true},
	}
	vl.SetItems(items)
	vl.SetViewport(6)
	vl.ScrollTo(3)
	rendered := vl.RenderVisible(func(idx int, item *Item, width int) []string {
		lines := make([]string, item.Height)
		for j := range lines {
			lines[j] = string(rune('A'+idx)) + "-" + string(rune('0'+j))
		}
		return lines
	}, 80)
	if len(rendered) != 6 {
		t.Fatalf("len(rendered) = %d, want 6", len(rendered))
	}
	if rendered[0] != "A-3" {
		t.Errorf("rendered[0] = %q, want \"A-3\" (skipLines=3)", rendered[0])
	}
	if rendered[1] != "A-4" {
		t.Errorf("rendered[1] = %q, want \"A-4\"", rendered[1])
	}
	if !strings.HasPrefix(rendered[2], "B-") {
		t.Errorf("rendered[2] = %q, want start with B-", rendered[2])
	}
}

func TestRenderVisible_WidthPassed(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3))
	vl.SetViewport(10)
	var receivedWidth int
	vl.RenderVisible(func(idx int, item *Item, width int) []string {
		receivedWidth = width
		lines := make([]string, item.Height)
		return lines
	}, 42)
	if receivedWidth != 42 {
		t.Errorf("renderFn received width = %d, want 42", receivedWidth)
	}
}

func TestRenderVisible_NoItems(t *testing.T) {
	vl := New()
	vl.SetViewport(5)
	rendered := vl.RenderVisible(func(idx int, item *Item, width int) []string {
		return nil
	}, 80)
	if len(rendered) != 5 {
		t.Fatalf("len(rendered) = %d, want 5", len(rendered))
	}
	for i, line := range rendered {
		if line != "" {
			t.Errorf("rendered[%d] = %q, want empty string", i, line)
		}
	}
}

func TestScrollState_OnWheelUpAcceleration(t *testing.T) {
	s := NewScrollState()
	first := s.OnWheelUp()
	if first != 1 {
		t.Errorf("first OnWheelUp = %d, want 1", first)
	}
	second := s.OnWheelUp()
	if second < 1 {
		t.Errorf("second OnWheelUp = %d, want >=1 (accelerated)", second)
	}
	if s.Velocity <= 1.0 {
		t.Errorf("velocity = %v, want > 1.0 after rapid events", s.Velocity)
	}
}

func TestScrollState_OnWheelDownAcceleration(t *testing.T) {
	s := NewScrollState()
	first := s.OnWheelDown()
	if first != 1 {
		t.Errorf("first OnWheelDown = %d, want 1", first)
	}
	second := s.OnWheelDown()
	if second < 1 {
		t.Errorf("second OnWheelDown = %d, want >=1 (accelerated)", second)
	}
	if s.Velocity <= 1.0 {
		t.Errorf("velocity = %v, want > 1.0 after rapid events", s.Velocity)
	}
}

func TestScrollState_VelocityReset(t *testing.T) {
	s := NewScrollState()
	s.OnWheelUp()
	s.Velocity = 4.0
	s.LastWheelTime = time.Now().Add(-200 * time.Millisecond)
	got := s.OnWheelUp()
	if got != 1 {
		t.Errorf("OnWheelUp after pause = %d, want 1 (velocity reset)", got)
	}
	if s.Velocity != 1.0 {
		t.Errorf("velocity = %v, want 1.0 after reset", s.Velocity)
	}
}

func TestScrollState_AutoScroll(t *testing.T) {
	s := NewScrollState()
	if !s.AutoScroll {
		t.Error("AutoScroll should start true")
	}
	if !s.ShouldAutoScroll(100, 100) {
		t.Error("ShouldAutoScroll at bottom (scrollY==maxScroll) should be true")
	}
	if s.ShouldAutoScroll(50, 100) {
		t.Error("ShouldAutoScroll not at bottom should be false")
	}
	if s.AutoScroll {
		t.Error("AutoScroll should be false after scrolling up")
	}
	if !s.ShouldAutoScroll(100, 100) {
		t.Error("ShouldAutoScroll at bottom should re-enable auto scroll")
	}
	if !s.AutoScroll {
		t.Error("AutoScroll should be true after re-enabling at bottom")
	}
	if !s.ShouldAutoScroll(97, 100) {
		t.Error("ShouldAutoScroll near bottom (within 3 lines) should be true")
	}
}

func TestEstimatePlainTextHeight(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  int
	}{
		{"hello", 10, 1},
		{"hello world", 5, 2},
		{"hello\nworld", 10, 2},
		{"", 10, 1},
		{"hi", 0, 1},
	}
	for _, tc := range cases {
		got := EstimatePlainTextHeight(tc.text, tc.width)
		if got != tc.want {
			t.Errorf("EstimatePlainTextHeight(%q, %d) = %d, want %d", tc.text, tc.width, got, tc.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  int
	}{
		{"hello world", 5, 2},
		{"hello\nworld", 10, 2},
		{"", 10, 1},
		{"short", 10, 1},
		{"a b c d e", 3, 3},
	}
	for _, tc := range cases {
		got := WrapText(tc.text, tc.width)
		if len(got) != tc.want {
			t.Errorf("WrapText(%q, %d) returned %d lines, want %d: %v", tc.text, tc.width, len(got), tc.want, got)
		}
	}
	wideWrapped := WrapText("你好 世界", 4)
	if len(wideWrapped) < 2 {
		t.Errorf("WrapText with wide chars returned %d lines, want >=2: %v", len(wideWrapped), wideWrapped)
	}
}

func TestStreamingHeightEstimate(t *testing.T) {
	got := StreamingHeightEstimate([]string{"a", "b"}, "c", 10)
	if got != 3 {
		t.Errorf("StreamingHeightEstimate = %d, want 3", got)
	}
	got = StreamingHeightEstimate(nil, "", 10)
	if got != 1 {
		t.Errorf("StreamingHeightEstimate empty = %d, want 1", got)
	}
	got = StreamingHeightEstimate([]string{"x"}, "hello world", 5)
	if got < 2 {
		t.Errorf("StreamingHeightEstimate with wrap = %d, want >=2", got)
	}
}

func TestUpdateItemAt(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5))
	vl.UpdateItemAt(1, Item{ID: "B2", Height: 7, Dirty: true})
	if vl.Item(1).Height != 7 {
		t.Errorf("item 1 height = %d, want 7", vl.Item(1).Height)
	}
	if !vl.indexDirty {
		t.Error("indexDirty should be true after UpdateItemAt")
	}
	vl.UpdateItemAt(-1, Item{ID: "X", Height: 1, Dirty: true})
	vl.UpdateItemAt(99, Item{ID: "X", Height: 1, Dirty: true})
}

func TestInvalidateItem(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 3, Dirty: false},
		{ID: "B", Height: 5, Dirty: false},
	}
	vl.SetItems(items)
	vl.InvalidateItem("B")
	if vl.items[0].Dirty {
		t.Error("item A should not be dirty")
	}
	if !vl.items[1].Dirty {
		t.Error("item B should be dirty after InvalidateItem")
	}
}

func TestInvalidateAll(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 3, Dirty: false},
		{ID: "B", Height: 5, Dirty: false},
	}
	vl.SetItems(items)
	vl.InvalidateAll()
	if !vl.items[0].Dirty || !vl.items[1].Dirty {
		t.Error("all items should be dirty after InvalidateAll")
	}
}

func TestInvalidateHeights(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5))
	vl.InvalidateHeights()
	if !vl.items[0].Dirty || !vl.items[1].Dirty {
		t.Error("all items should be dirty after InvalidateHeights")
	}
	if !vl.indexDirty {
		t.Error("indexDirty should be true after InvalidateHeights")
	}
}

func TestMaxScroll(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	want := 25 + vl.BottomPadding() - 10
	if vl.MaxScroll() != want {
		t.Errorf("MaxScroll = %d, want %d", vl.MaxScroll(), want)
	}
}

func TestScrollToGlobalLine(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(5, 5, 5, 5, 5))
	vl.SetViewport(10)
	vl.ScrollToGlobalLine(5)
	if vl.ScrollY() != 5 {
		t.Errorf("ScrollY = %d, want 5", vl.ScrollY())
	}
	vl.ScrollToGlobalLine(-10)
	if vl.ScrollY() != 0 {
		t.Errorf("ScrollY = %d after negative, want 0", vl.ScrollY())
	}
	vl.ScrollToGlobalLine(1000)
	if vl.ScrollY() != vl.MaxScroll() {
		t.Errorf("ScrollY = %d after overflow, want MaxScroll=%d", vl.ScrollY(), vl.MaxScroll())
	}
}

func TestHeightIndex_Len(t *testing.T) {
	var hi HeightIndex
	hi.Rebuild(makeItems(3, 5, 2))
	if hi.Len() != 3 {
		t.Errorf("Len = %d, want 3", hi.Len())
	}
	var hiEmpty HeightIndex
	hiEmpty.Rebuild(nil)
	if hiEmpty.Len() != 0 {
		t.Errorf("Len on empty = %d, want 0", hiEmpty.Len())
	}
}

func TestItemsAccessor(t *testing.T) {
	vl := New()
	items := makeItems(3, 5)
	vl.SetItems(items)
	got := vl.Items()
	if len(got) != 2 {
		t.Errorf("len(Items()) = %d, want 2", len(got))
	}
}

func TestTotalHeight(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5, 2))
	if vl.TotalHeight() != 10 {
		t.Errorf("TotalHeight = %d, want 10", vl.TotalHeight())
	}
}

func TestItemAtLine_VirtualList(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5, 2))
	idx, local := vl.ItemAtLine(3)
	if idx != 1 || local != 0 {
		t.Errorf("ItemAtLine(3) = (%d, %d), want (1, 0)", idx, local)
	}
}

func TestLineOfItem_VirtualList(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5, 2))
	if vl.LineOfItem(1) != 3 {
		t.Errorf("LineOfItem(1) = %d, want 3", vl.LineOfItem(1))
	}
}

func TestBuildIndex_Idempotent(t *testing.T) {
	vl := New()
	vl.SetItems(makeItems(3, 5, 2))
	vl.BuildIndex()
	if vl.indexDirty {
		t.Error("indexDirty should be false after BuildIndex")
	}
	vl.BuildIndex()
	if vl.TotalHeight() != 10 {
		t.Errorf("TotalHeight = %d, want 10 after second BuildIndex", vl.TotalHeight())
	}
}

func TestScrollState_VelocityCap(t *testing.T) {
	s := NewScrollState()
	for i := 0; i < 20; i++ {
		s.OnWheelDown()
	}
	if s.Velocity > 5.0 {
		t.Errorf("velocity = %v, want <= 5.0 (capped)", s.Velocity)
	}
}

func TestScrollState_SetAutoScroll(t *testing.T) {
	s := NewScrollState()
	s.SetAutoScroll(false)
	if s.AutoScroll {
		t.Error("AutoScroll should be false after SetAutoScroll(false)")
	}
	s.SetAutoScroll(true)
	if !s.AutoScroll {
		t.Error("AutoScroll should be true after SetAutoScroll(true)")
	}
}

func TestScrollState_OnContentInserted(t *testing.T) {
	s := NewScrollState()
	s.AutoScroll = true
	if n := s.OnContentInserted(true, true, 10); n != 0 {
		t.Errorf("auto-scroll on, content above: got %d, want 0", n)
	}
	s.AutoScroll = false
	if n := s.OnContentInserted(true, true, 10); n != 10 {
		t.Errorf("auto-scroll off, content above viewport: got %d, want 10", n)
	}
	if n := s.OnContentInserted(true, false, 10); n != 0 {
		t.Errorf("auto-scroll off, content below viewport: got %d, want 0", n)
	}
}

func TestEstimatePlainTextHeight_WideChars(t *testing.T) {
	got := EstimatePlainTextHeight("你好世界", 4)
	if got < 1 {
		t.Errorf("EstimatePlainTextHeight with wide chars = %d, want >=1", got)
	}
}

func TestWrapText_WideChars(t *testing.T) {
	got := WrapText("a 你好 b", 6)
	if len(got) < 1 {
		t.Errorf("WrapText with wide chars returned %d lines", len(got))
	}
}

func TestWrapText_ZeroWidth(t *testing.T) {
	got := WrapText("hello", 0)
	if len(got) != 1 {
		t.Errorf("WrapText with width 0 returned %d lines, want 1", len(got))
	}
}

func TestEstimateMarkdownHeight(t *testing.T) {
	if got := EstimateMarkdownHeight(""); got != 1 {
		t.Errorf("EstimateMarkdownHeight('') = %d, want 1", got)
	}
	if got := EstimateMarkdownHeight("line1\nline2"); got != 2 {
		t.Errorf("EstimateMarkdownHeight = %d, want 2", got)
	}
	if got := EstimateMarkdownHeight("single"); got != 1 {
		t.Errorf("EstimateMarkdownHeight('single') = %d, want 1", got)
	}
}

func TestVisibleRange_LongItemScrollToBottom(t *testing.T) {
	vl := New()
	items := []Item{
		{ID: "A", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "B", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "C", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "D", Height: 200, Dirty: false, Rendered: make([]string, 200)},
	}
	vl.SetItems(items)
	vl.SetViewport(40)
	vl.ScrollToBottom()

	first, last, skip := vl.VisibleRange()
	if first != 3 {
		t.Errorf("first=%d, want 3 (long item at bottom)", first)
	}
	if skip != 160 {
		t.Errorf("skip=%d, want 160 (scroll into long item)", skip)
	}

	output := vl.assembleOutput(first, last, skip)
	if len(output) != 40 {
		t.Fatalf("len(output) = %d, want 40", len(output))
	}

	renderFirst, _, _ := vl.RenderRange()
	if renderFirst > 0 {
		t.Errorf("renderFirst=%d, want 0 (overscan extends to start)", renderFirst)
	}
}

func TestAssembleOutput_OverscanBug(t *testing.T) {
	vl := New()
	dLines := make([]string, 200)
	for i := range dLines {
		dLines[i] = fmt.Sprintf("D-line-%d", i)
	}
	items := []Item{
		{ID: "A", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "B", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "C", Height: 10, Dirty: false, Rendered: make([]string, 10)},
		{ID: "D", Height: 200, Dirty: false, Rendered: dLines},
	}
	vl.SetItems(items)
	vl.SetViewport(40)
	vl.ScrollToBottom()

	first, last, skip := vl.VisibleRange()
	output := vl.assembleOutput(first, last, skip)
	if len(output) != 40 {
		t.Fatalf("len(output) = %d, want 40", len(output))
	}
	if output[0] != "D-line-160" {
		t.Errorf("output[0] = %q, want \"D-line-160\" (should start deep in long item, not from beginning)", output[0])
	}
	if output[39] != "D-line-199" {
		t.Errorf("output[39] = %q, want \"D-line-199\" (should end at last line of content)", output[39])
	}
}
