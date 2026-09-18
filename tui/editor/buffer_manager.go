package editor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// Buffer is the data model for a text file. It owns the PieceTable (the
// actual text content), the file path, the dirty flag, and cached LSP
// diagnostics.
//
// Multiple Editor views can share the same Buffer. The viewmodel removes its
// manager entry only when the last managed tab closes.
//
// Buffer is the "what IS" layer — it has no knowledge of cursor position,
// scroll offset, undo history, or any view-specific state. Those live on
// the Editor.
type Buffer struct {
	// ID identifies this buffer lifetime, never a reusable file path.
	ID string

	// Path is the file path on disk. Empty for scratch buffers.
	Path string

	// Title is the display name (typically filepath.Base(Path)).
	Title string

	buf *PieceTable

	diskHash       [sha256.Size]byte
	diskExists     bool
	diskGeneration uint64
	dirty          bool

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
	b.NoteEdit()
}

// Delete removes a range. Marks the buffer dirty.
func (b *Buffer) Delete(start, end Position) {
	_, _ = b.buf.Delete(start, end)
	b.NoteEdit()
}

// BufferSnapshot is an immutable revision selected for a workspace save.
type BufferSnapshot struct {
	ID             string
	Path           string
	Data           []byte
	Revision       uint64
	ExpectedHash   [sha256.Size]byte
	ExpectedExists bool
}

func (b *Buffer) Snapshot() BufferSnapshot {
	if b == nil || b.buf == nil {
		return BufferSnapshot{}
	}
	return BufferSnapshot{
		ID: b.ID, Path: b.Path, Data: b.buf.Bytes(), Revision: b.buf.Revision(),
		ExpectedHash: b.diskHash, ExpectedExists: b.diskExists,
	}
}

// AcceptSave advances the disk baseline for exactly the committed snapshot.
// Newer edits remain dirty.
func (b *Buffer) AcceptSave(revision uint64, diskHash [sha256.Size]byte) {
	if b == nil || b.buf == nil {
		return
	}
	b.diskHash = diskHash
	b.diskExists = true
	b.diskGeneration++
	b.dirty = b.buf.Revision() != revision
}

// ReplaceContents swaps the entire document text and marks the buffer dirty.
// Used by project-wide replace so an open editor stays the source of truth.
func (b *Buffer) ReplaceContents(text string) {
	b.buf.ReplaceAll(text)
	b.NoteEdit()
}

// IsDirty returns true if the buffer has unsaved changes.
func (b *Buffer) IsDirty() bool {
	return b != nil && b.dirty
}

func (b *Buffer) NoteEdit() {
	if b != nil {
		b.dirty = true
	}
}

func (b *Buffer) ReconcileSavepoint() {
	if b == nil {
		return
	}
	b.buf.ensureCache()
	b.dirty = sha256.Sum256(b.buf.cachedBytes) != b.diskHash
}

func newBuffer(pt *PieceTable, path string, exists bool) *Buffer {
	pt.ensureCache()
	hash := sha256.Sum256(pt.cachedBytes)
	return &Buffer{ID: nextScratchID(), Path: path, Title: filepath.Base(path), buf: pt,
		diskHash: hash, diskExists: exists}
}

// ApplyDisk replaces the shared text, not the PieceTable identity held by views.
func (b *Buffer) ApplyDisk(data []byte) {
	b.buf.ReplaceAll(string(data))
	b.AcceptSave(b.buf.Revision(), sha256.Sum256(data))
}

func (b *Buffer) MatchesDisk(hash [sha256.Size]byte) bool { return b.diskExists && b.diskHash == hash }

func (b *Buffer) NoteDiskMissing() {
	if b == nil {
		return
	}
	b.diskHash = [sha256.Size]byte{}
	b.diskExists = false
	b.diskGeneration++
	b.dirty = true
}

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
// reference stable Buffer pointers; the map is keyed by canonical file path
// for files and lifetime ID for scratches.
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
	_, statErr := os.Stat(abs)
	exists := statErr == nil
	buf := newBuffer(pt, abs, exists)

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
	buf := newBuffer(NewPieceTable(), "", false)
	id := buf.ID
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

// Close removes the manager's path/ID entry. Existing views retain the Buffer;
// callers invoke it only when the last managed tab closes.
func (m *BufferManager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buffers, id)
}

func (m *BufferManager) Retarget(oldPath, newPath string) error {
	oldAbs, err := filepath.Abs(oldPath)
	if err != nil {
		return err
	}
	newAbs, err := filepath.Abs(newPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	buffer := m.buffers[oldAbs]
	if buffer == nil {
		return fmt.Errorf("buffer not open: %s", oldPath)
	}
	if existing := m.buffers[newAbs]; existing != nil && existing != buffer {
		return fmt.Errorf("buffer already open: %s", newPath)
	}
	delete(m.buffers, oldAbs)
	buffer.Path, buffer.Title = newAbs, filepath.Base(newAbs)
	buffer.buf.filePath = newAbs
	m.buffers[newAbs] = buffer
	return nil
}

// scratchCounter produces unique IDs for scratch buffers.
var scratchCounter atomic.Uint64

func nextScratchID() string {
	return strconv.FormatUint(scratchCounter.Add(1), 10)
}
