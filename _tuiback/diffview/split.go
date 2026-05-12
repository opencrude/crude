package diffview

import (
	"slices"

	"github.com/aymanbagabas/go-udiff"
)

type splitHunk struct {
	fromLine int
	toLine   int
	lines    []*splitLine
}

type splitLine struct {
	before *udiff.Line
	after  *udiff.Line
}

func shift[T any](s []T) (T, []T, bool) {
	if len(s) == 0 {
		var zero T
		return zero, s, false
	}
	return s[0], s[1:], true
}

func deleteAt[T any](s []T, idx int) (T, []T, bool) {
	if idx < 0 || idx >= len(s) {
		var zero T
		return zero, s, false
	}
	v := s[idx]
	return v, append(s[:idx], s[idx+1:]...), true
}

func hunkToSplit(h *udiff.Hunk) (sh splitHunk) {
	lines := slices.Clone(h.Lines)
	sh = splitHunk{
		fromLine: h.FromLine,
		toLine:   h.ToLine,
		lines:    make([]*splitLine, 0, len(lines)),
	}

	for {
		var ul udiff.Line
		var ok bool
		ul, lines, ok = shift(lines)
		if !ok {
			break
		}

		var sl splitLine

		switch ul.Kind {
		case udiff.Equal:
			sl.before = &ul
			sl.after = &ul

		case udiff.Insert:
			sl.before = nil
			sl.after = &ul

		case udiff.Delete:
			sl.before = &ul

		inner:
			for i, l := range lines {
				switch l.Kind {
				case udiff.Insert:
					var ll udiff.Line
					ll, lines, _ = deleteAt(lines, i)
					sl.after = &ll
					break inner
				case udiff.Equal:
					break inner
				}
			}
		}

		sh.lines = append(sh.lines, &sl)
	}

	return sh
}
