package perfdash

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/metrics"
	"crude/perfdata"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

const (
	minCardWidth = 30
	cardGap      = 2
	chartRows    = 4
	cardHeight   = chartRows + 2
	blockGap     = 1
)

type blockKind uint8

const (
	blockVerdict blockKind = iota
	blockTiles
	blockFindings
	blockHeading
	blockCards
	blockTable
	blockEvents
	blockText
	blockRows
)

type tile struct {
	label string
	value string
	sev   Severity
}

// block is one laid-out piece of the scrollable body. y is in content rows.
type block struct {
	kind     blockKind
	y, h     int
	text     string
	sev      Severity
	tiles    []tile
	findings []Finding
	cards    []card
	cardW    int
	table    tableBlockData
	rowLimit int
	events   []processedEvent
	rows     []resourceRow
	// columns titles the value columns of a heading over rows.
	columns [2]string
	// target is the section a click on this block opens (overview only).
	target string
}

// body is the scrollable dashboard content: it lays out blocks for the
// active section and paints only the rows in view.
type body struct {
	ui.Base
	owner  *Screen
	blocks []block
	scroll *ui.ScrollModel
	width  int
	hoverX int
	hoverY int // content row; -1 when the pointer is outside
}

func newBody(owner *Screen) *body {
	return &body{Base: ui.NewBase(BodyID), owner: owner, scroll: ui.NewScrollModel(), hoverY: -1}
}

func (b *body) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (b *body) Layout(_ ui.LayoutContext, r ui.Rect) {
	b.SetBounds(r)
	if r.W != b.width {
		b.width = r.W
		b.rebuild()
	}
	b.scroll.SetViewport(r.H)
}

// rebuild lays the active section out for the current width.
func (b *body) rebuild() {
	s := b.owner
	width := max(b.width, 1)
	var blocks []block
	y := 0
	push := func(blk block) {
		blk.y = y
		blocks = append(blocks, blk)
		y += blk.h
	}
	gap := func() { y += blockGap }
	if !s.hasData {
		push(block{kind: blockText, h: 1, text: "Collecting measurements…"})
		b.install(blocks, y)
		return
	}
	d := s.data
	switch s.section {
	case SectionOverview:
		sev, verdict := Verdict(s.findings)
		push(block{kind: blockVerdict, h: 1, text: verdict, sev: sev})
		tiles := overviewTiles(d)
		push(block{kind: blockTiles, h: tileRows(tiles, width), tiles: tiles})
		if len(s.findings) > 0 {
			gap()
			push(block{kind: blockFindings, h: min(len(s.findings), 8), findings: s.findings})
		}
		gap()
		y = b.pushCards(&blocks, y, d, overviewRefs, true)
		if events := d.events(); len(events) > 0 {
			push(block{kind: blockHeading, h: 1, text: "Recent events", target: SectionEvents})
			push(block{kind: blockEvents, h: min(len(events), 5), events: events})
		}
	case SectionResources:
		views := resources(d)
		if len(views) == 0 {
			push(block{kind: blockText, h: 1, text: "Resource usage is not available yet."})
			break
		}
		for _, view := range views {
			tiles := resourceTiles(view)
			push(block{kind: blockTiles, h: tileRows(tiles, width), tiles: tiles})
			gap()
			rows := processRows(view)
			push(block{kind: blockHeading, h: 1, text: "Processes · " + view.Label, columns: [2]string{"memory", "cpu"}})
			push(block{kind: blockRows, h: len(rows), rows: rows})
			if disk := view.Disk; disk != nil {
				gap()
				push(block{kind: blockHeading, h: 1, text: diskHeading(disk, d.Fetched), columns: [2]string{"size"}})
				if rows := diskPartRows(disk); len(rows) > 0 {
					push(block{kind: blockRows, h: len(rows), rows: rows})
				}
				if rows := workspaceRows(disk); len(rows) > 0 {
					gap()
					push(block{kind: blockHeading, h: 1, text: workspacesHeading(disk), columns: [2]string{"size"}})
					push(block{kind: blockRows, h: len(rows), rows: rows})
				}
				if note := orphanNote(disk); note != "" {
					gap()
					for _, line := range rendering.WordWrap(note, width) {
						push(block{kind: blockText, h: 1, text: line})
					}
				}
			}
		}
	case SectionEvents:
		events := d.events()
		if len(events) == 0 {
			push(block{kind: blockText, h: 1, text: "No stalls, failures or blocking calls in this window."})
			break
		}
		push(block{kind: blockEvents, h: len(events), events: events})
	default:
		refs := sectionRefs(d, s.section)
		if len(refs) == 0 {
			push(block{kind: blockText, h: 1, text: "Nothing recorded here yet in this window."})
			break
		}
		var charts, tables []seriesRef
		for _, ref := range refs {
			if d.isTable(ref) {
				tables = append(tables, ref)
			} else {
				charts = append(charts, ref)
			}
		}
		y = b.pushCards(&blocks, y, d, charts, false)
		for _, ref := range tables {
			table, ok := buildTable(d, ref)
			if !ok {
				continue
			}
			rows := min(len(table.rows), 12)
			push(block{kind: blockTable, h: 2 + rows, table: table, rowLimit: rows})
			gap()
		}
	}
	b.install(blocks, y)
}

// pushCards lays refs out as rows of chart cards.
func (b *body) pushCards(blocks *[]block, y int, d Data, refs []seriesRef, overview bool) int {
	var cards []card
	for _, ref := range refs {
		if c, ok := buildCard(d, d.resolve(ref)); ok {
			cards = append(cards, c)
		}
	}
	if len(cards) == 0 {
		return y
	}
	columns := max(1, (b.width+cardGap)/(minCardWidth+cardGap))
	cardW := max(1, (b.width-cardGap*(columns-1))/columns)
	for start := 0; start < len(cards); start += columns {
		row := cards[start:min(len(cards), start+columns)]
		blk := block{kind: blockCards, y: y, h: cardHeight, cards: row, cardW: cardW}
		if overview {
			blk.target = "cards"
		}
		*blocks = append(*blocks, blk)
		y += cardHeight + blockGap
	}
	return y
}

func (b *body) install(blocks []block, content int) {
	b.blocks = blocks
	b.scroll.SetContent(content)
	b.scroll.Clamp()
}

// chartPoints is the widest chart in columns: the snapshot resolution worth
// requesting.
func (b *body) chartPoints() int {
	if b.width <= 0 {
		return metrics.HistorySeconds // not laid out yet: full resolution
	}
	columns := max(1, (b.width+cardGap)/(minCardWidth+cardGap))
	return max(8, (b.width-cardGap*(columns-1))/columns)
}

func overviewTiles(d Data) []tile { return tilesFrom(perfdata.OverviewHeadlines(perfdata.Data(d))) }

func tileWidth(t tile) int {
	return rendering.VisibleWidth(t.label) + 1 + rendering.VisibleWidth(t.value)
}

const tileSeparator = "  │  "

func tileRows(tiles []tile, width int) int {
	rows, x := 1, 0
	sep := rendering.VisibleWidth(tileSeparator)
	for i, t := range tiles {
		w := tileWidth(t)
		if i > 0 && x+sep+w > width {
			rows++
			x = 0
		} else if i > 0 {
			x += sep
		}
		x += w
	}
	return rows
}

// --- input -------------------------------------------------------------------

func (b *body) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	switch evt.Kind {
	case ui.EventMouseWheel:
		delta := 3
		if evt.MouseWheel.Button == tea.MouseWheelUp {
			delta = -3
		}
		b.scroll.ScrollBy(delta)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseMotion:
		x, y := ctx.LocalX, ctx.LocalY+b.scroll.Offset()
		if ctx.LocalX < 0 || ctx.LocalY < 0 || ctx.LocalY >= b.Bounds().H {
			y = -1
		}
		if x != b.hoverX || y != b.hoverY {
			b.hoverX, b.hoverY = x, y
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		if section := b.targetAt(ctx.LocalX, ctx.LocalY+b.scroll.Offset()); section != "" {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionSection, Payload: section}, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: b.ID()}}
	case ui.EventKey:
		switch evt.Key.Code {
		case tea.KeyUp:
			b.scroll.ScrollBy(-1)
		case tea.KeyDown:
			b.scroll.ScrollBy(1)
		case tea.KeyPgUp:
			b.scroll.ScrollBy(-max(1, b.Bounds().H-1))
		case tea.KeyPgDown:
			b.scroll.ScrollBy(max(1, b.Bounds().H-1))
		case tea.KeyHome:
			b.scroll.ScrollTo(0)
		case tea.KeyEnd:
			b.scroll.ScrollTo(b.scroll.MaxOffset())
		default:
			return ui.EventResult{}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// ClearHover drops the hover readout when the pointer leaves the dashboard.
func (b *body) clearHover() bool {
	if b.hoverY < 0 {
		return false
	}
	b.hoverY = -1
	return true
}

// targetAt maps a content position to the section a click opens.
func (b *body) targetAt(x, y int) string {
	for _, blk := range b.blocks {
		if y < blk.y || y >= blk.y+blk.h {
			continue
		}
		switch blk.kind {
		case blockFindings:
			if i := y - blk.y; i < len(blk.findings) {
				return blk.findings[i].Section
			}
		case blockHeading:
			return blk.target
		case blockCards:
			if blk.target == "" {
				return ""
			}
			if i := x / (blk.cardW + cardGap); i < len(blk.cards) && x-i*(blk.cardW+cardGap) < blk.cardW {
				return sectionOfRef(blk.cards[i].ref)
			}
		case blockEvents:
			return SectionEvents
		}
	}
	return ""
}

func sectionOfRef(ref seriesRef) string {
	for section, refs := range curated {
		for _, r := range refs {
			if r.name == ref.name {
				return section
			}
		}
	}
	return SectionUI
}

// --- paint -------------------------------------------------------------------

var eighths = [9]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

type painter struct {
	buf    *surface.CellBuffer
	tokens *ui.TokenSet
	bg     cell.Color
	// clip rows in buffer coordinates
	top, bottom int
}

func (p *painter) text(x, y, maxW int, text string, fg cell.Color, attr cell.Attrs) int {
	if maxW <= 0 || y < p.top || y >= p.bottom {
		return x
	}
	if rendering.VisibleWidth(text) > maxW {
		text = rendering.TruncateVisual(text, maxW, "…")
	}
	return p.buf.WriteString(x, y, text, fg, p.bg, attr)
}

func (p *painter) set(x, y int, ch rune, fg, bg cell.Color) {
	if y < p.top || y >= p.bottom {
		return
	}
	p.buf.Set(x, y, cell.NewCell(ch, fg, bg, 0))
}

func (p *painter) sevColor(sev Severity) cell.Color {
	switch sev {
	case SeverityCrit:
		return p.tokens.Color(ui.ColorError)
	case SeverityUnknown:
		return p.tokens.Color(ui.ColorMuted)
	case SeverityWarn:
		return p.tokens.Color(ui.ColorWarning)
	}
	return p.tokens.Color(ui.ColorAccent)
}

func (p *painter) sevGlyph(sev Severity) string {
	switch sev {
	case SeverityCrit:
		return "✕"
	case SeverityUnknown:
		return "?"
	case SeverityWarn:
		return "⚠"
	}
	return "✓"
}

func (b *body) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	// Hover belongs to the pointer's current component; a stale position
	// from before the pointer left must not keep painting a readout.
	stored := b.hoverY
	if !ctx.Hovered {
		b.hoverY = -1
	}
	defer func() { b.hoverY = stored }()
	tokens := b.owner.tokens
	r := b.Bounds()
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	p := &painter{buf: buf, tokens: tokens, bg: bg, top: oy + r.Y, bottom: oy + r.Y + r.H}
	offset := b.scroll.Offset()
	x0 := ox + r.X
	for i := range b.blocks {
		blk := &b.blocks[i]
		if blk.y+blk.h <= offset || blk.y >= offset+r.H {
			continue
		}
		b.paintBlock(p, blk, x0, oy+r.Y+blk.y-offset, r.W)
	}
}

func (b *body) paintBlock(p *painter, blk *block, x, y, w int) {
	fg := p.tokens.Color(ui.ColorFg)
	muted := p.tokens.Color(ui.ColorMuted)
	switch blk.kind {
	case blockText:
		p.text(x, y, w, blk.text, muted, 0)
	case blockVerdict:
		end := p.text(x, y, w, p.sevGlyph(blk.sev)+" ", p.sevColor(blk.sev), cell.AttrBold)
		status := b.owner.statusLine()
		statusW := rendering.VisibleWidth(status)
		end = p.text(end, y, max(0, w-(end-x)-statusW-2), blk.text, p.sevColor(blk.sev), cell.AttrBold)
		if x+w-statusW > end+1 {
			p.text(x+w-statusW, y, statusW, status, muted, 0)
		}
	case blockTiles:
		cx, cy := x, y
		sep := rendering.VisibleWidth(tileSeparator)
		for i, t := range blk.tiles {
			tw := tileWidth(t)
			if i > 0 && cx-x+sep+tw > w {
				cx, cy = x, cy+1
			} else if i > 0 {
				cx = p.text(cx, cy, sep, tileSeparator, p.tokens.Color(ui.ColorBorder), 0)
			}
			cx = p.text(cx, cy, w-(cx-x), t.label+" ", muted, 0)
			valueFg := fg
			if t.sev > SeverityOK {
				valueFg = p.sevColor(t.sev)
			}
			cx = p.text(cx, cy, w-(cx-x), t.value, valueFg, cell.AttrBold)
		}
	case blockFindings:
		for i, f := range blk.findings {
			if i >= blk.h {
				break
			}
			row := y + i
			hovered := b.hoverY == blk.y+i
			attr := cell.Attrs(0)
			if hovered {
				attr = cell.AttrUnderline
			}
			end := p.text(x, row, w, p.sevGlyph(f.Severity)+" ", p.sevColor(f.Severity), 0)
			end = p.text(end, row, w-(end-x), f.Title, fg, cell.AttrBold|attr)
			if f.Detail != "" {
				p.text(end, row, w-(end-x), " — "+f.Detail, muted, attr)
			}
		}
	case blockHeading:
		titleW := w
		if blk.columns[0] != "" {
			titleW = max(0, w-resourceColumnsWidth(w, blk.columns[1] != "")-1)
			paintResourceColumns(p, x, y, w, blk.columns)
		}
		p.text(x, y, titleW, blk.text, fg, cell.AttrBold)
	case blockRows:
		extra := len(blk.rows) > 0 && blk.rows[0].extra != ""
		for i, row := range blk.rows {
			if i >= blk.h {
				break
			}
			paintResourceRow(p, row, x, y+i, w, extra)
		}
	case blockCards:
		for i := range blk.cards {
			cx := x + i*(blk.cardW+cardGap)
			hoverCol := -1
			if b.hoverY >= blk.y+1 && b.hoverY < blk.y+1+chartRows {
				local := b.hoverX - i*(blk.cardW+cardGap)
				if local >= 0 && local < blk.cardW {
					hoverCol = local
				}
			}
			b.paintCard(p, &blk.cards[i], cx, y, blk.cardW, hoverCol, blk.target != "" && b.hoverY >= blk.y && b.hoverY < blk.y+blk.h && b.hoverX >= i*(blk.cardW+cardGap) && b.hoverX < i*(blk.cardW+cardGap)+blk.cardW)
		}
	case blockTable:
		b.paintTable(p, blk, x, y, w)
	case blockEvents:
		now := b.owner.data.Fetched
		for i, event := range blk.events {
			if i >= blk.h {
				break
			}
			row := y + i
			sev := SeverityWarn
			if event.Severity == "crit" {
				sev = SeverityCrit
			}
			end := p.text(x, row, w, event.At.Format("15:04:05")+" ", muted, 0)
			end = p.text(end, row, w-(end-x), p.sevGlyph(sev)+" ", p.sevColor(sev), 0)
			if event.Micros > 0 {
				end = p.text(end, row, w-(end-x), formatMicros(event.Micros)+" ", p.sevColor(sev), cell.AttrBold)
			}
			end = p.text(end, row, w-(end-x), event.Title, fg, cell.AttrBold)
			tail := ""
			if event.Label != "" {
				tail += " · " + shortKey(event.Label)
			}
			if event.Detail != "" {
				tail += " · " + event.Detail
			}
			tail += " · " + event.Process.Label()
			if now.Sub(event.At) > 2*time.Second {
				tail += " · " + formatAgo(now, event.At)
			}
			p.text(end, row, w-(end-x), tail, muted, 0)
		}
	}
}

func (b *body) paintCard(p *painter, c *card, x, y, w, hoverCol int, linkHover bool) {
	fg := p.tokens.Color(ui.ColorFg)
	muted := p.tokens.Color(ui.ColorMuted)
	// Title row: title left, current reading right.
	headline := c.headline
	headW := rendering.VisibleWidth(headline)
	titleAttr := cell.AttrBold
	if linkHover {
		titleAttr |= cell.AttrUnderline
	}
	p.text(x, y, max(0, w-headW-1), c.title, fg, titleAttr)
	if headW < w {
		p.text(x+w-headW, y, headW, headline, p.sevColor(c.headSev), cell.AttrBold)
	}
	// Chart rows.
	scale := 0.0
	for i, v := range c.values {
		scale = max(scale, v)
		if c.kind != chartLatency && c.peaks != nil {
			scale = max(scale, c.peaks[i])
		}
	}
	scale *= 1.15
	if c.warn > 0 && c.kind != chartRate {
		scale = max(scale, c.warn)
	}
	if scale <= 0 {
		scale = 1
	}
	levels := chartRows * 8
	shown := min(len(c.values), w)
	first := len(c.values) - shown
	bg := p.bg
	hoverBg := p.tokens.Color(ui.ColorHover)
	border := p.tokens.Color(ui.ColorBorder)
	warnLevel := -1
	if c.warn > 0 && c.kind != chartRate && c.warn <= scale {
		warnLevel = int(math.Round(c.warn / scale * float64(levels)))
	}
	for col := 0; col < w; col++ {
		cellBg := bg
		if col == hoverCol {
			cellBg = hoverBg
		}
		i := first + col - (w - shown)
		for row := 0; row < chartRows; row++ {
			yy := y + chartRows - row
			if i < first || i < 0 {
				p.set(x+col, yy, ' ', fg, cellBg)
				continue
			}
			v := c.values[i]
			level := int(math.Round(v / scale * float64(levels)))
			if v > 0 && level == 0 {
				level = 1
			}
			fill := min(max(level-row*8, 0), 8)
			if fill > 0 {
				p.set(x+col, yy, eighths[fill], p.sevColor(c.severity(v)), cellBg)
				continue
			}
			ch, color := ' ', fg
			if warnLevel >= 0 && warnLevel/8 == row && warnLevel > 0 {
				ch, color = '·', border
			}
			if c.peaks != nil && c.kind == chartLatency && c.peaks[i] > v {
				peakLevel := min(int(math.Round(c.peaks[i]/scale*float64(levels))), levels-1)
				if peakLevel/8 == row {
					ch, color = '·', p.sevColor(c.severity(c.peaks[i]))
					if c.peaks[i] > scale {
						ch = '↑'
					}
				}
			}
			p.set(x+col, yy, ch, color, cellBg)
		}
	}
	// Footer: hover readout, else window summary with the chart scale.
	footer := "scale " + c.format(scale/1.15) + " · " + c.footer
	if c.kind == chartLatency || c.warn > 0 {
		footer = c.footer
	}
	if hoverCol >= 0 {
		if i := first + hoverCol - (w - shown); i >= first && i >= 0 {
			footer = c.readout(i)
		}
	}
	p.text(x, y+chartRows+1, w, footer, muted, 0)
}

func (b *body) paintTable(p *painter, blk *block, x, y, w int) {
	fg := p.tokens.Color(ui.ColorFg)
	muted := p.tokens.Color(ui.ColorMuted)
	t := blk.table
	end := p.text(x, y, w, t.title, fg, cell.AttrBold)
	if t.help != "" && end-x+4 < w {
		p.text(end, y, w-(end-x), "  "+t.help, muted, 0)
	}
	// Columns, dropped right to left as width shrinks.
	type column struct {
		title string
		width int
		min   int // minimum total width to show
	}
	cols := []column{{"share", 14, 46}, {"time", 7, 0}, {"calls", 7, 56}, {"avg", 7, 64}, {"max", 7, 0}, {"trend", 14, 84}}
	var shown []column
	fixed := 0
	for _, c := range cols {
		if w >= c.min {
			shown = append(shown, c)
			fixed += c.width + 1
		}
	}
	keyW := max(8, w-fixed)
	header := strings.Repeat(" ", min(keyW, w))
	for _, c := range shown {
		header += " " + padLeft(c.title, c.width)
	}
	p.text(x, y+1, w, header, muted, 0)
	for i, row := range t.rows {
		if i >= blk.rowLimit {
			break
		}
		ry := y + 2 + i
		rowFg := fg
		key := shortKey(row.Key)
		p.text(x, ry, keyW, key, rowFg, 0)
		cx := x + keyW
		mean := row.Sum / int64(max(row.Count, 1))
		for _, c := range shown {
			cx++
			switch c.title {
			case "share":
				share := 0.0
				if t.total > 0 {
					share = float64(row.Sum) / float64(t.total)
				}
				barW := c.width - 5
				fillEighths := int(math.Round(share * float64(barW*8)))
				for j := 0; j < barW; j++ {
					n := min(max(fillEighths-j*8, 0), 8)
					ch := eighths[n]
					if n == 8 {
						ch = '█'
					} else if n > 0 {
						ch = []rune("▏▎▍▌▋▊▉")[n-1]
					}
					p.set(cx+j, ry, ch, p.tokens.Color(ui.ColorAccent), p.bg)
				}
				p.text(cx+barW, ry, 5, padLeft(formatValue(share*100, "%"), 5), muted, 0)
			case "time":
				p.text(cx, ry, c.width, padLeft(formatMicros(row.Sum), c.width), fg, 0)
			case "calls":
				p.text(cx, ry, c.width, padLeft(formatCount(float64(row.Count)), c.width), muted, 0)
			case "avg":
				p.text(cx, ry, c.width, padLeft(formatMicros(mean), c.width), p.sevOrFg(severityOf(mean, t.meta)), 0)
			case "max":
				p.text(cx, ry, c.width, padLeft(formatMicros(row.Max), c.width), p.sevOrFg(severityOf(row.Max, t.meta)), 0)
			case "trend":
				b.paintSpark(p, cx, ry, c.width, row.Points)
			}
			cx += c.width
		}
	}
}

func (p *painter) sevOrFg(sev Severity) cell.Color {
	if sev == SeverityOK {
		return p.tokens.Color(ui.ColorFg)
	}
	return p.sevColor(sev)
}

// paintSpark draws a one-row sparkline of time consumed per point.
func (b *body) paintSpark(p *painter, x, y, w int, points []int64) {
	if len(points) == 0 {
		return
	}
	// Fold points into w columns, keeping the most recent on the right.
	folded := make([]int64, w)
	for i, v := range points {
		col := i * w / len(points)
		folded[col] += v
	}
	peak := int64(0)
	for _, v := range folded {
		peak = max(peak, v)
	}
	if peak == 0 {
		return
	}
	for i, v := range folded {
		level := int(math.Round(float64(v) / float64(peak) * 8))
		if v > 0 && level == 0 {
			level = 1
		}
		p.set(x+i, y, eighths[level], p.tokens.Color(ui.ColorAccent2), p.bg)
	}
}

func padLeft(s string, w int) string {
	n := rendering.VisibleWidth(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}
