package editor

import (
	"bytes"
	"fmt"
	"os"

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

func (pt *PieceTable) findPiece(offset int) (int, int) {
	currentOffset := 0
	for i, p := range pt.pieces {
		if currentOffset+p.length > offset {
			return i, offset - currentOffset
		}
		currentOffset += p.length
	}
	if len(pt.pieces) == 0 {
		return 0, 0
	}
	return len(pt.pieces) - 1, pt.pieces[len(pt.pieces)-1].length
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
	line := pt.positionAtOffset(offset).Line

	if len(pt.pieces) == 0 || (len(pt.pieces) == 1 && pt.pieces[0].length == 0) {
		addStart := len(pt.add)
		pt.add = append(pt.add, text...)
		pt.pieces = []piece{{buffer: addBuffer, start: addStart, length: len(text)}}
		pt.revision++
		edit := document.Edit{Start: offset, End: offset, Text: string(text)}
		pt.recordLineChange(line, line, bytes.Count(text, []byte{'\n'}), edit)
		pt.invalidateCache()
		pt.observeMutation(edit)
		return EditInfo{}, nil
	}

	addStart := len(pt.add)
	pt.add = append(pt.add, text...)
	newPiece := piece{buffer: addBuffer, start: addStart, length: len(text)}

	pieceIdx, pieceOffset := pt.findPiece(offset)

	var newPieces []piece

	switch pieceOffset {
	case 0:
		newPieces = make([]piece, 0, len(pt.pieces)+1)
		newPieces = append(newPieces, pt.pieces[:pieceIdx]...)
		newPieces = append(newPieces, newPiece)
		newPieces = append(newPieces, pt.pieces[pieceIdx:]...)
	case pt.pieces[pieceIdx].length:
		newPieces = make([]piece, 0, len(pt.pieces)+1)
		newPieces = append(newPieces, pt.pieces[:pieceIdx+1]...)
		newPieces = append(newPieces, newPiece)
		if pieceIdx+1 < len(pt.pieces) {
			newPieces = append(newPieces, pt.pieces[pieceIdx+1:]...)
		}
	default:
		oldPiece := pt.pieces[pieceIdx]
		leftPiece := piece{buffer: oldPiece.buffer, start: oldPiece.start, length: pieceOffset}
		rightPiece := piece{buffer: oldPiece.buffer, start: oldPiece.start + pieceOffset, length: oldPiece.length - pieceOffset}

		newPieces = make([]piece, 0, len(pt.pieces)+2)
		newPieces = append(newPieces, pt.pieces[:pieceIdx]...)
		newPieces = append(newPieces, leftPiece, newPiece, rightPiece)
		if pieceIdx+1 < len(pt.pieces) {
			newPieces = append(newPieces, pt.pieces[pieceIdx+1:]...)
		}
	}

	pt.pieces = newPieces
	pt.revision++
	edit := document.Edit{Start: offset, End: offset, Text: string(text)}
	pt.recordLineChange(line, line, bytes.Count(text, []byte{'\n'}), edit)
	pt.invalidateCache()
	pt.observeMutation(edit)

	return EditInfo{}, nil
}

func (pt *PieceTable) Delete(start, end Position) (EditInfo, error) {
	return pt.deleteOffsets(pt.positionToOffset(start), pt.positionToOffset(end))
}

// deleteOffsets intentionally preserves exact source byte spans, including
// spans inside a grapheme when applying a recorded inverse operation.
func (pt *PieceTable) deleteOffsets(startOff, endOff int) (EditInfo, error) {
	pt.ensureCache()
	startOff = min(len(pt.cachedBytes), max(0, startOff))
	endOff = min(len(pt.cachedBytes), max(0, endOff))

	if startOff >= endOff {
		return EditInfo{}, nil
	}
	startLine := pt.positionAtOffset(startOff).Line
	endLine := pt.positionAtOffset(endOff).Line

	startPieceIdx, startPieceOffset := pt.findPiece(startOff)
	endPieceIdx, endPieceOffset := pt.findPiece(endOff)

	var newPieces []piece

	newPieces = append(newPieces, pt.pieces[:startPieceIdx]...)

	if startPieceOffset > 0 {
		startPiece := pt.pieces[startPieceIdx]
		newPieces = append(newPieces, piece{
			buffer: startPiece.buffer,
			start:  startPiece.start,
			length: startPieceOffset,
		})
	}

	if endPieceOffset < pt.pieces[endPieceIdx].length {
		endPiece := pt.pieces[endPieceIdx]
		newPieces = append(newPieces, piece{
			buffer: endPiece.buffer,
			start:  endPiece.start + endPieceOffset,
			length: endPiece.length - endPieceOffset,
		})
	}

	if endPieceIdx+1 < len(pt.pieces) {
		newPieces = append(newPieces, pt.pieces[endPieceIdx+1:]...)
	}

	pt.pieces = newPieces
	pt.revision++
	edit := document.Edit{Start: startOff, End: endOff}
	pt.recordLineChange(startLine, endLine, startLine-endLine, edit)
	pt.invalidateCache()
	pt.observeMutation(edit)

	return EditInfo{}, nil
}

func (pt *PieceTable) FilePath() string {
	return pt.filePath
}

func (pt *PieceTable) Revision() uint64 { return pt.revision }
