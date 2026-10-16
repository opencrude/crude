package surface

import (
	"testing"

	"crude/tui/cell"
)

func TestSurface_NewAndBuffer(t *testing.T) {
	base := cell.BlankCell(cell.Blue)
	s := NewSurface("main", 10, 5, base)
	if s.ID() != "main" {
		t.Fatal(s.ID())
	}
	if s.Width() != 10 || s.Height() != 5 {
		t.Fatal("dims")
	}
	if !s.Buffer().Get(0, 0).BG.Equal(cell.Blue) {
		t.Fatal("base fill")
	}
	if !s.Visible() || s.Alpha() != 1.0 {
		t.Fatal("defaults")
	}
}

func TestSurface_TransparentBaseNotFilled(t *testing.T) {
	s := NewSurface("t", 2, 2, cell.TransparentBlank())
	if !s.Buffer().Get(0, 0).Transparent {
		t.Fatal("transparent base")
	}
}

func TestSurface_PositionAbsolute(t *testing.T) {
	root := NewSurface("root", 20, 20, cell.TransparentBlank())
	child := NewSurface("child", 5, 5, cell.TransparentBlank())
	grand := NewSurface("grand", 2, 2, cell.TransparentBlank())
	root.AddChild(child)
	child.AddChild(grand)
	child.SetPosition(3, 4)
	grand.SetPosition(1, 2)
	ax, ay := grand.AbsolutePosition()
	if ax != 4 || ay != 6 {
		t.Fatalf("abs %d,%d", ax, ay)
	}
	sx, sy := grand.LocalToScreen(0, 0)
	if sx != 4 || sy != 6 {
		t.Fatalf("local2screen %d,%d", sx, sy)
	}
	lx, ly := grand.ScreenToLocal(5, 8)
	if lx != 1 || ly != 2 {
		t.Fatalf("screen2local %d,%d", lx, ly)
	}
}

func TestSurface_SetPositionNoop(t *testing.T) {
	s := NewSurface("s", 1, 1, cell.TransparentBlank())
	s.ClearDirty()
	s.SetPosition(0, 0) // default is 0,0
	if s.IsDirty() {
		t.Fatal("noop position should not dirty")
	}
	s.SetPosition(1, 2)
	if !s.IsDirty() {
		t.Fatal("position change dirties")
	}
	x, y := s.Position()
	if x != 1 || y != 2 {
		t.Fatal(x, y)
	}
}

func TestSurface_ChildrenZOrder(t *testing.T) {
	p := NewSurface("p", 10, 10, cell.TransparentBlank())
	a := NewSurface("a", 1, 1, cell.TransparentBlank())
	b := NewSurface("b", 1, 1, cell.TransparentBlank())
	c := NewSurface("c", 1, 1, cell.TransparentBlank())
	a.SetZOrder(2)
	b.SetZOrder(0)
	c.SetZOrder(1)
	// Clear dirty from SetZOrder before parent attach noise
	p.AddChild(a)
	p.AddChild(b)
	p.AddChild(c)
	kids := p.Children()
	if len(kids) != 3 {
		t.Fatal(len(kids))
	}
	if kids[0].ID() != "b" || kids[1].ID() != "c" || kids[2].ID() != "a" {
		t.Fatalf("z order: %s %s %s", kids[0].ID(), kids[1].ID(), kids[2].ID())
	}
	// Duplicate add no-op
	p.AddChild(a)
	if len(p.Children()) != 3 {
		t.Fatal("duplicate add")
	}
	// Reorder via SetZOrder on attached child
	a.SetZOrder(-1)
	if p.Children()[0].ID() != "a" {
		t.Fatalf("after re-z: %s", p.Children()[0].ID())
	}
}

func TestSurface_RemoveChildAndAll(t *testing.T) {
	p := NewSurface("p", 5, 5, cell.TransparentBlank())
	a := NewSurface("a", 1, 1, cell.TransparentBlank())
	b := NewSurface("b", 1, 1, cell.TransparentBlank())
	p.AddChild(a)
	p.AddChild(b)
	p.RemoveChild(a)
	if a.Parent() != nil {
		t.Fatal("parent cleared")
	}
	if len(p.Children()) != 1 || p.Children()[0].ID() != "b" {
		t.Fatal("remove one")
	}
	p.RemoveAllChildren()
	if len(p.Children()) != 0 || b.Parent() != nil {
		t.Fatal("remove all")
	}
	// remove missing is fine
	p.RemoveChild(a)
}

func TestSurface_VisibilityChain(t *testing.T) {
	root := NewSurface("r", 5, 5, cell.TransparentBlank())
	child := NewSurface("c", 2, 2, cell.TransparentBlank())
	root.AddChild(child)
	if !child.Visible() {
		t.Fatal("both visible")
	}
	root.SetVisible(false)
	if child.Visible() {
		t.Fatal("ancestor hidden hides child")
	}
	root.SetVisible(true)
	child.SetVisible(false)
	if child.Visible() {
		t.Fatal("self hidden")
	}
	// noop
	child.ClearDirty()
	child.SetVisible(false)
	if child.IsDirty() {
		t.Fatal("noop visible")
	}
}

func TestSurface_AlphaClamp(t *testing.T) {
	s := NewSurface("s", 1, 1, cell.TransparentBlank())
	s.SetAlpha(2)
	if s.Alpha() != 1 {
		t.Fatal(s.Alpha())
	}
	s.SetAlpha(-1)
	if s.Alpha() != 0 {
		t.Fatal(s.Alpha())
	}
	s.SetAlpha(0.5)
	if s.Alpha() != 0.5 {
		t.Fatal(s.Alpha())
	}
}

func TestSurface_DirtyPropagation(t *testing.T) {
	root := NewSurface("r", 5, 5, cell.TransparentBlank())
	child := NewSurface("c", 2, 2, cell.TransparentBlank())
	root.AddChild(child)
	root.ClearDirtyTree()
	child.MarkDirty()
	if !child.IsDirty() || !root.IsDirty() {
		t.Fatal("dirty propagates up")
	}
	// MarkDirty when already dirty is no-op path
	child.MarkDirty()
	root.ClearDirtyTree()
	if child.IsDirty() || root.IsDirty() {
		t.Fatal("clear tree")
	}
}

func TestSurface_DirtyRect(t *testing.T) {
	s := NewSurface("s", 10, 10, cell.TransparentBlank())
	s.ClearDirty()
	s.MarkDirtyRect(Rect{X: 2, Y: 2, W: 3, H: 3})
	if !s.IsDirty() || !s.HasDirtyRects() {
		t.Fatal("dirty rect")
	}
	rects := s.DirtyRects()
	if len(rects) != 1 || rects[0] != (Rect{X: 2, Y: 2, W: 3, H: 3}) {
		t.Fatalf("%+v", rects)
	}
	// OOB clipped to empty; no dirty if completely outside
	s.ClearDirty()
	s.MarkDirtyRect(Rect{X: 100, Y: 100, W: 1, H: 1})
	if s.IsDirty() {
		t.Fatal("oob dirty rect should not mark")
	}
	// Partial clip
	s.MarkDirtyRect(Rect{X: 8, Y: 8, W: 10, H: 10})
	if !s.IsDirty() {
		t.Fatal("partial clip should dirty")
	}
	s.ClearDirty()
	if s.HasDirtyRects() {
		t.Fatal("clear rects")
	}
}

func TestSurface_FlattenZOrder(t *testing.T) {
	root := NewSurface("root", 10, 10, cell.TransparentBlank())
	a := NewSurface("a", 1, 1, cell.TransparentBlank())
	b := NewSurface("b", 1, 1, cell.TransparentBlank())
	aChild := NewSurface("a-child", 1, 1, cell.TransparentBlank())
	a.SetZOrder(0)
	b.SetZOrder(1)
	root.AddChild(a)
	root.AddChild(b)
	a.AddChild(aChild)
	flat := root.FlattenZOrder()
	ids := make([]string, len(flat))
	for i, s := range flat {
		ids[i] = s.ID()
	}
	// pre-order: root, a, a-child, b
	want := []string{"root", "a", "a-child", "b"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("flatten %v want %v", ids, want)
		}
	}
}

func TestSurface_HitTest(t *testing.T) {
	root := NewSurface("root", 20, 20, cell.TransparentBlank())
	// Child at (5,5) size 4x4
	child := NewSurface("child", 4, 4, cell.TransparentBlank())
	child.SetPosition(5, 5)
	// Top child overlapping
	top := NewSurface("top", 2, 2, cell.TransparentBlank())
	top.SetPosition(6, 6)
	top.SetZOrder(10)
	root.AddChild(child)
	root.AddChild(top)

	// Outside root
	if root.HitTest(100, 100) != nil {
		t.Fatal("outside")
	}
	// On root empty area
	hit := root.HitTest(0, 0)
	if hit == nil || hit.ID() != "root" {
		t.Fatalf("root hit: %v", hit)
	}
	// On child only
	hit = root.HitTest(5, 5)
	if hit == nil || hit.ID() != "child" {
		t.Fatalf("child hit: %v", hit)
	}
	// On top (higher z)
	hit = root.HitTest(6, 6)
	if hit == nil || hit.ID() != "top" {
		t.Fatalf("top hit: %v", hit)
	}
	// Hidden child skipped
	top.SetVisible(false)
	hit = root.HitTest(6, 6)
	if hit == nil || hit.ID() != "child" {
		t.Fatalf("hidden top falls through: %v", hit)
	}
}

func TestSurface_BoundsAndLastScreen(t *testing.T) {
	s := NewSurface("s", 8, 6, cell.TransparentBlank())
	s.SetPosition(2, 3)
	if s.Bounds() != (Rect{W: 8, H: 6}) {
		t.Fatal(s.Bounds())
	}
	// No parent: screen bounds at position
	sb := s.ScreenBounds()
	if sb != (Rect{X: 2, Y: 3, W: 8, H: 6}) {
		t.Fatal(sb)
	}
	s.SetLastScreenBounds(Rect{X: 1, Y: 1, W: 2, H: 2})
	if s.LastScreenBounds() != (Rect{X: 1, Y: 1, W: 2, H: 2}) {
		t.Fatal(s.LastScreenBounds())
	}
}

func TestSurface_Resize(t *testing.T) {
	s := NewSurface("s", 4, 4, cell.BlankCell(cell.Blue))
	s.ClearDirty()
	s.Resize(8, 2)
	if s.Width() != 8 || s.Height() != 2 {
		t.Fatal("resize")
	}
	if !s.IsDirty() {
		t.Fatal("resize dirties")
	}
}
