// Package cell provides cell-level primitives for terminal rendering.
//
// The CharPool and StylePool types provide internment for frequently-used
// characters and style combinations. They are currently not used by the
// V1 compositor (which generates ANSI strings directly), but are designed
// for future optimization where cell metadata can be packed into compact
// integer IDs referencing pool entries, reducing per-cell memory from ~20
// bytes to ~8 bytes.
package cell

// ---------------------------------------------------------------------------
// CharPool — Rune Interning
// ---------------------------------------------------------------------------

// maxPoolSize is the maximum number of entries in a pool before it resets.
// This is the maximum value of uint16 (65535).
const maxPoolSize = 65535

// CharPool interns frequently-used runes to reduce memory.
//
// For a typical frame, most cells contain ASCII characters (space, letters,
// box-drawing). The pool maps rune → uint16 ID, avoiding per-cell rune
// storage overhead when the same character appears many times.
//
// When the pool exceeds maxPoolSize entries, it is automatically reset.
// This prevents unbounded memory growth while maintaining correctness
// (IDs are only valid within a single frame).
type CharPool struct {
	chars []rune          // ID → rune (slice index is the ID)
	index map[rune]uint16 // rune → ID
}

// NewCharPool creates a new empty character pool.
func NewCharPool() *CharPool {
	return &CharPool{
		chars: make([]rune, 0, 256),
		index: make(map[rune]uint16, 256),
	}
}

// Intern adds a rune to the pool and returns its ID.
// If the rune already exists in the pool, its existing ID is returned.
// If the pool is full (65535 entries), it is reset and the rune is
// interned with a new ID starting from 0.
func (p *CharPool) Intern(r rune) uint16 {
	if id, ok := p.index[r]; ok {
		return id
	}

	// Check for overflow. If we've hit the max, reset the pool.
	if len(p.chars) >= maxPoolSize {
		p.Reset()
	}

	id := uint16(len(p.chars))
	p.chars = append(p.chars, r)
	p.index[r] = id
	return id
}

// Get returns the rune for the given ID.
// Returns 0 (the zero rune) if the ID is out of range.
func (p *CharPool) Get(id uint16) rune {
	if int(id) >= len(p.chars) {
		return 0
	}
	return p.chars[id]
}

// Reset clears the pool for a new frame.
// Interned IDs from before the reset are no longer valid.
func (p *CharPool) Reset() {
	p.chars = p.chars[:0]
	for k := range p.index {
		delete(p.index, k)
	}
}

// Len returns the number of unique runes in the pool.
func (p *CharPool) Len() int {
	return len(p.chars)
}

// ---------------------------------------------------------------------------
// StylePool — Style Interning
// ---------------------------------------------------------------------------

// StyleEntry represents a complete cell style: foreground, background, and attributes.
// It is stored in the StylePool and retrieved by ID.
type StyleEntry struct {
	FG   Color
	BG   Color
	Attr Attrs
}

// StyleID is used as a map key for style interning.
// It has the same fields as StyleEntry and is comparable (all value types),
// making it suitable as a Go map key.
type StyleID struct {
	FG   Color
	BG   Color
	Attr Attrs
}

// StylePool interns color+attribute combinations.
//
// A typical frame has ~20-50 unique style combinations.
// Interning reduces each cell's style storage from ~20 bytes to ~4 bytes
// (just a uint16 style ID).
//
// When the pool exceeds maxPoolSize entries, it is automatically reset.
type StylePool struct {
	styles []StyleEntry     // ID → style
	index  map[StyleID]uint16 // style → ID
}

// NewStylePool creates a new empty style pool.
func NewStylePool() *StylePool {
	return &StylePool{
		styles: make([]StyleEntry, 0, 64),
		index:  make(map[StyleID]uint16, 64),
	}
}

// Intern adds a style to the pool and returns its ID.
// If the style already exists in the pool, its existing ID is returned.
// If the pool is full (65535 entries), it is reset and the style is
// interned with a new ID starting from 0.
func (p *StylePool) Intern(fg Color, bg Color, attr Attrs) uint16 {
	key := StyleID{FG: fg, BG: bg, Attr: attr}
	if id, ok := p.index[key]; ok {
		return id
	}

	// Check for overflow. If we've hit the max, reset the pool.
	if len(p.styles) >= maxPoolSize {
		p.Reset()
	}

	id := uint16(len(p.styles))
	p.styles = append(p.styles, StyleEntry{FG: fg, BG: bg, Attr: attr})
	p.index[key] = id
	return id
}

// Get returns the style for the given ID.
// Returns a zero-value StyleEntry if the ID is out of range.
func (p *StylePool) Get(id uint16) StyleEntry {
	if int(id) >= len(p.styles) {
		return StyleEntry{}
	}
	return p.styles[id]
}

// Reset clears the pool for a new frame.
// Interned IDs from before the reset are no longer valid.
func (p *StylePool) Reset() {
	p.styles = p.styles[:0]
	for k := range p.index {
		delete(p.index, k)
	}
}

// Len returns the number of unique styles in the pool.
func (p *StylePool) Len() int {
	return len(p.styles)
}
