package editor

import "bytes"

// Piece storage is append-only and piece vectors are replaced, never edited.
// A checkpoint therefore retains immutable spans without copying the document.
// Equality compares bytes, not revision IDs or probabilistic fingerprints, and
// bytes.Equal skips unchanged spans sharing the same storage in constant time.
type textSnapshot struct {
	original, add []byte
	pieces        []piece
	size          int
}

func (pt *PieceTable) snapshotText() textSnapshot {
	s := textSnapshot{original: pt.original, add: pt.add, pieces: pt.pieces}
	for _, p := range pt.pieces {
		s.size += p.length
	}
	return s
}

func snapshotBytes(data []byte) textSnapshot {
	return textSnapshot{original: data, pieces: []piece{{buffer: originalBuffer, length: len(data)}}, size: len(data)}
}

type textCursor struct {
	text          textSnapshot
	index, offset int
	reverse       bool
}

func (s textSnapshot) cursor(reverse bool) textCursor {
	index := 0
	if reverse {
		index = len(s.pieces) - 1
	}
	return textCursor{text: s, index: index, reverse: reverse}
}
func (c *textCursor) span() []byte {
	for c.index >= 0 && c.index < len(c.text.pieces) {
		p := c.text.pieces[c.index]
		if c.offset == p.length {
			c.offset = 0
			if c.reverse {
				c.index--
			} else {
				c.index++
			}
			continue
		}
		data := c.text.original
		if p.buffer == addBuffer {
			data = c.text.add
		}
		if c.reverse {
			return data[p.start : p.start+p.length-c.offset]
		}
		return data[p.start+c.offset : p.start+p.length]
	}
	return nil
}

func (s textSnapshot) equal(other textSnapshot) bool {
	if s.size != other.size {
		return false
	}
	a, b := s.cursor(false), other.cursor(false)
	for compared := 0; compared < s.size; {
		x, y := a.span(), b.span()
		n := min(len(x), len(y))
		if n == 0 || !bytes.Equal(x[:n], y[:n]) {
			return false
		}
		a.offset += n
		b.offset += n
		compared += n
	}
	return true
}

// matchingEdges bounds the exact changed bytes even for grouped undo/redo,
// multi-line edits and full replacement. It does not rely on a lossy edit log.
func (s textSnapshot) matchingEdges(other textSnapshot) (prefix, suffix int) {
	match := func(reverse bool, limit int) int {
		a, b := s.cursor(reverse), other.cursor(reverse)
		total := 0
		for total < limit {
			x, y := a.span(), b.span()
			n := min(len(x), len(y), limit-total)
			if n == 0 {
				break
			}
			if reverse {
				x = x[len(x)-n:]
				y = y[len(y)-n:]
			} else {
				x = x[:n]
				y = y[:n]
			}
			if !bytes.Equal(x, y) {
				for i := 0; i < n; i++ {
					j := i
					if reverse {
						j = n - 1 - i
					}
					if x[j] != y[j] {
						return total + i
					}
				}
			}
			total += n
			a.offset += n
			b.offset += n
		}
		return total
	}
	limit := min(s.size, other.size)
	prefix = match(false, limit)
	suffix = match(true, limit-prefix)
	return prefix, suffix
}
