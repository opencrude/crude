package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// PointerTextSource marks a reading component whose painted text a pointer
// drag may select. Components that own their own text model (TextArea, the
// editor, chat, terminals) or pointer drags (scrollbars, dividers, tabs, trees)
// do not implement it. Local coordinates are relative to the component.
type PointerTextSource interface {
	PointerTextSelectable(localX, localY int) bool
}

// hostTextDrag is the Host's one painted-text selection gesture. A plain left
// press on a PointerTextSource is held back: a release on the same cell replays
// it as an ordinary click, so a list row activates exactly as before; moving
// the pointer instead selects the painted cells and copies them on release, as
// every other reading surface does. The press is never delivered for a drag,
// so selecting the text of a choice never chooses it.
type hostTextDrag struct {
	active    bool
	selecting bool
	target    ComponentID
	press     tea.MouseClickMsg
	clip      Rect
	anchorX   int
	anchorY   int
	headX     int
	headY     int
	text      string
}

// SetPointerTextSelection enables painted-text drag selection for this Host.
// Only Hosts whose owners route the deferred press result on release (the
// shell root, session decks and native window frames) enable it.
func (h *Host) SetPointerTextSelection(enabled bool) {
	if !enabled {
		h.endTextDrag()
	}
	h.pointerText = enabled
}

// SetClipboardCopy overrides how painted-text copies are built (tests inject a
// recorder). Passing nil restores the system-clipboard writer.
func (h *Host) SetClipboardCopy(fn func(string) tea.Cmd) { h.copyText = fn }

// PointerTextSelection reports the painted text selected by an active drag.
func (h *Host) PointerTextSelection() (string, bool) {
	if !h.textDrag.selecting {
		return "", false
	}
	return h.textDrag.text, true
}

func (h *Host) beginTextDrag(m tea.MouseClickMsg) bool {
	if !h.pointerText || h.replaying || m.Button != tea.MouseLeft || m.Mod != 0 {
		return false
	}
	id := h.resolvePointerTarget(h.HitTest(m.X, m.Y))
	source, ok := h.registry[id].(PointerTextSource)
	if !ok || (h.FocusScopeModal() && !h.FocusWithinScope(id)) {
		return false
	}
	r := h.screen[id]
	if r.Empty() || !source.PointerTextSelectable(m.X-r.X, m.Y-r.Y) {
		return false
	}
	h.ResetWheelGesture()
	h.CancelPointerCapture()
	h.Focus(id)
	h.textDrag = hostTextDrag{active: true, target: id, press: m, clip: r, anchorX: m.X, anchorY: m.Y, headX: m.X, headY: m.Y}
	h.captured, h.captureBtn = id, tea.MouseLeft
	return true
}

func (h *Host) moveTextDrag(m tea.MouseMotionMsg) EventResult {
	// Like every other drag owner, motion is not filtered by its reported
	// button: some terminals omit it while a button is held.
	x, y := m.X, m.Y
	d := &h.textDrag
	if !d.selecting && x == d.anchorX && y == d.anchorY {
		return EventResult{Handled: true}
	}
	d.selecting = true
	d.headX, d.headY = d.clampHead(x, y)
	h.MarkComponentPaint(d.target)
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func (h *Host) releaseTextDrag(m tea.MouseReleaseMsg) EventResult {
	d := h.textDrag
	h.endTextDrag()
	h.clearPointerCapture()
	if d.selecting {
		res := EventResult{Handled: true, Invalidate: InvalidatePaint}
		if text := strings.TrimRight(d.text, " \n"); text != "" {
			copyText := h.copyText
			if copyText == nil {
				copyText = rendering.CopyToClipboardCmd
			}
			res.Cmd = copyText(text)
		}
		return res
	}
	// A click: deliver the held press and its release through the ordinary
	// routes, so focus, capture, activation and release stay unchanged.
	h.replaying = true
	press := h.Handle(d.press)
	release := h.Handle(m)
	h.replaying = false
	return mergePointerResults(press, release)
}

func (h *Host) endTextDrag() {
	if h.textDrag.selecting {
		h.MarkComponentPaint(h.textDrag.target)
	}
	h.textDrag = hostTextDrag{}
}

func mergePointerResults(a, b EventResult) EventResult {
	out := a
	out.Handled = true
	out.Invalidate |= b.Invalidate
	if out.Action == nil {
		out.Action = b.Action
	}
	out.Cmd = tea.Batch(a.Cmd, b.Cmd)
	return out
}

// clampHead keeps the selection inside the pressed component. Above it the
// selection reaches the first cell; below it, the last.
func (d *hostTextDrag) clampHead(x, y int) (int, int) {
	c := d.clip
	switch {
	case y < c.Y:
		return c.X, c.Y
	case y >= c.Y+c.H:
		return c.X + c.W - 1, c.Y + c.H - 1
	}
	return min(max(x, c.X), c.X+c.W-1), y
}

// paintTextSelection records the selected painted text and marks it. It runs
// after the tree paints, so the copy is exactly what the user sees. The marking
// is idempotent: retained regions that did not repaint keep the same colors.
func (h *Host) paintTextSelection(buf *surface.CellBuffer) {
	d := &h.textDrag
	if !d.selecting || buf == nil {
		return
	}
	sx, sy, ex, ey := d.anchorX, d.anchorY, d.headX, d.headY
	if ey < sy || (ey == sy && ex < sx) {
		sx, sy, ex, ey = ex, ey, sx, sy
	}
	left, right := d.clip.X, d.clip.X+d.clip.W-1
	var text strings.Builder
	for y := sy; y <= ey; y++ {
		row := buf.Row(y)
		if row == nil {
			continue
		}
		x0, x1 := left, right
		if y == sy {
			x0 = sx
		}
		if y == ey {
			x1 = ex
		}
		x0, x1 = max(x0, 0), min(x1, len(row)-1)
		// A wide glyph is selected whole when either of its columns is.
		for x0 > left && x0 > 0 && isContinuationCell(row[x0]) {
			x0--
		}
		// Arranged right-to-left text returns to logical order and to its
		// source characters, so the copy reads like the text it shows.
		line := ""
		if x0 <= x1 {
			line = surface.ReadLogical(row[x0 : x1+1])
		}
		for x := x0; x <= x1; x++ {
			c := &row[x]
			c.FG, c.BG = h.tokens.TextSelectionColors(c.FG)
		}
		if y > sy {
			text.WriteByte('\n')
		}
		text.WriteString(strings.TrimRight(line, " "))
	}
	d.text = text.String()
}

// isContinuationCell reports the trailing column of a wide glyph.
func isContinuationCell(c cell.Cell) bool {
	return c.Width == 0 && c.Char == 0 && c.Content == ""
}
