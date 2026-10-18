package editor

import (
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

type pointerUnit uint8

const (
	pointerNone pointerUnit = iota
	pointerCharacter
	pointerWord
	pointerLine
)

// A gesture owns its original source-unit bounds, latest local pointer and at
// most one tick. A caret-anchored Shift gesture has identical start/end bounds.
// Selection projection is identical for motion, wheel, repeat and final release.
type pointerSelection struct {
	kind        pointerUnit
	start, end  Position
	x, y        int
	tickPending bool
}

// PointerScrollMsg is delivered only to the currently captured mounted control.
// The exact editor and gesture epoch reject late ticks after release, cancel,
// close, replacement or a new press. The timer closure reads no mutable state.
type PointerScrollMsg struct {
	owner *Editor
	epoch uint64
}

const pointerScrollInterval = 50 * time.Millisecond

func (e *Editor) stopPointerSelection() {
	e.dragEpoch++
	e.drag = pointerSelection{}
}

func (e *Editor) beginPointerSelection(kind pointerUnit, start, end Position, x, y int) {
	e.stopPointerSelection()
	e.drag = pointerSelection{kind: kind, start: start, end: end, x: x, y: y}
	e.selecting = true
	e.projectPointerSelection()
}

func (e *Editor) pointerPosition() Position {
	left, right := e.gutterWidth(), e.viewW-e.scrollbarColWidth()
	// An active selection crosses scrollbar/gutter chrome without changing owners.
	// Clamp to the text edge, not an invisible source row/column far offscreen.
	x := min(max(left, right), max(left, e.drag.x))
	y := min(e.bodyHeight()-1, max(0, e.drag.y))
	return e.buf.snapPosition(Position{Line: e.scrollY + y, Col: e.scrollX + x - left})
}

func positionBefore(a, b Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
}

func (e *Editor) projectPointerSelection() {
	if !e.IsMouseDragging() {
		return
	}
	pos := e.pointerPosition()
	if e.drag.kind == pointerCharacter {
		// A plain press places the insertion caret before the hit source unit.
		// When dragging backward that unit must be inside the half-open range,
		// so its trailing boundary is the anchor. Recompute from the original
		// bounds on every projection: reversing or returning to the pressed
		// unit must not accumulate an extra character. Shift gestures retain
		// their collapsed caret anchor, as does a press at an empty line/EOL.
		e.selAnchor = e.drag.start
		if positionBefore(pos, e.drag.start) {
			e.selAnchor = e.drag.end
		}
		e.selEnd = pos
	} else {
		start, end := e.pointerUnitRange(e.drag.kind, pos)
		if positionBefore(start, e.drag.start) {
			e.selAnchor, e.selEnd = e.drag.end, start
		} else {
			e.selAnchor, e.selEnd = e.drag.start, end
		}
	}
	e.cursor = e.selEnd
	e.clearGoal()
	e.cursorBlink.MarkActivity()
}

func (e *Editor) movePointerSelection(x, y int) tea.Cmd {
	e.drag.x, e.drag.y = x, y
	e.projectPointerSelection()
	return e.schedulePointerScroll()
}

// One-cell edge zones also work when a pane meets the terminal boundary and no
// outside motion is available. Distance accelerates the timer, never event count.
func pointerEdgeStep(value, start, end int) int {
	if value <= start {
		return -min(8, 1+max(0, start-value)/2)
	}
	if value >= end-1 {
		return min(8, 1+max(0, value-(end-1))/2)
	}
	return 0
}

func (e *Editor) pointerScrollStep() (dx, dy int) {
	if !e.IsMouseDragging() {
		return 0, 0
	}
	dy = pointerEdgeStep(e.drag.y, 0, e.bodyHeight())
	if dy < 0 && e.scrollY <= 0 || dy > 0 && e.scrollY >= max(0, e.LineCount()-e.bodyHeight()) {
		dy = 0
	}
	if e.drag.kind != pointerLine {
		textW := max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth())
		e.syncHorizontalScrollChrome(textW, e.bodyHeight())
		dx = pointerEdgeStep(e.drag.x, e.gutterWidth(), e.gutterWidth()+textW)
		if dx < 0 && e.scrollX <= 0 || dx > 0 && e.scrollX >= e.hScrollModel.MaxOffset() {
			dx = 0
		}
	}
	return dx, dy
}

func (e *Editor) schedulePointerScroll() tea.Cmd {
	dx, dy := e.pointerScrollStep()
	if dx == 0 && dy == 0 {
		if e.drag.tickPending {
			e.dragEpoch++
			e.drag.tickPending = false
		}
		return nil
	}
	if e.drag.tickPending {
		return nil
	}
	e.drag.tickPending = true
	e.dragEpoch++
	msg := PointerScrollMsg{owner: e, epoch: e.dragEpoch}
	return tea.Tick(pointerScrollInterval, func(time.Time) tea.Msg { return msg })
}

func (e *Editor) handlePointerScroll(msg PointerScrollMsg) tea.Cmd {
	if msg.owner != e || msg.epoch != e.dragEpoch || !e.IsMouseDragging() || !e.drag.tickPending {
		return nil
	}
	e.drag.tickPending = false
	dx, dy := e.pointerScrollStep()
	e.scrollY = min(max(0, e.LineCount()-e.bodyHeight()), max(0, e.scrollY+dy))
	if dx != 0 {
		e.hScrollModel.ScrollBy(dx)
		e.scrollX = e.hScrollModel.Offset()
	}
	e.syncScrollChrome(e.bodyHeight())
	e.projectPointerSelection()
	return e.schedulePointerScroll()
}

func (e *Editor) pointerUnitRange(kind pointerUnit, pos Position) (start, end Position) {
	pos = e.buf.snapPosition(pos)
	if kind == pointerLine {
		start = Position{Line: pos.Line}
		if pos.Line+1 < e.LineCount() {
			end = Position{Line: pos.Line + 1}
		} else {
			end = Position{Line: pos.Line, Col: e.lineVisualWidth(pos.Line)}
		}
		return start, end
	}
	// Classify complete source units, not individual code points. In particular a
	// combining mark cannot cut a word selection or leave half a ZWJ cluster.
	text := e.LineText(pos.Line)
	col, runStart, runClass := 0, 0, -1
	for len(text) > 0 {
		size, width := firstSourceUnit(text, col)
		if size == 0 {
			break
		}
		r, _ := utf8.DecodeRuneInString(text)
		class := 2
		if unicode.IsSpace(r) {
			class = 0
		} else if isWordChar(r) {
			class = 1
		}
		if class != runClass {
			if pos.Col < col {
				break
			}
			runStart, runClass = col, class
		}
		col += width
		text = text[size:]
	}
	return Position{Line: pos.Line, Col: runStart}, Position{Line: pos.Line, Col: col}
}
