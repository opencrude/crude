package workspacehub

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// USD formats a cost for people: two decimals, and a small nonzero cost is
// never shown as zero.
func USD(value float64) string {
	switch {
	case value <= 0:
		return "$0.00"
	case value < 0.01:
		return "<$0.01"
	case value >= 10000:
		return "$" + Count(int64(value+0.5))
	}
	return fmt.Sprintf("$%.2f", value)
}

// Count formats an integer with thousands separators.
func Count(value int64) string {
	text := strconv.FormatInt(value, 10)
	if len(text) <= 3 {
		return text
	}
	var out strings.Builder
	lead := len(text) % 3
	if lead > 0 {
		out.WriteString(text[:lead])
	}
	for i := lead; i < len(text); i += 3 {
		if out.Len() > 0 {
			out.WriteByte(',')
		}
		out.WriteString(text[i : i+3])
	}
	return out.String()
}

// Tokens formats a token count compactly: 940, 12.4k, 3.1M.
func Tokens(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1e9)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1e6)
	case value >= 10_000:
		return fmt.Sprintf("%.0fk", float64(value)/1e3)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1e3)
	}
	return strconv.FormatInt(value, 10)
}

// Day formats a date without the year when it is this year.
func Day(at time.Time, now time.Time) string {
	if at.Year() == now.Year() {
		return at.Format("Jan 2")
	}
	return at.Format("Jan 2, 2006")
}

// DateRange formats the span between two unix-nanosecond times.
func DateRange(first, last int64, now time.Time) string {
	if first == 0 || last == 0 {
		return ""
	}
	from, to := Day(time.Unix(0, first), now), Day(time.Unix(0, last), now)
	if from == to {
		return from
	}
	return from + " – " + to
}

// pen paints text into a page that scrolls. Rows outside the viewport are
// skipped, so a page paints its whole content in one pass.
type pen struct {
	buf      *surface.CellBuffer
	tokens   *ui.TokenSet
	bg       cell.Color
	x, y     int // page origin in the buffer
	offset   int // first content row shown
	viewport int
}

func (p pen) visible(row int) bool { return row >= p.offset && row < p.offset+p.viewport }

func (p pen) text(col, row, width int, text string, role ui.SemanticColor, attr cell.Attrs) int {
	if width <= 0 || !p.visible(row) {
		return col + min(width, rendering.VisibleWidth(text))
	}
	if rendering.VisibleWidth(text) > width {
		text = rendering.TruncateVisual(text, width, "…")
	}
	end := p.buf.WriteString(p.x+col, p.y+row-p.offset, text, p.tokens.Color(role), p.bg, attr)
	return end - p.x
}

// right paints text so it ends at col+width.
func (p pen) right(col, row, width int, text string, role ui.SemanticColor, attr cell.Attrs) {
	w := rendering.VisibleWidth(text)
	if w > width {
		p.text(col, row, width, text, role, attr)
		return
	}
	p.text(col+width-w, row, w, text, role, attr)
}

func (p pen) glyph(col, row int, glyph rune, role ui.SemanticColor) {
	if p.visible(row) {
		p.buf.Set(p.x+col, p.y+row-p.offset, cell.NewCell(glyph, p.tokens.Color(role), p.bg, 0))
	}
}

// bar paints a share bar with an empty track.
func (p pen) bar(col, row, width int, share float64, role ui.SemanticColor) {
	for i, glyph := range ui.ShareBar(width, share) {
		if glyph == 0 {
			p.glyph(col+i, row, '·', ui.ColorBorder)
			continue
		}
		p.glyph(col+i, row, glyph, role)
	}
}

// pageScroll keeps a scrolling page's offset and scrollbar.
type pageScroll struct {
	model *ui.ScrollModel
	bar   *ui.ScrollbarControl
}

func newPageScroll(id ui.ComponentID, tokens *ui.TokenSet) pageScroll {
	model := ui.NewScrollModel()
	return pageScroll{model: model, bar: ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), model, tokens)}
}

// handle scrolls with the wheel and the paging keys.
func (s pageScroll) handle(evt ui.Event) bool {
	before := s.model.Offset()
	switch {
	case evt.Kind == ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			s.model.ScrollBy(-3)
		case tea.MouseWheelDown:
			s.model.ScrollBy(3)
		default:
			return false
		}
	case evt.IsKey():
		switch evt.Key.String() {
		case "up":
			s.model.ScrollBy(-1)
		case "down":
			s.model.ScrollBy(1)
		case "pgup":
			s.model.ScrollBy(-max(1, s.model.Viewport()-1))
		case "pgdown":
			s.model.ScrollBy(max(1, s.model.Viewport()-1))
		case "home":
			s.model.ScrollTo(0)
		case "end":
			s.model.ScrollTo(s.model.MaxOffset())
		default:
			return false
		}
	default:
		return false
	}
	return s.model.Offset() != before || evt.IsKey()
}
