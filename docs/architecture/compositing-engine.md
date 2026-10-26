# Compositing Engine Architecture

> Status: Current architecture — cells, surfaces, compositor and window manager. The shell-level frame contract is in [the shell host](shell-host.md).

## 1. Vision

Build a production-grade multi-window compositing engine for the Crude TUI that:
- Supports **virtually infinite nesting** of windows, panes, and widgets
- Performs **per-cell compositing** (not naive whole-line rendering)
- Enables **floating windows** that can overlap, be dragged, resized, and stacked
- Supports **transparency** (seeing through empty cells to windows below)
- Handles **partial-line rendering** (Window A on left, Window B on right of the same row)
- Reuses retained cells and bounded caches; allocation/latency claims require measured gates
- Integrates cleanly with Bubble Tea v2's event loop

## 2. Architecture Layers

```
┌                             ┐
│  Layer 4: Application                                    │
│  (viewmodel, App, mode routing, existing business logic)  │
├                             ┤
│  Layer 3: Window Manager (tui/wm/)                      │
│  (FloatingWindow, WindowManager, focus, frame hit-test,  │
│   z-order, drag, resize, hit-testing)                    │
├                             ┤
│  Layer 2.5: Window content (wm.WindowContent)          │
│  (Widget, RenderContext, Container, adapters)            │
├                             ┤
│  Layer 2: Compositor (tui/compositor/)                   │
│  (Compositor, double-buffer, damage tracking, cell diff, │
│   direct-cell publication / diagnostic ANSI)              │
├                             ┤
│  Layer 1: Surface System (tui/surface/)                  │
│  (CellBuffer, Surface hierarchy, Rect, ClipStack,       │
│   compositing operations: blit, blend, merge)            │
├                             ┤
│  Layer 0: Cell Primitives (tui/cell/)                    │
│  (Cell type, Color, Attributes, pool-based interning)    │
└                             ┘
```

Each layer depends ONLY on the layer directly below it. No upward dependencies.

## 3. Package Structure

```
tui/
├  cell/                    # Layer 0: Cell primitives
│   ├  cell.go             # Cell, Color, Attributes types
│   └  pool.go             # CharPool, StylePool (internment)
├  surface/                 # Layer 1: Surface system
│   ├  buffer.go           # CellBuffer (2D cell array, operations)
│   ├  surface.go          # Surface (buffer + position + hierarchy)
│   ├  rect.go             # Rect, geometry utilities
│   ├  clip.go             # ClipStack (scissor clipping)
│   └  compose.go          # Blit, blend, merge operations
├  compositor/              # Layer 2: Compositing engine
│   ├  compositor.go       # Compositor (frame pipeline, double-buffer)
│   ├  damage.go           # DamageRegion tracking
│   ├  diff.go             # Cell diff between front/back buffers
│   ├  direct.go           # Direct-cell Bubble Tea/UV surface publication
│   └  output.go           # Explicit diagnostic/string ANSI output
├  wm/                      # Layer 3: Window manager
│   ├  window.go           # FloatingWindow struct and lifecycle
│   ├  manager.go          # WindowManager (focus, z-order, hit-test)
│   └  interaction.go      # Drag, resize, mouse handling
├  ui/                      # Retained components and unified visual chrome
│   ├  window_frame.go     # Window/Dialog/Popover border, title, close paint
│   └  component_widget.go # Component/WindowFrame bridge into FloatingWindow
```

The window-content contract (Layer 2.5) lives in `tui/wm/content.go`.

## 4. Layer 0: Cell Primitives (tui/cell/)

### 4.1 Color

```go
package cell

// ColorType identifies how a color is represented.
type ColorType uint8

const (
    ColorDefault  ColorType = iota // Terminal default (no escape sequence)
    ColorPalette                   // 256-color palette (0-255)
    ColorTrueColor                 // 24-bit RGB truecolor
)

// Color represents a terminal color. Supports default, palette, and truecolor.
type Color struct {
    Type    ColorType
    Palette uint8 // For ColorPalette: index 0-255
    R, G, B uint8 // For ColorTrueColor: RGB components
}

// Predefined colors for convenience
var (
    NoColor  = Color{Type: ColorDefault}
    Black    = Color{Type: ColorPalette, Palette: 0}
    White    = Color{Type: ColorPalette, Palette: 15}
    Red      = Color{Type: ColorPalette, Palette: 1}
    // ... more palette colors
)

// NewTrueColor creates an RGB truecolor.
func NewTrueColor(r, g, b uint8) Color {
    return Color{Type: ColorTrueColor, R: r, G: g, B: b}
}

// NewPaletteColor creates a 256-color palette entry.
func NewPaletteColor(idx uint8) Color {
    return Color{Type: ColorPalette, Palette: idx}
}

// ANSI returns the ANSI escape sequence fragment for setting this color as foreground.
func (c Color) ANSIFG() string { ... }

// ANSI returns the ANSI escape sequence fragment for setting this color as background.
func (c Color) ANSIBG() string { ... }

// Equal reports whether two colors are identical.
func (c Color) Equal(other Color) bool { ... }

// IsDefault reports whether this is the terminal default color.
func (c Color) IsDefault() bool { return c.Type == ColorDefault }

// Blend performs alpha compositing of this color over another.
// alpha=1.0 means fully this color, alpha=0.0 means fully the other.
func (c Color) Blend(over Color, alpha float32) Color { ... }
```

### 4.2 Attributes

```go
// Attrs represents text rendering attributes as a bitmask.
type Attrs uint16

const (
    AttrBold          Attrs = 1 << iota
    AttrItalic
    AttrUnderline
    AttrStrikethrough
    AttrReverse       // Swap foreground/background
    AttrBlink
    AttrDim
)

// ANSI returns the ANSI escape sequence for enabling these attributes.
func (a Attrs) ANSI() string { ... }

// Has reports whether all bits in the given mask are set.
func (a Attrs) Has(mask Attrs) bool { return a&mask == mask }
```

### 4.3 Cell

```go
// Cell represents a single terminal cell — the atomic unit of compositing.
//
// Each cell occupies one or more columns on the terminal grid.
// A wide character (CJK, emoji) occupies 2 columns and produces:
// - Cell{Char: '界', Width: 2} at column N
// - Cell{Char: 0, Width: 0} at column N+1 (continuation cell)
//
// A Transparent cell does not overwrite the cell below during compositing.
// This is how "holes" in floating windows let background content show through.
type Cell struct {
    Char        rune
    Width       uint8  // Display width: 0 (continuation), 1 (normal), 2 (wide)
    FG          Color
    BG          Color
    Attr        Attrs
    Transparent bool   // If true, compositing skips this cell (shows below)
}

// NewCell creates a normal-width cell with the given character and style.
func NewCell(ch rune, fg, bg Color, attr Attrs) Cell {
    return Cell{Char: ch, Width: 1, FG: fg, BG: bg, Attr: attr}
}

// NewWideCell creates a wide (2-column) cell.
func NewWideCell(ch rune, fg, bg Color, attr Attrs) Cell {
    return Cell{Char: ch, Width: 2, FG: fg, BG: bg, Attr: attr}
}

// ContinuationCell is the placeholder for the second column of a wide character.
var ContinuationCell = Cell{Char: 0, Width: 0}

// BlankCell creates a space cell with the given background.
func BlankCell(bg Color) Cell {
    return Cell{Char: ' ', Width: 1, BG: bg}
}

// TransparentBlank creates a transparent space cell (shows content below).
func TransparentBlank() Cell {
    return Cell{Char: ' ', Width: 1, Transparent: true}
}

// IsBlank reports whether the cell is an empty space (no visible character).
func (c Cell) IsBlank() bool {
    return c.Char == ' ' || c.Char == 0
}

// IsVisible reports whether the cell has any visible content.
func (c Cell) IsVisible() bool {
    return !c.Transparent && c.Char != 0 && c.Char != ' '
}

// Equal reports whether two cells are identical.
func (c Cell) Equal(other Cell) bool { ... }
```

### 4.4 Pool-based Interning

Inspired by Claude Code Ink's packed cell approach. Instead of allocating
new Color/Attrs objects per cell, we intern them into pools and reference by ID.

```go
// CharPool interns frequently-used runes to reduce memory.
// For a typical frame, most cells contain ASCII characters (space, letters, box-drawing).
// The pool maps rune → uint16 ID, avoiding per-cell rune storage overhead
// when the same character appears many times.
type CharPool struct {
    chars []rune        // ID → rune
    index map[rune]uint16 // rune → ID
}

// StylePool interns color+attribute combinations.
// A typical frame has ~20-50 unique style combinations.
// Interning reduces each cell's style storage from ~20 bytes to ~4 bytes.
type StylePool struct {
    styles []StyleEntry // ID → style
    index  map[StyleID]uint16 // style → ID
}

type StyleEntry struct {
    FG   Color
    BG   Color
    Attr Attrs
}

type StyleID struct {
    FG   Color
    BG   Color
    Attr Attrs
}

// Intern adds a style to the pool and returns its ID.
func (p *StylePool) Intern(fg, bg Color, attr Attrs) uint16 { ... }

// Get returns the style for the given ID.
func (p *StylePool) Get(id uint16) StyleEntry { ... }

// Reset clears the pool for a new frame.
func (p *CharPool) Reset() { ... }
func (p *StylePool) Reset() { ... }
```

## 5. Layer 1: Surface System (tui/surface/)

### 5.1 Rect

```go
package surface

// Rect represents a rectangular region in cell coordinates.
type Rect struct {
    X, Y, W, H int
}

// Empty reports whether the rectangle has zero area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Contains reports whether a point is inside the rectangle.
func (r Rect) Contains(x, y int) bool { ... }

// Intersect returns the intersection of two rectangles.
// If they don't overlap, returns an empty rect.
func (r Rect) Intersect(other Rect) Rect { ... }

// Union returns the smallest rectangle containing both rectangles.
func (r Rect) Union(other Rect) Rect { ... }

// FullRect returns a rect covering a buffer of the given size.
func FullRect(w, h int) Rect { return Rect{0, 0, w, h} }
```

### 5.2 CellBuffer

```go
// CellBuffer is a 2D grid of Cells. It is the fundamental rendering target.
//
// CellBuffer uses a flat []Cell array (not [][]Cell) for memory locality.
// Access pattern: buffer.cells[y * buffer.width + x]
//
// Operations on CellBuffer:
// - Set/Get individual cells
// - Fill a region with a base cell
// - Write a string of text into a row
// - Resize (reallocates the buffer)
type CellBuffer struct {
    cells  []cell.Cell
    width  int
    height int
}

// NewCellBuffer creates a buffer filled with transparent blank cells.
func NewCellBuffer(w, h int) *CellBuffer { ... }

// Width returns the buffer width in columns.
func (b *CellBuffer) Width() int { return b.width }

// Height returns the buffer height in rows.
func (b *CellBuffer) Height() int { return b.height }

// Set writes a cell at the given coordinates.
// Out-of-bounds writes are silently ignored (implicit clipping).
func (b *CellBuffer) Set(x, y int, c cell.Cell) { ... }

// Get reads the cell at the given coordinates.
// Out-of-bounds reads return a transparent blank cell.
func (b *CellBuffer) Get(x, y int) cell.Cell { ... }

// Fill fills the entire buffer with the given cell.
func (b *CellBuffer) Fill(c cell.Cell) { ... }

// FillRect fills a rectangular region with the given cell.
func (b *CellBuffer) FillRect(r Rect, c cell.Cell) { ... }

// Resize changes the buffer dimensions. Existing content is preserved
// where possible; new areas are filled with transparent blanks.
func (b *CellBuffer) Resize(w, h int) { ... }

// WriteString writes a string of text starting at position (x, y)
// using the given style. Handles wide characters correctly:
// - Wide chars (width=2) write the char at column N and a continuation at N+1
// - If a wide char would be split across the buffer edge, it is truncated
func (b *CellBuffer) WriteString(x, y int, text string, fg, bg cell.Color, attr cell.Attrs) int {
    // Uses uniseg.GraphemeClusterCount for proper grapheme iteration
    // Uses uniseg.StringWidth for width calculation
    // Returns the x position after the last written cell
}

// WriteRunes writes individual rune cells, handling wide chars.
func (b *CellBuffer) WriteRunes(x, y int, chars []rune, fg, bg cell.Color, attr cell.Attrs) int { ... }

// Clone creates a deep copy of the buffer.
func (b *CellBuffer) Clone() *CellBuffer { ... }

// Clear resets all cells to transparent blanks.
func (b *CellBuffer) Clear() { ... }

// Cells returns the raw cell slice (for fast iteration in compositor).
func (b *CellBuffer) Cells() []cell.Cell { return b.cells }

// Row returns a slice of cells for a specific row.
func (b *CellBuffer) Row(y int) []cell.Cell { ... }
```

### 5.3 Surface

```go
// Surface is a rectangular rendering surface with hierarchy and z-ordering.
//
// A Surface is the fundamental unit of compositing. It owns a CellBuffer
// and can have child surfaces positioned relative to itself.
//
// Hierarchy model (like Notcurses ncplanes):
// - Each surface has a position (X, Y) relative to its parent
// - Children are composited on top of their parent in z-order
// - Moving a parent moves all children with it
// - A surface clips its children to its own bounds
//
// Z-Order model:
// - Children with higher ZOrder are rendered on top
// - Within the same ZOrder, later children are on top (append order)
// - The compositor flattens the tree into a z-sorted list for compositing
//
// Transparency model:
// - A cell with Transparent=true does not overwrite the cell below
// - Empty (blank) cells can be transparent or opaque (depending on BG color)
// - A surface with Alpha < 1.0 blends its cells over the parent
type Surface struct {
    id       string
    buffer   *CellBuffer
    x        int       // Position relative to parent
    y        int
    zOrder   int       // Z-axis position (higher = on top among siblings)
    parent   *Surface
    children []*Surface // Ordered by z-order
    visible  bool
    alpha    float32    // 0.0 (invisible) to 1.0 (opaque)
    baseCell cell.Cell  // Default fill cell (background color)
    dirty    bool       // Whether this surface needs re-rendering
    damage   Rect       // Bounding box of changed cells
}

// NewSurface creates a new surface with the given dimensions and base cell.
func NewSurface(id string, w, h int, baseCell cell.Cell) *Surface { ... }

// ID returns the surface's unique identifier.
func (s *Surface) ID() string { return s.id }

// Buffer returns the surface's cell buffer for direct cell manipulation.
func (s *Surface) Buffer() *CellBuffer { return s.buffer }

// SetPosition sets the surface position relative to its parent.
func (s *Surface) SetPosition(x, y int) { ... }

// Position returns the position relative to parent.
func (s *Surface) Position() (int, int) { return s.x, s.y }

// AbsolutePosition returns the absolute screen position.
// Walks up the parent chain summing offsets.
func (s *Surface) AbsolutePosition() (int, int) { ... }

// LocalToScreen converts local surface coordinates to screen coordinates.
func (s *Surface) LocalToScreen(lx, ly int) (int, int) { ... }

// ScreenToLocal converts screen coordinates to local surface coordinates.
func (s *Surface) ScreenToLocal(sx, sy int) (int, int) { ... }

// SetZOrder sets the z-axis position among siblings.
func (s *Surface) SetZOrder(z int) { ... }

// AddChild adds a child surface. The child's parent is set to this surface.
func (s *Surface) AddChild(child *Surface) { ... }

// RemoveChild removes a child surface.
func (s *Surface) RemoveChild(child *Surface) { ... }

// Children returns the list of child surfaces (z-sorted).
func (s *Surface) Children() []*Surface { return s.children }

// SetVisible controls whether this surface is rendered.
func (s *Surface) SetVisible(v bool) { ... }

// Visible reports whether this surface and all its ancestors are visible.
func (s *Surface) Visible() bool { ... }

// SetAlpha sets the opacity for compositing (0.0-1.0).
func (s *Surface) SetAlpha(a float32) { ... }

// Resize resizes the surface's cell buffer.
func (s *Surface) Resize(w, h int) { ... }

// Width returns the buffer width.
func (s *Surface) Width() int { return s.buffer.Width() }

// Height returns the buffer height.
func (s *Surface) Height() int { return s.buffer.Height() }

// MarkDirty marks this surface and all ancestors as needing re-render.
func (s *Surface) MarkDirty() { ... }

// FlattenZOrder returns all surfaces in the tree in z-order (bottom to top).
// This is used by the compositor to produce the compositing order.
func (s *Surface) FlattenZOrder() []*Surface { ... }

// HitTest returns the topmost surface at the given screen coordinates.
// Traverses children in reverse z-order (topmost first).
func (s *Surface) HitTest(screenX, screenY int) *Surface { ... }

// Bounds returns the surface's local bounds as a Rect.
func (s *Surface) Bounds() Rect { ... }

// ScreenBounds returns the surface's bounds in screen coordinates.
func (s *Surface) ScreenBounds() Rect { ... }
```

### 5.4 ClipStack

```go
// ClipStack implements a scissor-clip stack for hierarchical clipping.
//
// When rendering nested containers, each container pushes its clip rect
// onto the stack. All drawing operations are clipped to the intersection
// of all active clip rects. When the container is done, it pops the clip.
//
// This is the same pattern used by GPU scissor testing and OpenTUI.
type ClipStack struct {
    stack []Rect
}

// NewClipStack creates an empty clip stack.
func NewClipStack() *ClipStack { ... }

// Push adds a clip rectangle. The new active clip is the intersection
// of the pushed rect and the previous active clip.
func (cs *ClipStack) Push(r Rect) { ... }

// Pop removes the top clip rectangle.
func (cs *ClipStack) Pop() { ... }

// Active returns the current effective clip rectangle.
// If the stack is empty, returns a maximally-sized rect (no clipping).
func (cs *ClipStack) Active() Rect { ... }

// Contains reports whether a point is inside the active clip region.
func (cs *ClipStack) Contains(x, y int) bool { ... }

// Reset clears the stack.
func (cs *ClipStack) Reset() { ... }

// Depth returns the current stack depth.
func (cs *ClipStack) Depth() int { ... }
```

### 5.5 Compositing Operations (compose.go)

```go
// Blit copies cells from a source buffer to a destination buffer at the given offset.
// Respects transparency: transparent source cells do not overwrite destination cells.
// Handles wide characters: a wide source cell writes the char at (dx, dy) and
// a continuation cell at (dx+1, dy). If the wide char would cross the buffer edge,
// it is truncated.
//
// This is the core compositing operation used by the Compositor.
func Blit(dst *CellBuffer, dstX, dstY int, src *CellBuffer, clip *ClipStack) { ... }

// BlitOpaque copies cells from source to destination, overwriting all cells
// (including transparent ones). Used when the destination should be fully replaced.
func BlitOpaque(dst *CellBuffer, dstX, dstY int, src *CellBuffer) { ... }

// BlendAlpha composites source cells over destination with alpha blending.
// For each cell: result = src * alpha + dst * (1 - alpha)
// Transparent source cells are skipped.
func BlendAlpha(dst *CellBuffer, dstX, dstY int, src *CellBuffer, alpha float32) { ... }

// MergeDown merges all cells from src into dst at the given offset.
// Cells marked as transparent in src are skipped, letting dst show through.
// Non-transparent cells in src overwrite the corresponding dst cells.
// This implements the "painter's algorithm" — later writes win.
func MergeDown(dst *CellBuffer, dstX, dstY int, src *CellBuffer) { ... }

// ClearRegion fills a rectangular region with transparent blank cells.
func ClearRegion(buf *CellBuffer, r Rect) { ... }
```

## 6. Layer 2: Compositor (tui/compositor/)

### 6.1 Compositor

```go
package compositor

// Compositor manages the double-buffer frame pipeline.
//
// Frame lifecycle:
// 1. BeginFrame() — clear back buffer, reset damage
// 2. App renders widgets into surfaces
// 3. Composite() — flatten surface tree, composite bottom-to-top into back buffer
// 4. PresentSurface() — publish cells, swap buffers; Bubble Tea owns output
//
// The compositor owns the screen-sized cell buffers and manages the
// completed cell-grid publication. Explicit ANSI output remains diagnostic.
type Compositor struct {
    front     *surface.CellBuffer // Currently displayed (last frame sent to terminal)
    back      *surface.CellBuffer // Being rendered into (current frame)
    width     int                 // Terminal width in columns
    height    int                 // Terminal height in rows
    root      *surface.Surface    // Root of the surface tree
    damage    DamageRegion        // Tracks what changed this frame
}

// NewCompositor creates a compositor for the given terminal dimensions.
func NewCompositor(w, h int) *Compositor { ... }

// Resize handles terminal resize events.
func (c *Compositor) Resize(w, h int) { ... }

// Root returns the root surface. All application surfaces should be
// attached as children (directly or transitively) of the root.
func (c *Compositor) Root() *surface.Surface { return c.root }

// Width returns the terminal width.
func (c *Compositor) Width() int { return c.width }

// Height returns the terminal height.
func (c *Compositor) Height() int { return c.height }

// BeginFrame starts a new frame. Clears the back buffer with transparent cells.
// Call this before rendering widgets.
func (c *Compositor) BeginFrame() { ... }

// Composite composites the entire surface tree into the back buffer.
// Traverses the tree in z-order (bottom-to-top) and blits each visible
// surface into the back buffer at its absolute screen position.
//
// This is the core of the compositing engine. It produces a single
// cell buffer that represents the final frame, with all overlapping
// windows, transparency, and partial-line rendering resolved at the
// cell level.
func (c *Compositor) Composite() { ... }

// PresentSurface publishes the completed grid and swaps buffers. The drawable
// borrows the front grid until BeginFrame/Resize. Bubble Tea snapshots it before
// another Update and UV diffs/replaces changed row spans for terminal output.
func (c *Compositor) PresentSurface() uv.Surface { ... }
func (c *Compositor) CurrentSurface() uv.Surface { ... }

// Present is the explicit ANSI path for diagnostics and string consumers.
// It is not called by App.View's normal direct-cell path.
func (c *Compositor) Present() string { ... }

// FullRender performs a complete frame cycle:
// BeginFrame → Composite → Present
// Returns the ANSI output string.
// Convenience method for the common case.
func (c *Compositor) FullRender() string { ... }
```

### 6.2 DamageRegion

```go
// DamageRegion tracks which cells changed during a frame.
//
// For efficiency, we track a bounding box rather than individual cells.
// Explicit diff consumers can restrict comparisons to this box.
// Normal App output uses UV's independent changed-row replacement.
//
// For future optimization, we can track multiple non-overlapping rectangles
// instead of a single bounding box.
type DamageRegion struct {
    MinX, MinY int // Top-left corner of the damage box
    MaxX,MaxY int // Bottom-right corner (exclusive)
    hasDamage  bool
}

// NewDamageRegion creates an empty damage region.
func NewDamageRegion() *DamageRegion { ... }

// Mark records that a cell at (x, y) was written this frame.
// Expands the bounding box to include this point.
func (d *DamageRegion) Mark(x, y int) { ... }

// MarkRect records that a rectangular region was modified.
func (d *DamageRegion) MarkRect(r surface.Rect) { ... }

// Bounds returns the bounding box of all damaged cells.
func (d *DamageRegion) Bounds() surface.Rect { ... }

// HasDamage reports whether any cells were damaged.
func (d *DamageRegion) HasDamage() bool { ... }

// Reset clears the damage region for a new frame.
func (d *DamageRegion) Reset() { ... }

// ContainsRow reports whether a row is within the damage region.
// Used by the diff to skip unchanged rows.
func (d *DamageRegion) ContainsRow(y int) bool { ... }

// ContainsCell reports whether a specific cell is within the damage region.
func (d *DamageRegion) ContainsCell(x, y int) bool { ... }
```

### 6.3 Cell Diff (diff.go)

```go
// DiffResult contains the cells that changed between two buffers.
type DiffResult struct {
    Cells []CellChange // Changed cells, sorted by (y, x) for sequential output
}

// CellChange represents a single changed cell and its new value.
type CellChange struct {
    X, Y int
    Cell cell.Cell
}

// Diff compares two cell buffers within the given region and returns
// the cells that differ. This is used to generate minimal ANSI output.
//
// Optimization: skip rows outside the damage region.
// Further optimization: compare only cells within damage rect columns.
func Diff(front, back *surface.CellBuffer, damage *DamageRegion) *DiffResult { ... }

// DiffRow compares a single row between two buffers.
// Returns the changed cells for that row.
func DiffRow(front, back *surface.CellBuffer, y int, minX, maxX int) []CellChange { ... }
```

### 6.4 ANSI Output Generation (output.go)

```go
// GenerateANSI converts a cell buffer into an ANSI escape sequence string.
//
// The output is a full frame: each row on a separate line, with
// ANSI SGR (Select Graphic Rendition) sequences for colors and attributes.
//
// Optimization strategy:
// 1. Track the "current style" cursor (fg, bg, attrs)
// 2. Only emit SGR sequences when the style changes
// 3. Group consecutive cells with the same style
// 4. For diff-based output: only emit changed rows/cells
//
// This is an explicit diagnostic/string path, not App.View presentation.
// The inner loop must be tight. Avoid string concatenation in the hot path;
// use a bytes.Buffer or strings.Builder.
func GenerateANSI(buf *surface.CellBuffer) string { ... }

// GenerateDiffANSI converts only the changed cells into ANSI output.
// Uses cursor positioning (CSI H) to jump to changed cells.
//
// Diagnostic consumers can request changed-cell ANSI. App.View instead hands
// a native cell surface to Bubble Tea; UV owns the terminal diff/output.
func GenerateDiffANSI(buf *surface.CellBuffer, diff *DiffResult) string { ... }

// cellToANSI writes the ANSI escape sequences for a single cell's style.
// Compares against the current style state and only emits changes.
// Returns the updated style state.
func cellToANSI(sb *strings.Builder, c cell.Cell, cur *cellStyleState) { ... }
```

## 7. Layer 2.5: Window Content

The original design had a standalone widget package (`Widget`, `Container`,
layout managers). It was deleted: retained `tui/ui` components replaced it, and
the window manager keeps only the minimal render contract it needs.

```go
// tui/wm/content.go
type WindowContent interface {
	// Render draws the content into the given surface, pre-sized to the
	// content's allocated space.
	Render(surf *surface.Surface, ctx *RenderContext)
}

type RenderContext struct {
	Theme     *theme.Theme
	Focused   bool // the window has keyboard focus
	SpinFrame int  // shared animation clock
}
```

`ui.ComponentWidget` adapts a component tree (normally a `ui.WindowFrame` plus
its body) to `WindowContent`, owning its own `ui.Host` for layout, paint and
focus. The compositor owns clipping and z-order; nothing consumes per-window
opacity. Shell-level rules are in [the shell host](shell-host.md).

## 8. Layer 3: Window Manager (tui/wm/)

### 8.1 FloatingWindow

```go
package wm

// FloatingWindow is an interactive surface that floats above other content.
//
// A FloatingWindow has:
// - A full-size content surface (where ui.WindowFrame + body render)
// - Frame interaction geometry used by the window manager
// - Interaction state (drag, resize, focus)
// - Z-order position among other windows
//
// The WM never paints visual chrome. ui.WindowFrame owns border, background,
// title, close glyph, padding, and the inner content rectangle.
type FloatingWindow struct {
    ID         string
    Title      string
    Content    WindowContent      // The content inside this window
    surf       *surface.Surface   // This window's placement/z-order surface
    x, y       int                // Position (screen coordinates)
    w, h       int                // Full WindowFrame size
    minW, minH int                // Minimum size
    maxW, maxH int                // Maximum size (0 = unlimited)
    decorated  bool               // Enables WindowFrame-aligned hit regions
    movable    bool               // Can be dragged
    resizable  bool               // Can be resized
    closable   bool               // Has close button
    modal      bool               // Blocks input to windows below
    visible    bool
    state      WindowState
    focused    bool

    // Interaction state (transient, per-frame)
    dragging    bool
    dragStartX  int
    dragStartY  int
    dragOffX    int
    dragOffY    int
    resizing    bool
    resizeEdge  Edge
    resizeStartW int
    resizeStartH int

    // Full content surface; WindowFrame calculates visual insets once.
    contentX, contentY int
    contentW, contentH int
}

type WindowState int

const (
    WindowNormal    WindowState = iota
    WindowMinimized
    WindowMaximized
    WindowFullscreen
)

type Edge int

const (
    EdgeNone   Edge = iota
    EdgeTop
    EdgeBottom
    EdgeLeft
    EdgeRight
    EdgeTopLeft
    EdgeTopRight
    EdgeBottomLeft
    EdgeBottomRight
)

// NewFloatingWindow creates a new floating window.
func NewFloatingWindow(id, title string, content WindowContent, w, h int, opts ...WindowOption) *FloatingWindow { ... }

// WindowOption configures a floating window.
type WindowOption func(*FloatingWindow)

func WithDecoration(decorated bool) WindowOption { ... }
func WithMovable(movable bool) WindowOption { ... }
func WithResizable(resizable bool) WindowOption { ... }
func WithClosable(closable bool) WindowOption { ... }
func WithModal(modal bool) WindowOption { ... }
func WithMinSize(w, h int) WindowOption { ... }
func WithMaxSize(w, h int) WindowOption { ... }

// Render draws full-size content into the window's surface.
// Then the surface is composited onto the screen by the WindowManager.
func (fw *FloatingWindow) Render(ctx *RenderContext) { ... }

// ContentBounds returns the full WM surface. WindowFrame owns inner bounds.
func (fw *FloatingWindow) ContentBounds() surface.Rect { ... }

// HitTestEdge returns which edge/corner a screen coordinate hits.
// Used for resize cursor and interaction.
func (fw *FloatingWindow) HitTestEdge(screenX, screenY int) Edge { ... }

// ContainsPoint reports whether a screen coordinate is within this window.
func (fw *FloatingWindow) ContainsPoint(screenX, screenY int) bool { ... }

// SetPosition moves the window to the given screen coordinates.
func (fw *FloatingWindow) SetPosition(x, y int) { ... }

// SetSize resizes the window (clamped to min/max size).
func (fw *FloatingWindow) SetSize(w, h int) { ... }

// Resize adjusts the window size based on a drag edge.
func (fw *FloatingWindow) ApplyResize(edge Edge, dx, dy int) { ... }

// Move adjusts the window position based on a drag offset.
func (fw *FloatingWindow) ApplyMove(dx, dy int) { ... }

```

### 8.2 WindowManager

```go
// WindowManager manages all floating windows.
//
// Responsibilities:
// - Maintain z-ordered list of windows
// - Route keyboard input to the focused window
// - Route mouse input to the correct window (hit-testing)
// - Handle drag and resize interactions
// - Manage focus chain (Tab / Shift+Tab between windows)
// - Compositing: render all windows into the compositor's surface tree
type WindowManager struct {
    windows    []*FloatingWindow  // All windows (creation order)
    zOrder     []*FloatingWindow  // Windows sorted by z-order (bottom to top)
    focused    *FloatingWindow    // Currently focused window
    compositor *compositor.Compositor
    
    // Screen layout: non-floating regions (background)
    background Widget  // The main content behind all floating windows
    
    // Interaction state
    activeDrag    *FloatingWindow  // Window being dragged
    activeResize  *FloatingWindow  // Window being resized
    resizeEdge    Edge
    dragStartX, dragStartY int
}

// NewWindowManager creates a window manager backed by the given compositor.
func NewWindowManager(comp *compositor.Compositor) *WindowManager { ... }

// AddWindow adds a floating window to the manager.
// The window is placed at the top of the z-order and receives focus.
func (wm *WindowManager) AddWindow(fw *FloatingWindow) { ... }

// RemoveWindow removes a floating window by ID.
// Focus moves to the next window in z-order.
func (wm *WindowManager) RemoveWindow(id string) { ... }

// Window returns a window by ID.
func (wm *WindowManager) Window(id string) *FloatingWindow { ... }

// Windows returns all windows.
func (wm *WindowManager) Windows() []*FloatingWindow { ... }

// TopWindow returns the topmost (focused) window.
func (wm *WindowManager) TopWindow() *FloatingWindow { ... }

// FocusWindow brings a window to the top and gives it focus.
func (wm *WindowManager) FocusWindow(id string) { ... }

// FocusNext moves focus to the next window in z-order (cyclic).
func (wm *WindowManager) FocusNext() { ... }

// FocusPrev moves focus to the previous window in z-order (cyclic).
func (wm *WindowManager) FocusPrev() { ... }

// BringToTop moves a window to the top of the z-order.
func (wm *WindowManager) BringToTop(id string) { ... }

// SendToBottom moves a window to the bottom of the z-order.
func (wm *WindowManager) SendToBottom(id string) { ... }

// HandleMouse processes a mouse event and routes it to the correct window.
// Returns the window that consumed the event (if any).
func (wm *WindowManager) HandleMouse(msg tea.MouseMsg) *FloatingWindow { ... }

// Render renders the background and all floating windows.
// Uses the compositor's surface tree for proper z-ordered compositing.
func (wm *WindowManager) Render(ctx *RenderContext) { ... }

// SetBackground sets the main content widget that renders behind all windows.
func (wm *WindowManager) SetBackground(w WindowContent) { ... }
```

### 8.3 Unified Window Chrome (`tui/ui/window_frame.go`)

`ui.WindowFrame` is the only visual chrome implementation. It paints semantic
theme tokens for background, rounded border, title, focus state, and close action,
and computes the body content rectangle. `ui.WindowFrameWidget` mounts native or
legacy bodies into FloatingWindow's full-size content surface and translates legacy
pointer events into that content rectangle. WM retains close/title/edge hit testing,
drag, resize, focus, placement, and z-order without drawing cells.

### 8.4 Window Interaction (interaction.go)

```go
// HandleDrag processes a mouse drag on a window.
// If the mouse is on the title bar, it starts/moves a window drag.
// Returns true if the event was consumed.
func HandleDrag(wm *WindowManager, msg tea.MouseMsg, fw *FloatingWindow) bool { ... }

// HandleResize processes a mouse drag on a window edge.
// If the mouse is on a border edge/corner, it starts/continues a resize.
// Returns true if the event was consumed.
func HandleResize(wm *WindowManager, msg tea.MouseMsg, fw *FloatingWindow) bool { ... }

// DetectEdge examines a mouse position and returns which window edge
// it's near (for resize cursor display).
func DetectEdge(fw *FloatingWindow, mouseX, mouseY int) Edge { ... }

// ClampPosition ensures a window stays within screen bounds.
// At least 3 rows and 10 columns must remain visible.
func ClampPosition(fw *FloatingWindow, screenW, screenH int) { ... }

// ClampSize ensures a window size is within min/max bounds.
func ClampSize(fw *FloatingWindow) { ... }
```

## 9. Integration with Bubble Tea

`App.View()` returns `tea.View` with a native `Surface`, not a full-frame ANSI
string. The local Bubble Tea/UV integration snapshots the borrowed drawable
synchronously before another Update; rendering never races a mutable front grid.

```text
App.View()
  1. configureShell + prepareShellSurface
                                      Host layout/focus; clean/no windows/no WM damage reuses frame
  2. compositor.BeginFrame()          clear root children and composite back buffer
  3. mount mainSurf + PaintRegions    one shell surface, theme-owned background
  4. windowManager.Render(ctx)        FloatingWindow content surfaces
  5. compositor.Composite()           flatten z-order and blit
  6. compositor.PresentSurface()      swap/publish cells → tea.View.Surface
Bubble Tea snapshot → UV Replace → changed-row terminal diff/output
```

UV `RenderBuffer.Replace` compares the latest complete grid to the last flushed
one and preserves damage across pending/coalesced views. Primary grapheme widths,
wide-cell continuations, colors and attributes cross directly; there is no
full-grid ANSI encode/decode cycle on the application path. Terminal escape
emission remains UV's responsibility, not a second direct writer in App.
Complete opaque `Blit` rows may use value-copy only after validating actual
backgrounds and complete grapheme spans. Transparent/default-background,
cropped and malformed rows keep the normal inheritance/normalization path.
For direct presentation, only Bubble Tea's owned producer `snapshotScreen`
explicitly offers `SetCellRow`: it copies the complete converted row into its
own storage and never retains compositor scratch. Generic UV screens still
receive individual `SetCell` calls, preserving their damage tracking. Every
new publication reads current source cells; there is no pointer-only frame cache.
The compositor advances an owner-scoped snapshot generation only when publishing
or resizing its completed grid. Repeated handoffs of that exact generation reuse
Bubble Tea's detached copy; different producers, new generations, geometry and
string/surface transitions always resnapshot. This avoids copying the whole grid
on coalesced presentation opportunities without dropping source mutations. A
fresh renderer after suspend/reset marks all retained rows damaged once, even
when they equal the last producer snapshot.
Physical terminal scrolling can change cells outside the producer's changed
spans. UV therefore expands latest-buffer damage over the entire physical scroll
region after applying a scroll, including source-unchanged exposed rows; it does
not disable scrolling or force an unrelated whole-frame repaint.

The renderer is demand-woken, not an unconditional 60 Hz empty-slot poll. A new
frame or queued terminal command brings the single timer forward to the active
frame deadline, paced by the last actual writer cost. The idle maximum remains
500 ms (2 FPS); a key after an idle interval does not wait for that idle deadline.
Update only enqueues mode/color/clear/print commands and snapshots the latest
complete frame. The renderer executes those commands and performs terminal I/O;
slow output cannot hold Update behind its writer mutex. Stopping or restoring a
terminal joins that exact renderer/timer lifetime before replacing it.
An immediate frame deadline is not source damage: admitted terminal keys retain
sibling regions, while the focused terminal Host owns selection/cursor/prediction
invalidation. Unknown messages keep conservative shell damage; global shortcuts
still own their layout and overlay transitions. Mutation remains immediate and
no terminal key waits for a presentation timer.

UV cell styles carry immutable tagged scalar terminal colors: default, basic
palette, indexed palette and RGB remain distinct even when they currently have
the same RGB components. External `color.Color` inputs normalize once at styled
text boundaries. Direct cells do not box colors per pixel or retain mutable
external color objects. The native pen appends SGR parameters directly into its
retained writer buffer, with no per-cell intermediate style strings or cache of
video colors. The single renderer also reuses its synchronized-output envelope
at actual observed capacity; it does not allocate another full video frame on
each flush. The envelope remains protected by the writer mutex and never enters
the producer handoff lifetime.

Floating windows remain `OverlayHost`-managed `FloatingWindow`s whose content is
a `ui.ComponentWidget`. Root Host retained regions do not change window ownership,
parent-local child bounds, z-order, transparency or grapheme-atomic blits. The
explicit `Present`/`GenerateANSI` path remains for diagnostics/string consumers.
See [the shell host](shell-host.md#retained-component-generation-paint) for source
invalidation, conservative fallbacks and the full `Host.Paint` oracle.

### Terminal image resources

Attachment images use Kitty Unicode placeholders rather than a second spatial
overlay renderer. The screen paints one-column cells containing the placeholder
and explicit row/column/high-ID diacritics; the foreground RGB encodes the lower
24 image-ID bits. Thus parent-local bounds, clipping, z-order and opaque window
occlusion remain ordinary cell composition. Original PNG pixels and alpha are
handled by the terminal, not sampled down to the character grid. The screen fits
geometry using reported cell pixel dimensions; unsupported terminals retain the
existing half-block body. Color-profile downsampling disables placeholders
because changing their foreground color would corrupt image identity.

`tea.NewKittyImage` copies bounded PNG bytes and assigns an immutable image ID on
the existing off-UI attachment worker. `tea.View.Images` is the complete desired
resource/virtual-placement set paired with the completed cell view. Bubble Tea
snapshots descriptors synchronously but uploads/reconciles only at its single
writer, against the last successfully flushed set. Dropped frames never upload
their resources. An unchanged image emits no extra commands or allocations;
geometry changes replace stable placement `p=1` without reuploading the PNG.
Every APC uses tmux passthrough when applicable. Failed writes retain exact
possibly transmitted IDs; close, screen changes, clear/reset and suspend/resume
delete or recreate only owned IDs, never terminal-wide image deletion. The
capability probe itself is an invisible one-pixel virtual placement with a
random reply identity, bounded timeout and exact-ID cleanup.

## 10. Key Design Decisions

### 10.1 Why Flat Array for CellBuffer (not [][]Cell)?

- **Memory locality**: A flat []Cell has better cache behavior than [][]Cell
- **Single allocation**: One big allocation instead of width+1 allocations
- **Simpler resize**: Realloc one slice instead of reallocating each row
- **Access**: cells[y*width+x] is a single index computation

### 10.2 Why Painter's Algorithm (not Z-Buffer)?

- Terminal cells have limited state — no depth value needed
- The number of overlapping windows is typically small (<20)
- Painter's algorithm (render back-to-front) is simpler and sufficient
- Z-buffering is overkill for the problem domain

### 10.3 Why Transparent Cells (not Alpha Per-Cell)?

- Terminal emulators don't support per-cell alpha natively
- "Transparent" (skip during compositing) handles 95% of use cases:
  - Empty areas of a floating window show content behind it
  - Rounded borders leave corners transparent
  - Window shadows could be simulated with special background colors
- Full alpha blending is available at the Surface level for semi-transparent overlays

### 10.4 Why Pool-Based Interning?

- Typical frame: ~20-50 unique style combinations, ~100-200 unique characters
- Interning reduces per-cell overhead from ~20+ bytes to ~8 bytes
- Zero per-frame allocations for styles (pool reuses IDs across frames)
- Pool reset prevents unbounded growth

### 10.5 Why Surface Hierarchy (not Flat Window List)?

- Enables virtually infinite nesting: window → tab bar → tab content → split pane → widget
- Children move with their parent (no manual coordinate tracking)
- Clip inheritance (children can't draw outside their parent)
- Z-order is relative to siblings (more intuitive than global z-order)
- Hit-testing traverses the tree naturally

### 10.6 Why Double Buffer?

- Tear-free rendering (no partial frames visible to user)
- Diff-based output (only changed cells produce ANSI sequences)
- Frame reuse (unchanged cells are simply not re-output)
- This is the standard approach used by Ratatui, Notcurses, and Claude Code Ink

## 11. Character Width Handling

All layout width uses the shared `VisibleWidth` grapheme profile; consumers do not
call uniseg/runewidth directly or add platform-specific corrections. CellBuffer
writes preserve the complete cluster and its measured primary width, plus width-0
continuation cells. Blit/occlusion is grapheme-atomic: covering a primary or tail
clears the old whole span. The direct-cell adapter passes measured widths to UV
rather than reparsing them from ANSI. See [terminal width](../reference/terminal-width.md).

## 12. Performance and correctness boundaries

Measure mutation, projection, component paint, composition, direct-cell snapshot
and presented output separately. A clean Host with no floating layers reuses the
exact completed publication after geometry/focus derivation; admitted terminal
keys invalidate only changed local selection, scroll, cursor or prediction ink.
New source damage still requires full-grid composition/snapshot work. Unknown messages,
geometry/mount/theme/buffer changes, overlapping regions and explicit clocks retain
conservative paint paths. There is no universal zero-allocation or all-leaves-
skipped claim. Compare direct/region output to the complete `Host.Paint` oracle,
including resize, unmount, overlays, transparency, theme and Unicode width.

Current benchmarks and enforced ceilings belong in [performance baselines](../reference/performance-baselines.md);
source implementation does not establish native emulator or remote-host latency
acceptance. Do not preserve old architectural estimates as measured results or
raise a gate to make a regression pass.
