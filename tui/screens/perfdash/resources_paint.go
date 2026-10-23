package perfdash

import (
	"math"

	"crude/tui/ui"
)

// Resource rows end in fixed columns: a share bar (when wide enough), the
// value and an optional second value.
const (
	resourceBarWidth   = 14
	resourceValueWidth = 10
	resourceExtraWidth = 6
	resourceBarMinW    = 56
)

func resourceColumnsWidth(w int, extra bool) int {
	width := resourceValueWidth
	if extra {
		width += 1 + resourceExtraWidth
	}
	if w >= resourceBarMinW {
		width += resourceBarWidth + 1
	}
	return width
}

func paintResourceColumns(p *painter, x, y, w int, titles [2]string) {
	muted := p.tokens.Color(ui.ColorMuted)
	right := x + w
	if titles[1] != "" {
		right -= resourceExtraWidth
		p.text(right, y, resourceExtraWidth, padLeft(titles[1], resourceExtraWidth), muted, 0)
		right--
	}
	p.text(right-resourceValueWidth, y, resourceValueWidth, padLeft(titles[0], resourceValueWidth), muted, 0)
}

func paintResourceRow(p *painter, row resourceRow, x, y, w int, extra bool) {
	fg := p.tokens.Color(ui.ColorFg)
	muted := p.tokens.Color(ui.ColorMuted)
	columns := resourceColumnsWidth(w, extra)
	left := max(0, w-columns-1)
	labelFg := fg
	if row.sev > SeverityOK {
		labelFg = p.sevColor(row.sev)
	}
	end := p.text(x, y, left, row.label, labelFg, 0)
	if row.detail != "" && end-x+3 < left {
		p.text(end, y, left-(end-x), "  "+row.detail, muted, 0)
	}
	cx := x + w - columns
	if w >= resourceBarMinW {
		if row.share >= 0 {
			paintShareBar(p, cx, y, resourceBarWidth, row.share)
		}
		cx += resourceBarWidth + 1
	}
	p.text(cx, y, resourceValueWidth, padLeft(row.value, resourceValueWidth), fg, 0)
	cx += resourceValueWidth
	if extra {
		p.text(cx+1, y, resourceExtraWidth, padLeft(row.extra, resourceExtraWidth), muted, 0)
	}
}

// paintShareBar draws share of width cells in eighths.
func paintShareBar(p *painter, x, y, width int, share float64) {
	fill := int(math.Round(share * float64(width*8)))
	if share > 0 && fill == 0 {
		fill = 1
	}
	accent := p.tokens.Color(ui.ColorAccent)
	track := p.tokens.Color(ui.ColorBorder)
	for i := range width {
		n := min(max(fill-i*8, 0), 8)
		switch {
		case n == 8:
			p.set(x+i, y, '█', accent, p.bg)
		case n > 0:
			p.set(x+i, y, []rune("▏▎▍▌▋▊▉")[n-1], accent, p.bg)
		default:
			p.set(x+i, y, '·', track, p.bg)
		}
	}
}
