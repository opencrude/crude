package compositor

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

func TestNewCompositor_Dimensions(t *testing.T) {
	c := NewCompositor(80, 24)
	if c.Width() != 80 || c.Height() != 24 {
		t.Fatal(c.Width(), c.Height())
	}
	if c.Root() == nil || c.Root().ID() != "root" {
		t.Fatal("root")
	}
	if c.Root().Width() != 80 || c.Root().Height() != 24 {
		t.Fatal("root dims")
	}
}

func TestCompositor_Resize(t *testing.T) {
	c := NewCompositor(10, 5)
	c.Resize(20, 10)
	if c.Width() != 20 || c.Height() != 10 {
		t.Fatal("resize")
	}
	if c.Root().Width() != 20 || c.Root().Height() != 10 {
		t.Fatal("root resize")
	}
	// noop
	c.Resize(20, 10)
}

func TestCompositor_BeginFrameClearsChildren(t *testing.T) {
	c := NewCompositor(10, 5)
	child := surface.NewSurface("layer", 10, 5, cell.BlankCell(cell.Blue))
	c.Root().AddChild(child)
	if len(c.Root().Children()) != 1 {
		t.Fatal("precondition")
	}
	c.BeginFrame()
	if len(c.Root().Children()) != 0 {
		t.Fatal("BeginFrame must remove children")
	}
}

func TestCompositor_CompositeBlitsChildren(t *testing.T) {
	c := NewCompositor(5, 3)
	c.BeginFrame()
	layer := surface.NewSurface("layer", 5, 3, cell.BlankCell(cell.Blue))
	layer.Buffer().Set(1, 1, cell.NewCell('X', cell.Red, cell.Blue, 0))
	c.Root().AddChild(layer)
	c.Composite()
	// Present generates ANSI; verify via front buffer after Present swap.
	// After Composite, content is in back. Present swaps.
	out := c.Present()
	if out == "" {
		t.Fatal("expected ANSI output with damage")
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("expected X in output: %q", out)
	}
}

func TestCompositor_PresentNoDamageEmpty(t *testing.T) {
	c := NewCompositor(4, 2)
	// BeginFrame resets damage; Composite without children marks nothing?
	// BeginFrame clears damage. Composite with only invisible/empty tree:
	// root is in FlattenZOrder and is Visible — so it marks damage for root rect.
	c.BeginFrame()
	// Don't composite — Present with no damage
	out := c.Present()
	if out != "" {
		t.Fatalf("no damage should yield empty present, got %q", out)
	}
}

func TestCompositor_FullRender(t *testing.T) {
	c := NewCompositor(6, 2)
	// Attach child before FullRender — but FullRender calls BeginFrame which clears children.
	// So FullRender alone on empty root still composites root surface.
	out := c.FullRender()
	// Root is transparent blanks — still damages and generates ANSI (spaces)
	if out == "" {
		// Transparent blanks still produce characters (spaces) via GenerateANSI
		// Char is space for TransparentBlank
		t.Fatal("full render should produce output when root is composited")
	}
}

func TestCompositor_AlphaBlendPath(t *testing.T) {
	c := NewCompositor(3, 1)
	c.BeginFrame()
	// Background layer opaque
	bg := surface.NewSurface("bg", 3, 1, cell.BlankCell(cell.NewTrueColor(0, 0, 0)))
	bg.Buffer().Set(0, 0, cell.NewCell('B', cell.NewTrueColor(0, 0, 0), cell.NewTrueColor(0, 0, 0), 0))
	// Foreground semi-transparent
	fg := surface.NewSurface("fg", 3, 1, cell.TransparentBlank())
	fg.SetAlpha(0.5)
	fg.SetZOrder(1)
	fg.Buffer().Set(0, 0, cell.NewCell('F', cell.NewTrueColor(255, 0, 0), cell.NewTrueColor(255, 0, 0), 0))
	c.Root().AddChild(bg)
	c.Root().AddChild(fg)
	c.Composite()
	out := c.Present()
	if out == "" {
		t.Fatal("expected output")
	}
	// Character from source (F)
	if !strings.Contains(out, "F") {
		t.Fatalf("expected F: %q", out)
	}
}

func TestCompositor_ZOrderPainting(t *testing.T) {
	c := NewCompositor(3, 1)
	c.BeginFrame()
	bottom := surface.NewSurface("bottom", 3, 1, cell.TransparentBlank())
	bottom.SetZOrder(0)
	bottom.Buffer().Set(0, 0, cell.NewCell('B', cell.Red, cell.Blue, 0))
	top := surface.NewSurface("top", 3, 1, cell.TransparentBlank())
	top.SetZOrder(1)
	top.Buffer().Set(0, 0, cell.NewCell('T', cell.Green, cell.Yellow, 0))
	c.Root().AddChild(bottom)
	c.Root().AddChild(top)
	c.Composite()
	out := c.Present()
	// First non-space char of first row should be T (top wins)
	// Output may have ANSI before char
	if !strings.Contains(out, "T") {
		t.Fatalf("top should paint over: %q", out)
	}
}

func TestCompositor_HiddenSurfaceSkipped(t *testing.T) {
	c := NewCompositor(3, 1)
	c.BeginFrame()
	s := surface.NewSurface("hidden", 3, 1, cell.TransparentBlank())
	s.Buffer().Set(0, 0, cell.NewCell('H', cell.Red, cell.Blue, 0))
	s.SetVisible(false)
	c.Root().AddChild(s)
	c.Composite()
	out := c.Present()
	// Root still damages; H should not appear from hidden surface
	if strings.Contains(out, "H") {
		t.Fatalf("hidden surface leaked: %q", out)
	}
}

func TestCompositor_ZeroSizePresent(t *testing.T) {
	c := NewCompositor(0, 0)
	c.BeginFrame()
	// Force damage
	c.damage.Mark(0, 0)
	out := c.Present()
	if out != "" {
		t.Fatalf("zero size present: %q", out)
	}
}
