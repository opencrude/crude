package wm

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/widget"
)

type testWidget struct {
	widget.BaseWidget
}

func (t testWidget) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	for y := 0; y < surf.Height(); y++ {
		for x := 0; x < surf.Width(); x++ {
			surf.Buffer().Set(x, y, cell.NewCell('X', cell.NoColor, cell.NoColor, 0))
		}
	}
}

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func testContext() *widget.RenderContext {
	return &widget.RenderContext{
		Theme:     testTheme(),
		ClipStack: surface.NewClipStack(),
		Opacity:   1.0,
		Focused:   false,
		SpinFrame: 0,
	}
}

func TestNewFloatingWindowDefaults(t *testing.T) {
	fw := NewFloatingWindow("test", "Hello", testWidget{}, 20, 10)

	if fw.ID != "test" {
		t.Errorf("ID = %q, want %q", fw.ID, "test")
	}
	if fw.Title != "Hello" {
		t.Errorf("Title = %q, want %q", fw.Title, "Hello")
	}
	if !fw.decorated {
		t.Error("decorated should be true by default")
	}
	if !fw.movable {
		t.Error("movable should be true by default")
	}
	if !fw.resizable {
		t.Error("resizable should be true by default")
	}
	if !fw.closable {
		t.Error("closable should be true by default")
	}
	if !fw.focusable {
		t.Error("focusable should be true by default")
	}
	if !fw.Visible() {
		t.Error("visible should be true by default")
	}
	if fw.State() != WindowNormal {
		t.Errorf("state = %v, want WindowNormal", fw.State())
	}
	if fw.Focused() {
		t.Error("should not be focused by default")
	}
	if fw.borderStyle != BorderLShape {
		t.Errorf("borderStyle = %v, want BorderLShape", fw.borderStyle)
	}
	if fw.titleAlign != AlignLeft {
		t.Errorf("titleAlign = %v, want AlignLeft", fw.titleAlign)
	}
	x, y := fw.Position()
	if x != 0 || y != 0 {
		t.Errorf("position = (%d,%d), want (0,0)", x, y)
	}
	w, h := fw.Size()
	if w != 20 || h != 10 {
		t.Errorf("size = (%d,%d), want (20,10)", w, h)
	}
}

func TestNewFloatingWindowOptions(t *testing.T) {
	fw := NewFloatingWindow("opts", "Opts", testWidget{}, 30, 15,
		WithDecoration(false),
		WithMovable(false),
		WithResizable(false),
		WithClosable(false),
		WithModal(true),
		WithFocusable(false),
		WithBorderStyle(BorderRounded),
		WithMinSize(10, 5),
		WithMaxSize(100, 50),
		WithTitleAlign(AlignCenter),
	)

	if fw.decorated {
		t.Error("decorated should be false")
	}
	if fw.movable {
		t.Error("movable should be false")
	}
	if fw.resizable {
		t.Error("resizable should be false")
	}
	if fw.closable {
		t.Error("closable should be false")
	}
	if !fw.modal {
		t.Error("modal should be true")
	}
	if fw.focusable {
		t.Error("focusable should be false")
	}
	if fw.borderStyle != BorderRounded {
		t.Errorf("borderStyle = %v, want BorderRounded", fw.borderStyle)
	}
	if fw.titleAlign != AlignCenter {
		t.Errorf("titleAlign = %v, want AlignCenter", fw.titleAlign)
	}
	if fw.minW != 10 || fw.minH != 5 {
		t.Errorf("minSize = (%d,%d), want (10,5)", fw.minW, fw.minH)
	}
	if fw.maxW != 100 || fw.maxH != 50 {
		t.Errorf("maxSize = (%d,%d), want (100,50)", fw.maxW, fw.maxH)
	}
}

func TestSetPosition(t *testing.T) {
	fw := NewFloatingWindow("pos", "Pos", testWidget{}, 20, 10)
	fw.SetPosition(15, 25)
	x, y := fw.Position()
	if x != 15 || y != 25 {
		t.Errorf("position = (%d,%d), want (15,25)", x, y)
	}
}

func TestSetSizeClampsToMin(t *testing.T) {
	fw := NewFloatingWindow("min", "Min", testWidget{}, 1, 1)
	w, h := fw.Size()
	if w < 5 || h < 3 {
		t.Errorf("decorated min size = (%d,%d), want at least (5,3)", w, h)
	}
}

func TestSetSizeClampsToMinUndecorated(t *testing.T) {
	fw := NewFloatingWindow("min-undec", "Min", testWidget{}, 0, 0,
		WithDecoration(false))
	w, h := fw.Size()
	if w < 1 || h < 1 {
		t.Errorf("undecorated min size = (%d,%d), want at least (1,1)", w, h)
	}
}

func TestSetSizeClampsToMax(t *testing.T) {
	fw := NewFloatingWindow("max", "Max", testWidget{}, 200, 200,
		WithMaxSize(50, 30))
	w, h := fw.Size()
	if w > 50 || h > 30 {
		t.Errorf("max size = (%d,%d), want at most (50,30)", w, h)
	}
}

func TestComputeContentAreaBorderSingle(t *testing.T) {
	fw := NewFloatingWindow("ca-bs", "CA", testWidget{}, 20, 10,
		WithBorderStyle(BorderSingle))
	b := fw.ContentBounds()
	if b.X != 1 || b.Y != 1 || b.W != 18 || b.H != 8 {
		t.Errorf("BorderSingle content = (%d,%d,%d,%d), want (1,1,18,8)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestComputeContentAreaBorderRounded(t *testing.T) {
	fw := NewFloatingWindow("ca-br", "CA", testWidget{}, 20, 10,
		WithBorderStyle(BorderRounded))
	b := fw.ContentBounds()
	if b.X != 1 || b.Y != 1 || b.W != 18 || b.H != 8 {
		t.Errorf("BorderRounded content = (%d,%d,%d,%d), want (1,1,18,8)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestComputeContentAreaBorderNone(t *testing.T) {
	fw := NewFloatingWindow("ca-bn", "CA", testWidget{}, 20, 10,
		WithBorderStyle(BorderNone))
	b := fw.ContentBounds()
	if b.X != 0 || b.Y != 1 || b.W != 20 || b.H != 9 {
		t.Errorf("BorderNone content = (%d,%d,%d,%d), want (0,1,20,9)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestComputeContentAreaBorderLShape(t *testing.T) {
	fw := NewFloatingWindow("ca-bl", "CA", testWidget{}, 20, 10,
		WithBorderStyle(BorderLShape))
	b := fw.ContentBounds()
	if b.X != 1 || b.Y != 1 || b.W != 19 || b.H != 8 {
		t.Errorf("BorderLShape content = (%d,%d,%d,%d), want (1,1,19,8)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestComputeContentAreaUndecorated(t *testing.T) {
	fw := NewFloatingWindow("ca-undec", "CA", testWidget{}, 20, 10,
		WithDecoration(false))
	b := fw.ContentBounds()
	if b.X != 0 || b.Y != 0 || b.W != 20 || b.H != 10 {
		t.Errorf("Undecorated content = (%d,%d,%d,%d), want (0,0,20,10)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestComputeContentAreaMinSize1x1(t *testing.T) {
	fw := NewFloatingWindow("ca-min", "", nil, 3, 3)
	b := fw.ContentBounds()
	if b.W < 1 || b.H < 1 {
		t.Errorf("content size = (%d,%d), want at least (1,1)", b.W, b.H)
	}
}

func TestContainsPoint(t *testing.T) {
	fw := NewFloatingWindow("hit", "Hit", testWidget{}, 10, 5,
		WithBorderStyle(BorderSingle))
	fw.SetPosition(5, 5)

	tests := []struct {
		name     string
		x, y     int
		want     bool
	}{
		{"top-left corner", 5, 5, true},
		{"top-right corner", 14, 5, true},
		{"bottom-right corner", 14, 9, true},
		{"center", 10, 7, true},
		{"left of window", 4, 7, false},
		{"above window", 10, 4, false},
		{"right of window", 15, 7, false},
		{"below window", 10, 10, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.ContainsPoint(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("ContainsPoint(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestHitTestEdgeBorderSingle(t *testing.T) {
	fw := NewFloatingWindow("hte", "HTE", testWidget{}, 10, 6,
		WithBorderStyle(BorderSingle))
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want Edge
	}{
		{"top-left corner", 0, 0, EdgeTopLeft},
		{"top-right corner", 9, 0, EdgeTopRight},
		{"bottom-left corner", 0, 5, EdgeBottomLeft},
		{"bottom-right corner", 9, 5, EdgeBottomRight},
		{"top edge", 5, 0, EdgeTop},
		{"bottom edge", 5, 5, EdgeBottom},
		{"left edge", 0, 3, EdgeLeft},
		{"right edge", 9, 3, EdgeRight},
		{"center (not on edge)", 5, 3, EdgeNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.HitTestEdge(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("HitTestEdge(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestHitTestEdgeBorderNone(t *testing.T) {
	fw := NewFloatingWindow("hte-bn", "HTE", testWidget{}, 10, 6,
		WithBorderStyle(BorderNone))
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want Edge
	}{
		{"top row (not edge)", 5, 0, EdgeNone},
		{"bottom edge", 5, 5, EdgeBottom},
		{"bottom-left corner", 0, 5, EdgeBottomLeft},
		{"bottom-right corner", 9, 5, EdgeBottomRight},
		{"left edge", 0, 3, EdgeLeft},
		{"right edge", 9, 3, EdgeRight},
		{"center", 5, 3, EdgeNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.HitTestEdge(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("HitTestEdge(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestHitTestEdgeBorderLShape(t *testing.T) {
	fw := NewFloatingWindow("hte-bl", "HTE", testWidget{}, 10, 6,
		WithBorderStyle(BorderLShape))
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want Edge
	}{
		{"top row (not edge)", 5, 0, EdgeNone},
		{"bottom edge", 5, 5, EdgeBottom},
		{"bottom-left corner", 0, 5, EdgeBottomLeft},
		{"bottom-right corner", 9, 5, EdgeBottomRight},
		{"left edge", 0, 3, EdgeLeft},
		{"right edge (no border)", 9, 3, EdgeNone},
		{"center", 5, 3, EdgeNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.HitTestEdge(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("HitTestEdge(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestHitTestEdgeUndecorated(t *testing.T) {
	fw := NewFloatingWindow("hte-undec", "HTE", testWidget{}, 10, 6,
		WithDecoration(false))
	fw.SetPosition(0, 0)

	got := fw.HitTestEdge(0, 0)
	if got != EdgeNone {
		t.Errorf("undecorated window edge = %v, want EdgeNone", got)
	}
}

func TestIsOnTitleBarBorderSingle(t *testing.T) {
	fw := NewFloatingWindow("tb-s", "TB", testWidget{}, 10, 6,
		WithBorderStyle(BorderSingle))
	fw.SetPosition(5, 5)

	tests := []struct {
		name string
		x, y int
		want bool
	}{
		{"title bar between borders", 6, 5, true},
		{"left border (not title)", 5, 5, false},
		{"right border (not title)", 14, 5, false},
		{"below title bar", 7, 6, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.IsOnTitleBar(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("IsOnTitleBar(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestIsOnTitleBarBorderNone(t *testing.T) {
	fw := NewFloatingWindow("tb-bn", "TB", testWidget{}, 10, 6,
		WithBorderStyle(BorderNone))
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want bool
	}{
		{"title bar full width", 0, 0, true},
		{"title bar right edge", 9, 0, true},
		{"title bar middle", 5, 0, true},
		{"below title bar", 5, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.IsOnTitleBar(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("IsOnTitleBar(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestIsOnTitleBarUndecorated(t *testing.T) {
	fw := NewFloatingWindow("tb-undec", "TB", testWidget{}, 10, 6,
		WithDecoration(false))
	got := fw.IsOnTitleBar(5, 0)
	if got {
		t.Error("undecorated window should not have a title bar")
	}
}

func TestIsOnCloseButtonBorderSingle(t *testing.T) {
	fw := NewFloatingWindow("cb-s", "CB", testWidget{}, 10, 6,
		WithBorderStyle(BorderSingle))
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want bool
	}{
		{"close button (w-4)", 6, 0, true},
		{"close button (w-3)", 7, 0, true},
		{"close button (w-2)", 8, 0, true},
		{"left border", 0, 0, false},
		{"right border", 9, 0, false},
		{"below title bar", 7, 1, false},
		{"too far left", 5, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.IsOnCloseButton(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("IsOnCloseButton(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestIsOnCloseButtonBorderNone(t *testing.T) {
	fw := NewFloatingWindow("cb-bn", "CB", testWidget{}, 10, 6)
	fw.SetPosition(0, 0)

	tests := []struct {
		name string
		x, y int
		want bool
	}{
		{"close button (w-3)", 7, 0, true},
		{"close button (w-2)", 8, 0, true},
		{"close button (w-1)", 9, 0, true},
		{"too far left", 6, 0, false},
		{"below title bar", 8, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fw.IsOnCloseButton(tc.x, tc.y)
			if got != tc.want {
				t.Errorf("IsOnCloseButton(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestIsOnCloseButtonNotClosable(t *testing.T) {
	fw := NewFloatingWindow("cb-nc", "CB", testWidget{}, 10, 6,
		WithClosable(false))
	fw.SetPosition(0, 0)

	if fw.IsOnCloseButton(7, 0) {
		t.Error("non-closable window should not detect close button")
	}
}

func TestApplyResizeRight(t *testing.T) {
	fw := NewFloatingWindow("resize-r", "Resize", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyResize(EdgeRight, 5, 0)
	w, _ := fw.Size()
	if w != 25 {
		t.Errorf("width after right resize = %d, want 25", w)
	}
}

func TestApplyResizeBottom(t *testing.T) {
	fw := NewFloatingWindow("resize-b", "Resize", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyResize(EdgeBottom, 0, 3)
	_, h := fw.Size()
	if h != 13 {
		t.Errorf("height after bottom resize = %d, want 13", h)
	}
}

func TestApplyResizeLeft(t *testing.T) {
	fw := NewFloatingWindow("resize-l", "Resize", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyResize(EdgeLeft, 3, 0)
	w, _ := fw.Size()
	x, _ := fw.Position()
	if w != 17 {
		t.Errorf("width after left resize = %d, want 17", w)
	}
	if x != 13 {
		t.Errorf("x after left resize = %d, want 13", x)
	}
}

func TestApplyResizeTop(t *testing.T) {
	fw := NewFloatingWindow("resize-t", "Resize", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyResize(EdgeTop, 0, 2)
	_, h := fw.Size()
	_, y := fw.Position()
	if h != 8 {
		t.Errorf("height after top resize = %d, want 8", h)
	}
	if y != 12 {
		t.Errorf("y after top resize = %d, want 12", y)
	}
}

func TestApplyResizeCorner(t *testing.T) {
	fw := NewFloatingWindow("resize-c", "Resize", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyResize(EdgeBottomRight, 4, 2)
	w, h := fw.Size()
	if w != 24 || h != 12 {
		t.Errorf("size after corner resize = (%d,%d), want (24,12)", w, h)
	}
}

func TestApplyResizeClampsToMin(t *testing.T) {
	fw := NewFloatingWindow("resize-min", "Resize", testWidget{}, 20, 10)
	fw.ApplyResize(EdgeLeft, 30, 0)
	w, _ := fw.Size()
	if w < 5 {
		t.Errorf("width clamped to %d, want at least 5", w)
	}
}

func TestApplyMove(t *testing.T) {
	fw := NewFloatingWindow("move", "Move", testWidget{}, 20, 10)
	fw.SetPosition(10, 10)
	fw.ApplyMove(5, 3)
	x, y := fw.Position()
	if x != 15 || y != 13 {
		t.Errorf("position after move = (%d,%d), want (15,13)", x, y)
	}
}

func TestFocusUnfocus(t *testing.T) {
	fw := NewFloatingWindow("fu", "FU", testWidget{}, 20, 10)
	if fw.Focused() {
		t.Error("should not be focused initially")
	}
	fw.Focus()
	if !fw.Focused() {
		t.Error("should be focused after Focus()")
	}
	if !fw.IsContentDirty() {
		t.Error("should be dirty after focus")
	}
	fw.Unfocus()
	if fw.Focused() {
		t.Error("should not be focused after Unfocus()")
	}
	if !fw.IsContentDirty() {
		t.Error("should be dirty after unfocus")
	}
}

func TestRenderCreatesSurface(t *testing.T) {
	fw := NewFloatingWindow("render", "Render", testWidget{}, 10, 5)
	ctx := testContext()
	fw.Render(ctx)

	surf := fw.Surface()
	if surf == nil {
		t.Fatal("Surface() returned nil")
	}
	if surf.Width() != 10 {
		t.Errorf("surface width = %d, want 10", surf.Width())
	}
	if surf.Height() != 5 {
		t.Errorf("surface height = %d, want 5", surf.Height())
	}
}

func TestRenderFillsContentArea(t *testing.T) {
	fw := NewFloatingWindow("render-fill", "RF", testWidget{}, 10, 5)
	ctx := testContext()
	fw.Render(ctx)

	// Content is rendered into a child surface. Check the content child surface directly.
	children := fw.Surface().Children()
	if len(children) == 0 {
		t.Fatal("no child surfaces found")
	}
	contentChild := children[len(children)-1] // last child = content surface
	buf := contentChild.Buffer()
	c := buf.Get(0, 0)
	if c.Char != 'X' {
		t.Errorf("content cell char = %q, want 'X'", c.Char)
	}
}

func TestRenderUndecorated(t *testing.T) {
	fw := NewFloatingWindow("render-undec", "RU", testWidget{}, 10, 5,
		WithDecoration(false))
	ctx := testContext()
	fw.Render(ctx)

	children := fw.Surface().Children()
	if len(children) == 0 {
		t.Fatal("no child surfaces found")
	}
	contentChild := children[len(children)-1]
	buf := contentChild.Buffer()
	for y := 0; y < contentChild.Height(); y++ {
		for x := 0; x < contentChild.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 'X' {
				t.Errorf("undecorated cell (%d,%d) char = %q, want 'X'", x, y, c.Char)
			}
		}
	}
}

func TestRenderIdempotent(t *testing.T) {
	fw := NewFloatingWindow("render-idem", "RI", testWidget{}, 10, 5)
	ctx := testContext()
	fw.Render(ctx)
	surf1 := fw.Surface()
	fw.Render(ctx)
	surf2 := fw.Surface()
	if surf1 != surf2 {
		t.Error("Surface should return the same instance across renders")
	}
}

func TestMarkContentDirty(t *testing.T) {
	fw := NewFloatingWindow("dirty", "Dirty", testWidget{}, 10, 5)
	fw.Render(testContext())
	if fw.IsContentDirty() {
		t.Error("should not be dirty after render")
	}
	fw.MarkContentDirty()
	if !fw.IsContentDirty() {
		t.Error("should be dirty after MarkContentDirty()")
	}
}

func TestSetDragging(t *testing.T) {
	fw := NewFloatingWindow("drag", "Drag", testWidget{}, 10, 5)
	if fw.IsDragging() {
		t.Error("should not be dragging initially")
	}
	fw.SetDragging(true)
	if !fw.IsDragging() {
		t.Error("should be dragging after SetDragging(true)")
	}
	fw.SetDragging(false)
	if fw.IsDragging() {
		t.Error("should not be dragging after SetDragging(false)")
	}
}

func TestSetDragStart(t *testing.T) {
	fw := NewFloatingWindow("drag-start", "Drag", testWidget{}, 10, 5)
	fw.SetPosition(10, 10)
	fw.SetDragStart(15, 13)
	offX, offY := fw.DragOffset()
	if offX != 5 || offY != 3 {
		t.Errorf("drag offset = (%d,%d), want (5,3)", offX, offY)
	}
}

func TestSetResizing(t *testing.T) {
	fw := NewFloatingWindow("resize-set", "Resize", testWidget{}, 20, 10)
	fw.SetResizing(true, EdgeRight)
	if !fw.IsResizing() {
		t.Error("should be resizing")
	}
	if fw.ResizeEdge() != EdgeRight {
		t.Errorf("resize edge = %v, want EdgeRight", fw.ResizeEdge())
	}
	sw, sh := fw.ResizeStartSize()
	if sw != 20 || sh != 10 {
		t.Errorf("resize start size = (%d,%d), want (20,10)", sw, sh)
	}
}

func TestSetVisible(t *testing.T) {
	fw := NewFloatingWindow("vis", "Vis", testWidget{}, 10, 5)
	if !fw.Visible() {
		t.Error("should be visible initially")
	}
	fw.SetVisible(false)
	if fw.Visible() {
		t.Error("should not be visible after SetVisible(false)")
	}
	fw.SetVisible(true)
	if !fw.Visible() {
		t.Error("should be visible after SetVisible(true)")
	}
}

func TestContentBounds(t *testing.T) {
	fw := NewFloatingWindow("cb", "CB", testWidget{}, 20, 10,
		WithBorderStyle(BorderSingle))
	b := fw.ContentBounds()
	if b.X != 1 || b.Y != 1 || b.W != 18 || b.H != 8 {
		t.Errorf("ContentBounds = (%d,%d,%d,%d), want (1,1,18,8)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestNewFloatingWindowWithBorderLShape(t *testing.T) {
	fw := NewFloatingWindow("lshape", "LShape", testWidget{}, 20, 10,
		WithBorderStyle(BorderLShape))
	b := fw.ContentBounds()
	if b.X != 1 || b.Y != 1 || b.W != 19 || b.H != 8 {
		t.Errorf("LShape content = (%d,%d,%d,%d), want (1,1,19,8)",
			b.X, b.Y, b.W, b.H)
	}
}

func TestRenderWithNoContent(t *testing.T) {
	fw := NewFloatingWindow("no-content", "NoContent", nil, 10, 5)
	ctx := testContext()
	fw.Render(ctx)
	if fw.Surface() == nil {
		t.Fatal("Surface should not be nil")
	}
}
