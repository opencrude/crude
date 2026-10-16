package editor

import (
	"bytes"
	"fmt"
	"os"
	"unicode/utf8"

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
	revision          uint64
	start, end, delta int
}

func (pt *PieceTable) recordLineChange(start, end, delta int) {
	pt.lineChanges[pt.revision%uint64(len(pt.lineChanges))] = bufferLineChange{pt.revision, start, end, delta}
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
	offsets := pt.lineOffsets
	if pos.Line >= len(offsets) {
		return len(pt.cachedBytes)
	}

	lineStart := offsets[pos.Line]
	var lineEnd int
	if pos.Line+1 < len(offsets) {
		lineEnd = offsets[pos.Line+1] - 1
	} else {
		lineEnd = len(pt.cachedBytes)
	}
	lineBytes := pt.cachedBytes[lineStart:lineEnd]

	runes := []rune(string(lineBytes))
	ri := colToRuneIdx(runes, pos.Col)
	if ri > len(runes) {
		ri = len(runes)
	}

	byteOffInLine := 0
	for i := 0; i < ri; i++ {
		_, size := utf8.DecodeRune(lineBytes[byteOffInLine:])
		if size == 0 {
			break
		}
		byteOffInLine += size
	}

	return lineStart + byteOffInLine
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
	if len(text) == 0 {
		return EditInfo{}, nil
	}

	offset := pt.positionToOffset(pos)

	if len(pt.pieces) == 1 && pt.pieces[0].length == 0 {
		pt.add = append(pt.add, text...)
		pt.pieces = []piece{{buffer: addBuffer, start: 0, length: len(text)}}
		pt.revision++
		pt.recordLineChange(pos.Line, pos.Line, bytes.Count(text, []byte{'\n'}))
		pt.invalidateCache()
		pt.observeMutation(document.Edit{Start: offset, End: offset, Text: string(text)})
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
	pt.recordLineChange(pos.Line, pos.Line, bytes.Count(text, []byte{'\n'}))
	pt.invalidateCache()
	pt.observeMutation(document.Edit{Start: offset, End: offset, Text: string(text)})

	return EditInfo{}, nil
}

func (pt *PieceTable) Delete(start, end Position) (EditInfo, error) {
	startOff := pt.positionToOffset(start)
	endOff := pt.positionToOffset(end)

	if startOff >= endOff {
		return EditInfo{}, nil
	}

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
	pt.recordLineChange(start.Line, end.Line, start.Line-end.Line)
	pt.invalidateCache()
	pt.observeMutation(document.Edit{Start: startOff, End: endOff})

	return EditInfo{}, nil
}

func (pt *PieceTable) FilePath() string {
	return pt.filePath
}

func (pt *PieceTable) Revision() uint64 { return pt.revision }
