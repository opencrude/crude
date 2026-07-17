# Compositing Engine Architecture

## 1. Vision

Build a production-grade multi-window compositing engine for the Crude TUI that:
- Supports **virtually infinite nesting** of windows, panes, and widgets
- Performs **per-cell compositing** (not naive whole-line rendering)
- Enables **floating windows** that can overlap, be dragged, resized, and stacked
- Supports **transparency** (seeing through empty cells to windows below)
- Handles **partial-line rendering** (Window A on left, Window B on right of the same row)
- Maintains **zero-allocation frame rendering** via pool-based interning
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
│  Layer 2.5: Widget Interface (tui/widget/)              │
│  (Widget, RenderContext, Container, adapters)            │
├                             ┤
│  Layer 2: Compositor (tui/compositor/)                   │
│  (Compositor, double-buffer, damage tracking, cell diff, │
│   ANSI output generation)                                │
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
│   └  output.go           # ANSI output generation from cell buffer
├  wm/                      # Layer 3: Window manager
│   ├  window.go           # FloatingWindow struct and lifecycle
│   ├  manager.go          # WindowManager (focus, z-order, hit-test)
│   └  interaction.go      # Drag, resize, mouse handling
├  ui/                      # Retained components and unified visual chrome
│   ├  window_frame.go     # Window/Dialog/Popover border, title, close paint
│   └  component_widget.go # Component/WindowFrame bridge into FloatingWindow
├  widget/                  # Layer 2.5: Widget interface
│   ├  widget.go           # Widget interface, RenderContext
│   └  container.go        # Container widget (holds children, layout)
```

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
// 4. Present() — diff back vs front, generate ANSI output, swap buffers
//
// The compositor owns the screen-sized cell buffers and manages the
// conversion from cell buffer → ANSI escape sequences for the terminal.
type Compositor struct {
    front     *surface.CellBuffer // Currently displayed (last frame sent to terminal)
    back      *surface.CellBuffer // Being rendered into (current frame)
    width     int                 // Terminal width in columns
    height    int                 // Terminal height in rows
    root      *surface.Surface    // Root of the surface tree
    damage    DamageRegion        // Tracks what changed this frame
    stylePool *cell.StylePool     // Interned styles for output optimization
    lastStyle cellStyleState      // Cursor for optimizing ANSI output (avoids redundant SGR)
}

type cellStyleState struct {
    fg   cell.Color
    bg   cell.Color
    attr cell.Attrs
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

// Present finalizes the frame:
// 1. Diffs back buffer vs front buffer (within damage region)
// 2. Generates optimized ANSI output for changed cells
// 3. Swaps front and back buffers
// 4. Returns the ANSI output string
//
// The returned string can be passed directly to Bubble Tea's View() return value.
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
// The diff only examines rows within the bounding box.
// On a typical streaming frame, only 10-20 rows change out of 50+,
// giving a 3-5x reduction in diff work.
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
// Performance note: this function is called every frame (16ms when streaming).
// The inner loop must be tight. Avoid string concatenation in the hot path;
// use a bytes.Buffer or strings.Builder.
func GenerateANSI(buf *surface.CellBuffer) string { ... }

// GenerateDiffANSI converts only the changed cells into ANSI output.
// Uses cursor positioning (CSI H) to jump to changed cells.
//
// This is more efficient than full-frame output when only a small
// portion of the screen changed. However, for Bubble Tea integration,
// we use full-frame output (GenerateANSI) since Bubble Tea writes
// the entire view each frame.
//
// For future direct-to-terminal rendering (bypassing Bubble Tea),
// this diff-based output would provide significant savings.
func GenerateDiffANSI(buf *surface.CellBuffer, diff *DiffResult) string { ... }

// cellToANSI writes the ANSI escape sequences for a single cell's style.
// Compares against the current style state and only emits changes.
// Returns the updated style state.
func cellToANSI(sb *strings.Builder, c cell.Cell, cur *cellStyleState) { ... }
```

## 7. Layer 2.5: Widget Interface (tui/widget/)

### 7.1 Widget

```go
package widget

// Widget is the interface that all renderable components implement.
//
// This replaces the informal "has a View() string" pattern used currently.
// Widgets now render into a Surface (cell buffer) rather than returning
// ANSI strings. This enables:
// - Compositing at the cell level
// - Overlapping widgets
// - Transparency
// - Proper clipping
// - Damage tracking
//
// A Widget is also a tea.Model — it can receive Bubble Tea messages
// and return commands. This integrates with the existing event system.
type Widget interface {
    // Init initializes the widget (tea.Model.Init).
    Init() tea.Cmd

    // Update handles a Bubble Tea message (tea.Model.Update).
    // Returns the (possibly modified) widget and any commands.
    Update(msg tea.Msg) (Widget, tea.Cmd)

    // Render draws the widget into the given surface.
    // The surface is pre-sized to the widget's allocated space.
    // The widget should fill the surface completely.
    //
    // This replaces View() string. Instead of returning an ANSI string,
    // the widget writes cells directly into the surface's buffer.
    Render(surf *surface.Surface, ctx *RenderContext)

    // Measure reports the minimum and preferred size of the widget.
    // availW, availH: the maximum space available.
    // Returns: (preferredWidth, preferredHeight).
    //
    // This is used by the layout engine to allocate space.
    Measure(availW, availH int) (int, int)
}

// RenderContext carries rendering state through the widget tree.
//
// It is passed to every Widget.Render() call. Widgets can modify
// the context for their children (e.g., push a clip rect, change
// opacity) and must restore it when done.
type RenderContext struct {
    Theme     *theme.Theme
    ClipStack *surface.ClipStack
    Opacity   float32       // Current opacity multiplier (0.0-1.0)
    Focused   bool          // Whether this widget has keyboard focus
    SpinFrame int           // Current animation frame (for spinners, blink)
}

// PushClip pushes a clip rect and returns a function to pop it.
// Usage: defer ctx.PushClip(r)()
func (ctx *RenderContext) PushClip(r surface.Rect) func() { ... }

// WithOpacity returns a copy of the context with modified opacity.
// The new opacity is the product of the current and the given factor.
func (ctx *RenderContext) WithOpacity(factor float32) *RenderContext { ... }

// WithFocus returns a copy of the context with the given focus state.
func (ctx *RenderContext) WithFocus(focused bool) *RenderContext { ... }
```

### 7.2 Container

```go
// Container is a widget that holds and layouts child widgets.
//
// Container is the foundation for nested composition. A Container
// creates a sub-surface for each child and renders children into
// their allocated sub-surfaces. The sub-surfaces are then composited
// onto the container's own surface.
//
// This enables virtually infinite nesting:
// App → Container → FloatingWindow → Container → TabBar + Content
//   → Container → SplitPane → Container → Widget
type Container struct {
    id       string
    children []ChildWidget
    layout   Layout
    surf     *surface.Surface // Own surface (reused across frames)
}

// ChildWidget is a widget with position/size within its parent container.
type ChildWidget struct {
    Widget  Widget
    X, Y    int     // Position within parent
    W, H    int     // Allocated size
    ZOrder  int     // Z-order among siblings
    Visible bool
}

// Layout determines how children are positioned within a Container.
type Layout interface {
    // Arrange computes positions and sizes for all children
    // given the available space.
    Arrange(children []ChildWidget, availW, availH int)
}

// StackLayout arranges children in a vertical or horizontal stack.
type StackLayout struct {
    Direction Direction // Vertical or Horizontal
    Gap      int        // Spacing between children
}

type Direction int

const (
    Vertical   Direction = iota
    Horizontal
)

// OverlayLayout stacks all children on top of each other (same position, same size).
// Children are composited in z-order. The topmost child gets focus.
type OverlayLayout struct{}

// Render implements Widget. Creates sub-surfaces for each child,
// calls child.Render(), and composites them onto this container's surface.
func (c *Container) Render(surf *surface.Surface, ctx *RenderContext) { ... }

// Measure implements Widget. Sum of child sizes (stack) or max of child sizes (overlay).
func (c *Container) Measure(availW, availH int) (int, int) { ... }
```

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
    content    widget.Widget      // The widget inside this window
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
func NewFloatingWindow(id, title string, content widget.Widget, w, h int, opts ...WindowOption) *FloatingWindow { ... }

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
func (fw *FloatingWindow) Render(ctx *widget.RenderContext) { ... }

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
func (wm *WindowManager) Render(ctx *widget.RenderContext) { ... }

// SetBackground sets the main content widget that renders behind all windows.
func (wm *WindowManager) SetBackground(w widget.Widget) { ... }
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

## 9. Integration Strategy

### 9.1 Integration with Bubble Tea

The compositing engine integrates with Bubble Tea through the `View()` method:

```
Current flow:
  App.View() → build []string of ANSI lines → join → return

New flow:
  App.View() →
    1. compositor.BeginFrame()
    2. windowManager.Render(ctx)
       → background widget renders into root surface
       → each FloatingWindow renders into its own surface
       → surfaces composited into back buffer
    3. return compositor.Present()
       → diff back vs front buffer
       → generate ANSI string
       → swap buffers
```

The return value of `View()` is still a string — Bubble Tea doesn't need to change.
The difference is that the string is now generated from a cell-level composited buffer
rather than from concatenated ANSI strings.

### 9.2 Widget Adapter

To incrementally migrate existing widgets (which return `View() string`) to the
new `Render(surf, ctx)` interface, we provide an adapter:

```go
// ANSIAdapter wraps a legacy View()-returning widget into the Widget interface.
// It renders the widget's View() output into a surface by parsing ANSI escape
// sequences and writing the corresponding cells.
//
// This enables existing widgets to work within the compositing system
// without being rewritten. Widgets can be migrated one by one.
type ANSIAdapter struct {
    model tea.Model  // Any Bubble Tea model with View()
}

// NewANSIAdapter creates an adapter for a legacy widget.
func NewANSIAdapter(model tea.Model) *ANSIAdapter { ... }

// Render implements Widget. Calls model.View(), parses the ANSI string,
// and writes cells into the surface.
func (a *ANSIAdapter) Render(surf *surface.Surface, ctx *RenderContext) {
    view := a.model.View()
    ansi.RenderToSurface(surf, view)
}
```

The `ansi.RenderToSurface` function parses ANSI escape sequences from a string
and writes the corresponding cells into a surface. This is a bridge between
the old and new rendering models.

### 9.3 Rendering Pipeline Redesign

The existing `App.View()` function (currently 669 lines) will be simplified:

```
func (m *App) View() tea.View {
    if m.width == 0 || m.height == 0 {
        return tea.NewView("")
    }
    
    // 1. Begin frame
    m.compositor.BeginFrame()
    
    // 2. Render background (main layout: header, chat, input, status)
    m.rootWidget.Render(m.compositor.Root(), m.renderCtx)
    
    // 3. Render floating windows
    m.windowManager.Render(m.compositor.Root(), m.renderCtx)
    
    // 4. Composite and present
    output := m.compositor.Present()
    return tea.NewView(output)
}
```

The rootWidget is a Container that arranges the main layout:
- Header bar
- Agent tab bar (if visible)
- Chat area (MessageList)
- Input area
- Status bar

Floating windows are rendered on top of this background.

### 9.4 Replacement of Overlay System

The current overlay system (Settings, Confirm, Question, YOLO dialogs) will be
replaced by FloatingWindows:

```go
// Instead of m.setMode(ModeSettings), which currently opens an overlay tab:
func (m *App) openSettings() {
    settingsWidget := newSettingsWidget(m.settings)
    win := wm.NewFloatingWindow("settings", "Settings", settingsWidget,
        60, 30,
        wm.WithDecoration(true),
        wm.WithMovable(true),
        wm.WithResizable(true),
        wm.WithClosable(true),
        wm.WithBorderStyle(wm.BorderRounded),
        wm.WithMinSize(40, 20),
    )
    win.SetPosition(centerX, centerY) // Center on screen
    m.wm.AddWindow(win)
}
```

Each modal dialog becomes a FloatingWindow:
- Settings → FloatingWindow with settings form widget
- Confirm → FloatingWindow with confirm dialog widget
- Question → FloatingWindow with question form widget
- YOLO → FloatingWindow with YOLO dialog widget
- FilePicker → FloatingWindow with file picker widget
- TextOverlay → FloatingWindow with text editor widget

### 9.5 ANSI Parser for Adapter (New File)

To make the ANSIAdapter work, we need a function that parses ANSI escape
sequences from a string and writes cells into a surface:

```go
// RenderToSurface parses an ANSI string and writes the cells into a surface.
// Handles:
// - SGR (color/attribute) sequences: \x1b[...m
// - Regular characters → cells
// - Wide characters → wide cells + continuation
// - Newlines → row advancement
// - Cursor positioning sequences (for future diff-based output)
func RenderToSurface(surf *surface.Surface, ansi string) { ... }
```

This is essentially a "reverse" of the output.go GenerateANSI function.

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

The existing codebase already uses `github.com/rivo/uniseg` for character width
measurement. The compositing engine will continue using this:

```go
import "github.com/rivo/uniseg"

// measureWidth returns the display width of a grapheme cluster.
// Uses uniseg.GraphemeClusterCount and uniseg.StringWidth.
func measureWidth(s string) int {
    return uniseg.StringWidth(s)
}
```

All CellBuffer.WriteString operations use this for correct wide character handling.
When writing a wide character (width=2), the engine:
1. Writes Cell{Char: ch, Width: 2} at position N
2. Writes Cell{Char: 0, Width: 0} (continuation) at position N+1
3. If a wide char would be split at the buffer edge, it's truncated

## 12. Performance Budget

At 60fps (16ms per frame), the compositor must complete in under 8ms
(leaving 8ms for widget rendering and event handling).

For a 120×40 terminal (4,800 cells):
- CellBuffer.Clear: ~5μs (fill 4800 cells)
- Surface tree traversal: ~1μs (typically <20 surfaces)
- Blit operations: ~50μs (compositing ~4800 cells)
- Diff: ~100μs (comparing 4800 cells)
- ANSI output: ~200μs (generating ~4800 cell sequences)
- Total: ~356μs — well within budget

For a 200×60 terminal (12,000 cells):
- Total estimated: ~900μs — still well within 8ms

## 13. Future Optimizations (Not in V1)

These are intentionally deferred to keep V1 focused and correct:

1. **Diff-based ANSI output** — Only output changed cells with cursor positioning.
   Requires bypassing Bubble Tea's View() model for direct terminal writes.

2. **Multiple damage rectangles** — Track separate dirty regions instead of
   a single bounding box. Reduces diff work for scattered changes.

3. **Occlusion culling** — Skip rendering surfaces that are fully covered
   by windows above them. Requires bounding-box intersection tests.

4. **Blit optimization** — Copy unchanged subtrees from previous frame
   (like Claude Code Ink's blit optimization).

5. **Parallel compositing** — Use goroutines to composite non-overlapping
   surface regions in parallel.

6. **Layout cache** — Cache Measure/Arrange results and only recalculate
   when children change.

7. **Incremental surface rendering** — Only re-render widgets that are
   marked dirty, reuse previous frame's surface for unchanged widgets.
