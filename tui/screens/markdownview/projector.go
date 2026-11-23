package markdownview

import (
	"hash/fnv"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/internal/metrics"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Request is one source revision to render at one width.
type Request struct {
	Text     string
	Revision uint64
	Width    int
	Theme    *theme.Theme
	Links    rendering.MarkdownLinks
}

func (r Request) same(o Request) bool {
	return r.Revision == o.Revision && r.Width == o.Width && r.Theme == o.Theme && r.Links == o.Links
}

// RenderedMsg delivers a finished Document to the Screen that requested it.
type RenderedMsg struct {
	projector *Projector
	seq       uint64
	doc       *Document
}

// Projector renders Markdown documents off the UI goroutine. Rendering is
// block-wise: an edit renders only the blocks whose source changed, and the
// blocks that do need rendering render in parallel on all but one processor,
// which stays with the UI. At most one rendering runs; while it runs, only
// the newest request waits, so typing never queues stale work.
type Projector struct {
	deliver func(tea.Msg)

	mu      sync.Mutex
	seq     uint64
	running bool
	waiting *Request
	closed  atomic.Bool

	// Owned by the single running render.
	cache      map[string]renderedBlock
	cacheWidth int
	cacheTheme *theme.Theme
	version    uint64
}

type renderedBlock struct {
	lines   []chatprojection.PaintLine
	plain   []string
	version uint64
}

// NewProjector returns a projector that hands finished documents to deliver,
// from a worker goroutine.
func NewProjector(deliver func(tea.Msg)) *Projector {
	return &Projector{deliver: deliver}
}

// Request starts rendering r, or makes it the next rendering.
func (p *Projector) Request(r Request) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed.Load() {
		return 0
	}
	p.seq++
	if p.running {
		p.waiting = &r
		return p.seq
	}
	p.running = true
	go p.run(r, p.seq)
	return p.seq
}

// Close drops waiting work. A rendering in progress finishes and is not
// delivered.
func (p *Projector) Close() {
	p.mu.Lock()
	p.closed.Store(true)
	p.waiting = nil
	p.mu.Unlock()
}

func (p *Projector) run(r Request, seq uint64) {
	for {
		doc := p.render(r)
		p.mu.Lock()
		closed, next := p.closed.Load(), p.waiting
		p.waiting = nil
		if next == nil || closed {
			p.running = false
		}
		nextSeq := p.seq
		p.mu.Unlock()
		if !closed && doc != nil && p.deliver != nil {
			p.deliver(RenderedMsg{projector: p, seq: seq, doc: doc})
		}
		if next == nil || closed {
			return
		}
		r, seq = *next, nextSeq
	}
}

var markdownPreviewTime = metrics.NewLatency(metrics.Meta{Name: "render.markdown.preview", Group: metrics.GroupRender,
	Title: "Markdown preview", Help: "Time to build the rendered document on a background worker.", Warn: 150_000, Crit: 750_000})

// render builds one immutable document on a worker.
func (p *Projector) render(r Request) *Document {
	start := time.Now()
	defer markdownPreviewTime.Since(start)
	width := max(1, r.Width)
	if p.cache == nil || width != p.cacheWidth || r.Theme != p.cacheTheme {
		p.cache, p.cacheWidth, p.cacheTheme = make(map[string]renderedBlock), width, r.Theme
	}
	parsed := rendering.MakeMdRenderer(width, r.Theme).ParseBlocks(r.Text, r.Links)
	blocks := make([]renderedBlock, len(parsed.Blocks))
	var missing []int
	for i, block := range parsed.Blocks {
		if cached, ok := p.cache[block.Key]; ok {
			blocks[i] = cached
		} else {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		var next atomic.Int64
		var wg sync.WaitGroup
		workers := min(len(missing), max(1, runtime.GOMAXPROCS(0)-1))
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Each worker owns its renderer and tokens; neither is
				// shared between goroutines.
				md := rendering.MakeMdRenderer(width, r.Theme)
				tokens := ui.NewTokenSet(r.Theme)
				for !p.closed.Load() {
					k := int(next.Add(1)) - 1
					if k >= len(missing) {
						return
					}
					i := missing[k]
					blocks[i] = renderBlock(md, tokens, parsed.Blocks[i].Document)
				}
			}()
		}
		wg.Wait()
		if p.closed.Load() {
			return nil
		}
		for _, i := range missing {
			p.version++
			blocks[i].version = p.version
		}
	}
	// Keep only the blocks of this revision; an edit retires the old ones.
	cache := make(map[string]renderedBlock, len(parsed.Blocks))
	for i, block := range parsed.Blocks {
		cache[block.Key] = blocks[i]
	}
	p.cache = cache
	return assemble(parsed, blocks, r.Revision, width)
}

func renderBlock(md *rendering.MarkdownRenderer, tokens *ui.TokenSet, doc rendering.MarkdownDocument) renderedBlock {
	projection, err := md.RenderParsedMapped(doc)
	if err != nil {
		return renderedBlock{}
	}
	lines := chatprojection.BridgeSourceRows(projection, tokens, "", true)
	// A block's own margins are the space between blocks; the document puts
	// exactly one blank row there, as a whole-document rendering does.
	for len(lines) > 0 && layoutOnly(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && layoutOnly(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = line.PlainText()
	}
	return renderedBlock{lines: lines, plain: plain}
}

// layoutOnly reports a row with no text, link, action or background.
func layoutOnly(line chatprojection.PaintLine) bool {
	for _, span := range line.Spans {
		if strings.TrimSpace(span.Text) != "" || span.Link != nil || span.Action != nil || span.BgSet || span.BGSet {
			return false
		}
	}
	return true
}

func assemble(parsed rendering.MarkdownBlocks, blocks []renderedBlock, revision uint64, width int) *Document {
	doc := &Document{Rows: make([]chatprojection.MessageRow, 0, len(blocks)), Blocks: make([]Block, 0, len(blocks)),
		Lines: parsed.Lines, Revision: revision, Width: width}
	seen := make(map[uint64]int, len(blocks))
	// The blank row between blocks is the full-width padding row a whole
	// document rendering puts there.
	separator := chatprojection.PaintLine{SourceAware: true, Spans: []chatprojection.RowSpan{{Text: strings.Repeat(" ", width), Role: ui.ColorFg}}}
	for i, block := range blocks {
		if len(block.lines) == 0 {
			continue
		}
		row := chatprojection.MessageRow{ToggleLine: -1, Version: block.version}
		h := fnv.New64a()
		h.Write([]byte(parsed.Blocks[i].Key))
		sum := h.Sum64()
		// Equal blocks keep their identity across renderings, so the list
		// keeps their measurements and a selection inside them.
		row.ID = strconv.FormatUint(sum, 36) + "." + strconv.Itoa(seen[sum])
		seen[sum]++
		separated := len(doc.Rows) > 0
		if separated {
			row.Lines = make([]string, 0, len(block.plain)+1)
			row.Native = make([]chatprojection.PaintLine, 0, len(block.lines)+1)
			row.Lines = append(append(row.Lines, ""), block.plain...)
			row.Native = append(append(row.Native, separator), block.lines...)
		} else {
			row.Lines, row.Native = block.plain, block.lines
		}
		start := doc.Height
		if separated {
			start++
		}
		doc.Blocks = append(doc.Blocks, Block{Line: parsed.Blocks[i].Line, Row: start})
		doc.Rows = append(doc.Rows, row)
		doc.Height += len(row.Lines)
	}
	return doc
}
