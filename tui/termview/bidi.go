package termview

import (
	"strings"
	"sync/atomic"

	"crude/terminalvt"
	"crude/tui/bidi"
)

// Arrangement selects which terminal screens Crude arranges right-to-left
// text on. A program that draws its own full-screen layout (an editor, a
// pager, another Crude) normally uses the alternate screen and may arrange
// text itself; shell output uses the main screen and never does.
type Arrangement uint8

const (
	// ArrangeShell arranges the main screen only: shell and command output.
	ArrangeShell Arrangement = iota
	// ArrangeAll arranges the alternate screen as well.
	ArrangeAll
	// ArrangeNone shows every row as the program wrote it.
	ArrangeNone
)

var arrangement atomic.Uint32

// SetArrangement publishes the terminal arrangement preference.
func SetArrangement(a Arrangement) { arrangement.Store(uint32(a)) }

// CurrentArrangement reports the terminal arrangement preference.
func CurrentArrangement() Arrangement { return Arrangement(arrangement.Load()) }

// rowLine arranges one presented row like a terminal emulator in implicit
// BiDi mode (terminal-wg BiDi recommendation): the row is a left-to-right
// paragraph and right-to-left runs read right to left in place, so columns,
// prompts and box drawing stay where the program put them.
func (v *View) rowLine(y, width int) *bidi.Line {
	if y < 0 || y >= len(v.presented) || !v.arranges() {
		return nil
	}
	return arrangeRow(v.presented[y], width)
}

func (v *View) arranges() bool {
	switch CurrentArrangement() {
	case ArrangeNone:
		return false
	case ArrangeShell:
		return !v.Grid.Modes.AltScreen
	default:
		return true
	}
}

func arrangeRow(row []terminalvt.Cell, width int) *bidi.Line {
	width = min(width, len(row))
	needed := false
	for x := 0; x < width && !needed; x++ {
		needed = bidi.NeedsLayout(row[x].Text)
	}
	if !needed {
		return nil
	}
	// The key is the row's exact cells; units are rebuilt only on a miss.
	var key strings.Builder
	for x := 0; x < width; x++ {
		if c := row[x]; c.Width != 0 {
			key.WriteString(c.Text)
			key.WriteByte(0)
			key.WriteByte(byte(c.Width))
		}
	}
	return bidi.CachedLine(key.String(), bidi.LeftToRight, width, -1, func(dst []bidi.Unit) []bidi.Unit {
		for x := 0; x < width; x++ {
			c := row[x]
			if c.Width == 0 {
				continue
			}
			text := c.Text
			if text == "" {
				text = " "
			}
			dst = append(dst, bidi.Unit{Text: text, Width: max(1, int(c.Width))})
		}
		return dst
	})
}

// LogicalColumn maps a visible column of screen row y to the column the
// program wrote there, so selection and mouse reports address the
// character under the pointer.
func (v *View) LogicalColumn(x, y int) int {
	ln := v.rowLine(y, v.Grid.Cols)
	if ln == nil || x < 0 || x >= ln.Span() {
		return x
	}
	return ln.Logical(x)
}
