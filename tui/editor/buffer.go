package editor

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"crude/document"
)

type source int

const (
	originalBuffer source = iota
	addBuffer
)

type piece struct {
	buffer source
	start  int
	length int
}

type PieceTable struct {
	lineChanges [32]bufferLineChange
	original    []byte
	add         []byte
	pieces      []piece

	filePath string

	cachedBytes []byte
	lineOffsets []int
	cacheValid  bool
	revision    uint64
	// mutationObserver is a UI-local notification, never a backend call.
	mutationObserver func(document.Edit)
	// One dispatcher notifies passive private views after the canonical log.
	// Listeners never mutate recursively or replace mutationObserver.
	editOwner    *Editor
	snippetViews map[*Editor]struct{}
}

type bufferLineChange struct {
	revision           uint64
	start, end, delta  int
	byteStart, byteEnd int // relative to the pre-edit start line
	text               string
	inline             bool
}

func (pt *PieceTable) recordLineChange(start, end, delta int, edit document.Edit) {
	// Only inline edits need provisional colour mapping within a line. Do not
	// retain large paste payloads in this bounded geometry/decorations journal.
	inline := start == end && delta == 0 && len(edit.Text) <= 4096
	text := ""
	if inline {
		text = edit.Text
	}
	pt.lineChanges[pt.revision%uint64(len(pt.lineChanges))] = bufferLineChange{
		revision: pt.revision, start: start, end: end, delta: delta,
		byteStart: edit.Start - pt.lineOffsets[start], byteEnd: edit.End - pt.lineOffsets[start], text: text, inline: inline,
	}
}

func NewPieceTable() *PieceTable {
	return &PieceTable{
		original: []byte{},
		add:      []byte{},
		pieces:   []piece{{buffer: originalBuffer, start: 0, length: 0}},
	}
}

func (pt *PieceTable) Load(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			pt.filePath = filePath
			pt.original = []byte{}
			pt.add = []byte{}
			pt.pieces = []piece{{buffer: originalBuffer, start: 0, length: 0}}
			pt.revision++
			pt.invalidateCache()
			return nil
		}
		return err
	}

	pt.original = content
	pt.add = []byte{}
	pt.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(content)}}
	pt.filePath = filePath
	pt.revision++
	pt.invalidateCache()
	return nil
}

// ReplaceAll discards the current buffer contents and replaces them with
// the supplied text. Savepoint and view state belong to Buffer and Editor.
//
// We keep the filePath unchanged so the editor stays bound to the same
// file. The "didChange" notification must be fired by the caller; this
// function only touches the buffer.
func (pt *PieceTable) ReplaceAll(text string) {
	size := 0
	for _, p := range pt.pieces {
		size += p.length
	}
	pt.original = []byte(text)
	pt.add = []byte{}
	pt.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(text)}}
	pt.revision++
	pt.invalidateCache()
	pt.observeMutation(document.Edit{Start: 0, End: size, Text: text})
}

func (pt *PieceTable) observeMutation(edit document.Edit) {
	if pt.mutationObserver != nil {
		pt.mutationObserver(edit)
	}
	for view := range pt.snippetViews {
		view.observeSnippetMutation(edit, pt.editOwner)
	}
}

func (pt *PieceTable) ensureCache() {
	if pt.cacheValid {
		return
	}

	var result bytes.Buffer
	for _, p := range pt.pieces {
		if p.buffer == originalBuffer {
			if p.start+p.length <= len(pt.original) {
				result.Write(pt.original[p.start : p.start+p.length])
			}
		} else {
			if p.start+p.length <= len(pt.add) {
				result.Write(pt.add[p.start : p.start+p.length])
			}
		}
	}
	pt.cachedBytes = result.Bytes()

	offsets := make([]int, 0, 64)
	offsets = append(offsets, 0)
	for i, b := range pt.cachedBytes {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	pt.lineOffsets = offsets
	pt.cacheValid = true
}

func (pt *PieceTable) invalidateCache() {
	pt.cacheValid = false
	pt.cachedBytes = nil
	pt.lineOffsets = nil
}

func (pt *PieceTable) Bytes() []byte {
	pt.ensureCache()
	out := make([]byte, len(pt.cachedBytes))
	copy(out, pt.cachedBytes)
	return out
}

func (pt *PieceTable) Lines() [][]byte {
	pt.ensureCache()
	return bytes.Split(pt.cachedBytes, []byte{'\n'})
}

func (pt *PieceTable) Line(index int) ([]byte, error) {
	line, err := pt.lineView(index)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(line))
	copy(out, line)
	return out, nil
}

func (pt *PieceTable) lineView(index int) ([]byte, error) {
	pt.ensureCache()
	if index < 0 || index >= len(pt.lineOffsets) {
		return nil, fmt.Errorf("line index out of bounds")
	}
	start := pt.lineOffsets[index]
	var end int
	if index+1 < len(pt.lineOffsets) {
		end = pt.lineOffsets[index+1] - 1
		// CRLF is one source terminator, not a visible carriage-return cell.
		// Keep both bytes in the piece table; line content excludes both.
		if end > start && pt.cachedBytes[end-1] == '\r' {
			end--
		}
	} else {
		end = len(pt.cachedBytes)
	}
	if end < start {
		end = start
	}
	return pt.cachedBytes[start:end], nil
}

func (pt *PieceTable) LineCount() int {
	pt.ensureCache()
	return len(pt.lineOffsets)
}

func (pt *PieceTable) positionToOffset(pos Position) int {
	pt.ensureCache()
	if pos.Line >= len(pt.lineOffsets) {
		return len(pt.cachedBytes)
	}
	line := max(0, pos.Line)
	content, _ := pt.lineView(line)
	return pt.lineOffsets[line] + VisualColToByteOffset(string(content), pos.Col, tabWidth)
}

func (pt *PieceTable) GetText(start, end Position) string {
	startOff := pt.positionToOffset(start)
	endOff := pt.positionToOffset(end)
	if startOff > endOff {
		startOff, endOff = endOff, startOff
	}
	if startOff == endOff {
		return ""
	}
	bytes := pt.Bytes()
	if startOff < 0 {
		startOff = 0
	}
	if endOff > len(bytes) {
		endOff = len(bytes)
	}
	return string(bytes[startOff:endOff])
}

func (pt *PieceTable) Insert(pos Position, text []byte) (EditInfo, error) {
	return pt.insertAtOffset(pt.positionToOffset(pos), text)
}

// insertAtOffset is the exact-byte mutation primitive used by edit history.
// Unlike visual positions, offsets can express the inverse of an insertion
// that joined an existing grapheme (for example, an appended combining mark).
func (pt *PieceTable) insertAtOffset(offset int, text []byte) (EditInfo, error) {
	if len(text) == 0 {
		return EditInfo{}, nil
	}
	pt.ensureCache()
	offset = min(len(pt.cachedBytes), max(0, offset))
	return EditInfo{}, pt.applyOffsets([]document.Edit{{Start: offset, End: offset, Text: string(text)}})
}

func (pt *PieceTable) Delete(start, end Position) (EditInfo, error) {
	return pt.deleteOffsets(pt.positionToOffset(start), pt.positionToOffset(end))
}

// deleteOffsets preserves exact source byte spans, including inverse edits
// inside joined graphemes. All byte mutations share the same checked owner.
func (pt *PieceTable) deleteOffsets(start, end int) (EditInfo, error) {
	pt.ensureCache()
	start = min(len(pt.cachedBytes), max(0, start))
	end = min(len(pt.cachedBytes), max(0, end))
	if start >= end {
		return EditInfo{}, nil
	}
	return EditInfo{}, pt.applyOffsets([]document.Edit{{Start: start, End: end}})
}

func (pt *PieceTable) checkOffsets(edits []document.Edit) error {
	if len(edits) > document.MaxEdits {
		return document.ErrLimit
	}
	pt.ensureCache()
	for i, edit := range edits {
		if edit.Start < 0 || edit.End < edit.Start || edit.End > len(pt.cachedBytes) ||
			i > 0 && (edit.Start < edits[i-1].End || edit.Start == edits[i-1].Start) {
			return fmt.Errorf("invalid source byte edit range")
		}
	}
	return nil
}

// applyOffsets commits one sorted, disjoint original-coordinate vector. It is
// the sole piece-splice writer for Insert, Delete and recorded editor batches.
// Validation precedes every effect; text and geometry advance once, not once
// per mirror. The existing observer still receives every primitive in reverse
// sequential coordinates, after the complete source has committed.
func (pt *PieceTable) applyOffsets(edits []document.Edit) error {
	if err := pt.checkOffsets(edits); err != nil {
		return err
	}
	before := pt.cachedBytes
	changed := make([]document.Edit, 0, len(edits))
	delta := 0
	for _, edit := range edits {
		if bytes.Equal(before[edit.Start:edit.End], []byte(edit.Text)) {
			continue
		}
		changed = append(changed, edit)
		delta += strings.Count(edit.Text, "\n") - bytes.Count(before[edit.Start:edit.End], []byte{'\n'})
	}
	if len(changed) == 0 {
		return nil
	}
	lineAt := func(offset int) int {
		return sort.Search(len(pt.lineOffsets), func(i int) bool { return pt.lineOffsets[i] > offset }) - 1
	}
	first, last := changed[0], changed[len(changed)-1]
	startLine, endLine := lineAt(first.Start), lineAt(last.End)
	next := make([]piece, 0, len(pt.pieces)+2*len(changed))
	appendPiece := func(p piece) {
		if p.length == 0 {
			return
		}
		if n := len(next); n > 0 && next[n-1].buffer == p.buffer && next[n-1].start+next[n-1].length == p.start {
			next[n-1].length += p.length
		} else {
			next = append(next, p)
		}
	}
	index, base := 0, 0
	appendSource := func(start, end int) {
		for index < len(pt.pieces) && base+pt.pieces[index].length <= start {
			base += pt.pieces[index].length
			index++
		}
		for start < end {
			p := pt.pieces[index]
			n := min(end-start, base+p.length-start)
			appendPiece(piece{buffer: p.buffer, start: p.start + start - base, length: n})
			start += n
			if start == base+p.length {
				base += p.length
				index++
			}
		}
	}
	at := 0
	for _, edit := range changed {
		appendSource(at, edit.Start)
		if edit.Text != "" {
			addStart := len(pt.add)
			pt.add = append(pt.add, edit.Text...)
			appendPiece(piece{buffer: addBuffer, start: addStart, length: len(edit.Text)})
		}
		at = edit.End
	}
	appendSource(at, len(before))
	pt.pieces = next
	pt.revision++
	summary := first
	if len(changed) > 1 {
		summary.End, summary.Text = last.End, ""
	}
	pt.recordLineChange(startLine, endLine, delta, summary)
	if len(changed) > 1 {
		// Multiple substitutions do not claim a single inline colour mapping.
		pt.lineChanges[pt.revision%uint64(len(pt.lineChanges))].inline = false
	}
	pt.invalidateCache()
	for i := len(changed) - 1; i >= 0; i-- {
		pt.observeMutation(changed[i])
	}
	return nil
}

func (pt *PieceTable) FilePath() string {
	return pt.filePath
}

func (pt *PieceTable) Revision() uint64 { return pt.revision }
