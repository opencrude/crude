package editor

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Buffer is the data model for a text file. It owns the PieceTable (the
// actual text content), the file path, the dirty flag, and cached LSP
// diagnostics.
//
// Multiple Editor views can share the same Buffer (for split views, diff
// views, or scratch buffers). The Buffer is reference-counted through the
// BufferManager — it is only closed when the last view releases it.
//
// Buffer is the "what IS" layer — it has no knowledge of cursor position,
// scroll offset, undo history, or any view-specific state. Those live on
// the Editor.
type Buffer struct {
	// ID is the canonical identifier. For file-backed buffers, this is the
	// absolute file path. For scratch buffers, it's "scratch:<uuid>".
	ID string

	// Path is the file path on disk. Empty for scratch buffers.
	Path string

	// Title is the display name (typically filepath.Base(Path)).
	Title string

	buf *PieceTable

	dirty bool

	diags   []LSPDiagnostic
	diagsMu sync.RWMutex
}

// Bytes returns the buffer's text content as a byte slice.
func (b *Buffer) Bytes() []byte { return b.buf.Bytes() }

// LineCount returns the number of lines in the buffer.
func (b *Buffer) LineCount() int { return b.buf.LineCount() }

// Line returns the text of a specific line (0-indexed).
func (b *Buffer) Line(i int) string {
	raw, err := b.buf.Line(i)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Insert inserts text at a Position. Marks the buffer dirty.
func (b *Buffer) Insert(pos Position, text []byte) {
	_, _ = b.buf.Insert(pos, text)
	b.dirty = true
}

// Delete removes a range. Marks the buffer dirty.
func (b *Buffer) Delete(start, end Position) {
	_, _ = b.buf.Delete(start, end)
	b.dirty = true
}

// Save writes the buffer to disk. For scratch buffers (Path == ""),
// returns os.ErrInvalid.
func (b *Buffer) Save() error {
	if b.Path == "" {
		return os.ErrInvalid
	}
	if err := b.buf.Save(b.Path); err != nil {
		return err
	}
	b.dirty = false
	return nil
}

// MarkDirty marks the buffer as having unsaved changes.
func (b *Buffer) MarkDirty() { b.dirty = true }

// IsDirty returns true if the buffer has unsaved changes.
func (b *Buffer) IsDirty() bool { return b.dirty }

// SetDiagnostics stores LSP diagnostics for this buffer. Thread-safe.
func (b *Buffer) SetDiagnostics(diags []LSPDiagnostic) {
	b.diagsMu.Lock()
	defer b.diagsMu.Unlock()
	b.diags = diags
}

// Diagnostics returns the cached LSP diagnostics for this buffer.
func (b *Buffer) Diagnostics() []LSPDiagnostic {
	b.diagsMu.RLock()
	defer b.diagsMu.RUnlock()
	result := make([]LSPDiagnostic, len(b.diags))
	copy(result, b.diags)
	return result
}

// BufferManager owns all open buffers. It is the single source of truth
// for "which buffers exist" and "what is their text content". Editors
// reference buffers by ID (typically the file path).
//
// When an editor tab opens, it asks the manager for the buffer (creating
// it if needed). Multiple editors can share the same buffer (split view)
// without duplicating the PieceTable.
type BufferManager struct {
	mu      sync.RWMutex
	buffers map[string]*Buffer
}

// NewBufferManager creates an empty buffer manager.
func NewBufferManager() *BufferManager {
	return &BufferManager{
		buffers: make(map[string]*Buffer),
	}
}

// OpenFile loads a file from disk into a Buffer and registers it. If the
// buffer already exists (file already open in another tab/view), it returns
// the existing buffer — no reload, no duplicate.
func (m *BufferManager) OpenFile(path string) (*Buffer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	if buf, ok := m.buffers[abs]; ok {
		m.mu.RUnlock()
		return buf, nil
	}
	m.mu.RUnlock()

	pt := NewPieceTable()
	if err := pt.Load(abs); err != nil {
		return nil, err
	}

	buf := &Buffer{
		ID:    abs,
		Path:  abs,
		Title: filepath.Base(abs),
		buf:   pt,
	}

	m.mu.Lock()
	// Double-check after acquiring write lock.
	if existing, ok := m.buffers[abs]; ok {
		m.mu.Unlock()
		return existing, nil
	}
	m.buffers[abs] = buf
	m.mu.Unlock()
	return buf, nil
}

// CreateScratch creates a new unnamed buffer with no file path. Used for
// diff views, temporary notes, or "untitled" buffers.
func (m *BufferManager) CreateScratch() *Buffer {
	id := "scratch:" + nextScratchID()
	buf := &Buffer{
		ID:  id,
		buf: NewPieceTable(),
	}
	m.mu.Lock()
	m.buffers[id] = buf
	m.mu.Unlock()
	return buf
}

// Get returns the buffer with the given ID, or nil if not found.
func (m *BufferManager) Get(id string) *Buffer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buffers[id]
}

// Close removes a buffer from the manager. If other editors are still
// viewing it, those references become nil-safe (they check IsOpen()).
// Call this when the last tab/view for a buffer is closed.
func (m *BufferManager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buffers, id)
}

// scratchCounter produces unique IDs for scratch buffers.
var scratchCounter int

func nextScratchID() string {
	scratchCounter++
	return strconv.Itoa(scratchCounter)
}
