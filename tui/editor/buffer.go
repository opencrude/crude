package editor

import (
	"bytes"
	"fmt"
	"os"
	"unicode/utf8"
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
	original []byte
	add      []byte
	pieces   []piece

	filePath string
	modified bool

	cachedBytes []byte
	lineOffsets []int
	cacheValid  bool
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
			pt.modified = false
			pt.invalidateCache()
			return nil
		}
		return err
	}

	pt.original = content
	pt.add = []byte{}
	pt.pieces = []piece{{buffer: originalBuffer, start: 0, length: len(content)}}
	pt.filePath = filePath
	pt.modified = false
	pt.invalidateCache()
	return nil
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
	out := make([]byte, end-start)
	copy(out, pt.cachedBytes[start:end])
	return out, nil
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

	byteOffInLine := 0
	runeCount := 0
	for i := 0; i < len(lineBytes); {
		if runeCount == pos.Col {
			break
		}
		_, size := utf8.DecodeRune(lineBytes[i:])
		if size == 0 {
			break
		}
		byteOffInLine += size
		runeCount++
		i += size
	}
	if runeCount < pos.Col {
		byteOffInLine = len(lineBytes)
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
		pt.pieces[0] = piece{buffer: addBuffer, start: 0, length: len(text)}
		pt.modified = true
		pt.invalidateCache()
		return EditInfo{}, nil
	}

	addStart := len(pt.add)
	pt.add = append(pt.add, text...)
	newPiece := piece{buffer: addBuffer, start: addStart, length: len(text)}

	pieceIdx, pieceOffset := pt.findPiece(offset)

	var newPieces []piece

	if pieceOffset == 0 {
		newPieces = make([]piece, 0, len(pt.pieces)+1)
		newPieces = append(newPieces, pt.pieces[:pieceIdx]...)
		newPieces = append(newPieces, newPiece)
		newPieces = append(newPieces, pt.pieces[pieceIdx:]...)
	} else if pieceOffset == pt.pieces[pieceIdx].length {
		newPieces = make([]piece, 0, len(pt.pieces)+1)
		newPieces = append(newPieces, pt.pieces[:pieceIdx+1]...)
		newPieces = append(newPieces, newPiece)
		if pieceIdx+1 < len(pt.pieces) {
			newPieces = append(newPieces, pt.pieces[pieceIdx+1:]...)
		}
	} else {
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
	pt.modified = true
	pt.invalidateCache()

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
	pt.modified = true
	pt.invalidateCache()

	return EditInfo{}, nil
}

func (pt *PieceTable) Save(filePath string) error {
	path := filePath
	if path == "" {
		path = pt.filePath
	}
	if path == "" {
		return fmt.Errorf("no file path specified")
	}

	err := os.WriteFile(path, pt.Bytes(), 0644)
	if err != nil {
		return err
	}

	pt.filePath = path
	pt.modified = false
	return nil
}

func (pt *PieceTable) FilePath() string {
	return pt.filePath
}

func (pt *PieceTable) IsModified() bool {
	return pt.modified
}
