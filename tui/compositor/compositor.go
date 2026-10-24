package compositor

import (
	"crude/tui/cell"
	"crude/tui/perf"
	"crude/tui/surface"
)

// Compositor manages the double-buffer frame pipeline.
//
// Frame lifecycle:
//  1. BeginFrame(): reset damage and clear back buffer
//  2. App renders widgets into surfaces
//  3. Composite(): flatten surface tree into back buffer
//  4. PresentSurface(): publish the cell grid and swap front and back
//
// Bubble Tea snapshots the published drawable before the next Update and
// diffs cells and writes the terminal asynchronously. Present is retained for
// explicit ANSI consumers, not the application's presentation path.
type Compositor struct {
	front  *surface.CellBuffer // Currently displayed (last frame sent to terminal)
	back   *surface.CellBuffer // Being rendered into (current frame)
	width  int                 // Terminal width in columns
	height int                 // Terminal height in rows
	root   *surface.Surface    // Root of the surface tree
	damage DamageRegion        // Tracks what changed this frame
	direct directCells         // Reusable conversion scratch, never a frame cache
}

// NewCompositor creates a compositor for the given terminal dimensions.
// The root surface is created at the full terminal size with a transparent
// base cell. Application surfaces should be attached as children of the root.
func NewCompositor(w, h int) *Compositor {
	c := &Compositor{
		width:  w,
		height: h,
	}

	// Create front and back buffers
	c.front = surface.NewCellBuffer(w, h)
	c.back = surface.NewCellBuffer(w, h)

	// Create root surface at full terminal size with transparent base
	// (transparent so that the back buffer clearing with transparent blanks
	// means "nothing drawn"; surfaces will blit their opaque cells on top)
	c.root = surface.NewSurface("root", w, h, cell.TransparentBlank())

	return c
}

// Resize handles terminal resize events.
// Resizes both buffers and the root surface. Existing content in the front
// buffer is preserved where possible; the back buffer will be cleared on
// the next BeginFrame call.
func (c *Compositor) Resize(w, h int) {
	if w == c.width && h == c.height {
		return
	}
	c.width = w
	c.height = h
	c.front.Resize(w, h)
	c.back.Resize(w, h)
	c.root.Resize(w, h)
	c.direct.generation++
}

// Root returns the root surface. All application surfaces should be
// attached as children (directly or transitively) of the root.
func (c *Compositor) Root() *surface.Surface {
	return c.root
}

// Width returns the terminal width in columns.
func (c *Compositor) Width() int {
	return c.width
}

// Height returns the terminal height in rows.
func (c *Compositor) Height() int {
	return c.height
}

// BeginFrame starts a new frame.
// Fills the back buffer with opaque default background, resets the damage
// region, and clears all children from the root surface. The persistent layer
// surfaces will be re-added during rendering. Clearing the back buffer every
// frame is simple and correct; the real performance win is avoiding the
// expensive string build for unchanged layers.
func (c *Compositor) BeginFrame() {
	c.back.Clear()
	c.damage.Reset()
	c.root.RemoveAllChildren()
	c.root.Buffer().Clear()
}

// Composite composites the entire surface tree into the back buffer using
// painter's algorithm. It flattens the tree in z-order and blits every
// visible surface from bottom to top. The back buffer is pre-cleared in
// BeginFrame, so any uncovered area shows the default background.
//
// This is called every frame. The performance win comes from the layer
// surfaces: only dirty layers have their cell buffers re-rendered (the
// expensive string build / ANSI parse). The blit itself is a flat memcpy of
// ~2000 cells for 80x24, which is microseconds.
func (c *Compositor) Composite() {
	surfaces := c.root.FlattenZOrder()
	for _, s := range surfaces {
		if !s.Visible() {
			continue
		}
		absX, absY := s.AbsolutePosition()
		buf := s.Buffer()
		if s.Alpha() < 1.0 {
			surface.BlendAlpha(c.back, absX, absY, buf, s.Alpha())
		} else {
			surface.Blit(c.back, absX, absY, buf, nil)
		}
		c.damage.MarkRect(surface.Rect{
			X: absX, Y: absY,
			W: buf.Width(), H: buf.Height(),
		})
	}
}

// Present finalizes the frame:
//  1. Generate ANSI output from the back buffer
//  2. Swap front and back buffers
//  3. Clear the dirty tree for the next frame cycle
//
// This explicit ANSI path remains for diagnostics and string consumers.
// App.View uses PresentSurface instead; Bubble Tea owns terminal diff/output.
func (c *Compositor) Present() string {
	var output string
	if c.damage.HasDamage() && c.width > 0 && c.height > 0 {
		output = GenerateANSI(c.back)
		perf.MarkANSIFrame(c.width * c.height)
	}

	c.finishFrame()

	return output
}

func (c *Compositor) finishFrame() {
	c.front, c.back = c.back, c.front
	c.direct.generation++
	c.root.ClearDirtyTree()
	c.damage.Reset()
}

// FullRender performs a complete frame cycle:
// BeginFrame → Composite → Present
// Returns the ANSI output string.
// Convenience method for the common case where the full pipeline runs
// in a single step.
func (c *Compositor) FullRender() string {
	c.BeginFrame()
	c.Composite()
	return c.Present()
}
