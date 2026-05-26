package compositor

import (
	"crude/tui/cell"
	"crude/tui/surface"
)

// Compositor manages the double-buffer frame pipeline.
//
// Frame lifecycle:
//  1. BeginFrame() — clear back buffer with transparent cells, reset damage
//  2. App renders widgets into surfaces
//  3. Composite() — flatten surface tree, composite bottom-to-top into back buffer
//  4. Present() — generate ANSI output from back buffer, copy back to front
//
// The compositor owns the screen-sized cell buffers and manages the
// conversion from cell buffer → ANSI escape sequences for the terminal.
// It is the bridge between the surface tree (Layer 1) and the terminal
// output that Bubble Tea consumes.
type Compositor struct {
	front  *surface.CellBuffer // Currently displayed (last frame sent to terminal)
	back   *surface.CellBuffer // Being rendered into (current frame)
	width  int                 // Terminal width in columns
	height int                 // Terminal height in rows
	root   *surface.Surface   // Root of the surface tree
	damage DamageRegion       // Tracks what changed this frame

	// rootCellCache stores the root surface's cell buffer from the last full frame.
	// During drag/resize, we copy this directly instead of re-parsing ANSI strings.
	rootCellCache *surface.CellBuffer
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
	// means "nothing drawn" — surfaces will blit their opaque cells on top)
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
	c.InvalidateRootCellCache()
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

// CacheRootCells copies the current root surface's cell buffer for reuse
// during drag/resize frames. This is called during full frame builds.
func (c *Compositor) CacheRootCells() {
	if c.rootCellCache == nil || c.rootCellCache.Width() != c.width || c.rootCellCache.Height() != c.height {
		c.rootCellCache = surface.NewCellBuffer(c.width, c.height)
	}
	// Deep copy the root surface buffer into the cache
	srcCells := c.root.Buffer().Cells()
	copy(c.rootCellCache.Cells(), srcCells)
}

// RestoreRootCells copies the cached cell buffer back into the root surface.
// Used during drag/resize frames to skip ANSI parsing. Returns true if
// cache was available and restored, false if no cache exists.
func (c *Compositor) RestoreRootCells() bool {
	if c.rootCellCache == nil {
		return false
	}
	dstCells := c.root.Buffer().Cells()
	copy(dstCells, c.rootCellCache.Cells())
	return true
}

// InvalidateRootCellCache clears the cached root cells, forcing a full rebuild.
func (c *Compositor) InvalidateRootCellCache() {
	c.rootCellCache = nil
}

// HasRootCellCache reports whether a cached root cell buffer exists.
func (c *Compositor) HasRootCellCache() bool {
	return c.rootCellCache != nil
}

// BeginFrame starts a new frame.
// cells (so shrunk content is properly overwritten), resets the damage region,
// clears all children from the root surface, and clears the root surface's
// cell buffer to prevent stale content from previous frames.
// Call this before rendering widgets into the surface tree.
func (c *Compositor) BeginFrame() {
	// Fill with opaque default background, NOT transparent.
	// This ensures that when content shrinks, old cells are properly overwritten
	// instead of leaving transparent gaps that skip during Blit.
	c.back.Fill(cell.BlankCell(cell.NoColor))
	c.damage.Reset()
	// Clear all children from the root surface to prevent accumulation.
	// Children will be re-added during this frame's rendering.
	c.root.RemoveAllChildren()
	// Clear the root surface's cell buffer so content from the previous
	// frame does not persist. buildMainView writes the new content, and
	// unwritten cells remain as transparent blanks — which is correct
	// because the back buffer is already filled with opaque blanks.
	c.root.Buffer().Clear()
}

// BeginPartialFrame starts a partial frame used during drag/resize.
// Unlike BeginFrame, it does NOT clear the back buffer or remove root children.
// The back buffer is filled with opaque default background so that old window
// positions get properly overwritten when the window moves. Children will be
// repositioned during the frame but their content surfaces are reused from
// the previous frame (via the contentDirty optimization).
func (c *Compositor) BeginPartialFrame() {
	c.back.Fill(cell.BlankCell(cell.NoColor))
	c.damage.Reset()
	// Remove all children so they can be re-added during UpdatePositions.
	// The root cell buffer content is preserved (restored from cache by caller).
	c.root.RemoveAllChildren()
}

// Composite composites the entire surface tree into the back buffer.
// Uses painter's algorithm: flatten tree in z-order, blit each visible
// surface from bottom to top.
//
// Algorithm:
//  1. Get the flattened z-order list from root surface
//  2. For each surface in the list (bottom to top):
//     a. Get the surface's absolute screen position
//     b. If the surface is not visible, skip it
//     c. If the surface's alpha < 1.0, use BlendAlpha; otherwise use Blit
//     d. Mark the blitted region in the damage tracker
func (c *Compositor) Composite() {
	// Flatten the surface tree in z-order (bottom to top)
	surfaces := c.root.FlattenZOrder()

	// Composite each surface into the back buffer
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

		// Note: In V1, the damage region will cover the full screen because
		// the root surface (which spans the entire terminal) is included in
		// the flattened z-order list. This is acceptable for full-frame ANSI
		// output. Future optimization: skip marking the root surface's bounds
		// if it only contains transparent cells, and only mark regions where
		// visible content was actually written.

		// Mark the region as damaged
		c.damage.MarkRect(surface.Rect{
			X: absX, Y: absY,
			W: buf.Width(), H: buf.Height(),
		})
	}
}

// Present finalizes the frame:
//  1. Generate ANSI output from the back buffer
//  2. Swap front and back buffers (eliminates 240KB/frame clone allocation)
//  3. Return the ANSI string
//
// The returned string can be passed directly to Bubble Tea's View() return value.
// For V1, we always generate full-frame output. The buffer swap is the primary
// performance win — it avoids allocating a new buffer every frame.
func (c *Compositor) Present() string {
	output := GenerateANSI(c.back)

	// Swap buffers: front becomes the just-presented frame, back gets recycled.
	// The new back buffer (old front) will be cleared/filled in the next
	// BeginFrame() or BeginPartialFrame() call.
	c.front, c.back = c.back, c.front

	// Clear the dirty tree: all surfaces have been composited and presented,
	// so reset their dirty flags for the next frame cycle.
	c.root.ClearDirtyTree()

	return output
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
