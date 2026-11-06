package shellchat

import (
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

const (
	// findBarMaxWidth is the widest the field and its status grow.
	findBarMaxWidth = 72
	// findBarMinRows keeps the transcript readable: the bar only appears when
	// at least this many rows remain above it.
	findBarMinRows = 3
	// findStatusGap separates the field from its status text.
	findStatusGap = 2
)

// findBar is the single row under the transcript that holds the find field and
// its match count.
type findBar struct {
	ui.Base
	field   *ui.FilterField
	owner   *Screen
	status  string
	fieldW  int
	statusX int
}

func newFindBar(owner *Screen, tokens *ui.TokenSet) *findBar {
	id := ui.NewID(owner.ID(), "find")
	field := ui.NewFilterField(id, tokens)
	field.SetPlaceholder("Find in chat")
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetSubmitAction("")
	bar := &findBar{Base: ui.NewBase(id), field: field, owner: owner}
	field.OnChanged(owner.findQueryChanged)
	return bar
}

func (bar *findBar) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	bar.SetBounds(bounds)
	bar.status = bar.owner.find.status(len(bar.owner.rows))
	statusW := rendering.VisibleWidth(bar.status)
	bar.fieldW = bounds.W
	if statusW > 0 && bounds.W-statusW-findStatusGap >= 12 {
		bar.fieldW = bounds.W - statusW - findStatusGap
		bar.statusX = bar.fieldW + findStatusGap
	} else {
		bar.status = ""
	}
	bar.field.Layout(ctx, ui.Rect{W: bar.fieldW, H: bounds.H})
}

func (bar *findBar) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	b := bar.Bounds()
	bar.field.Paint(buf, originX+b.X, originY+b.Y, ctx)
	if bar.status == "" {
		return
	}
	tokens := bar.owner.tokens
	buf.WriteString(originX+b.X+bar.statusX, originY+b.Y, bar.status, tokens.Color(ui.ColorMuted), cell.NoColor, 0)
}

// Handle gives the bar its own keys: Enter, Down and F3 go to the next match,
// Shift+Enter, Up and Shift+F3 to the previous, Esc closes. Every other key
// edits the query.
func (bar *findBar) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		changed := ui.InvalidateLayout | ui.InvalidatePaint
		switch {
		case evt.Key.Code == tea.KeyEsc:
			bar.owner.CloseFind()
			return ui.EventResult{Handled: true, Invalidate: changed | ui.InvalidateFocus}
		case ui.IsPreviousMatchChord(evt.Key) || evt.Key.String() == "up":
			bar.owner.StepFind(-1)
			return ui.EventResult{Handled: true, Invalidate: changed}
		case evt.Key.String() == "enter" || evt.Key.String() == "f3" || evt.Key.String() == "down":
			bar.owner.StepFind(1)
			return ui.EventResult{Handled: true, Invalidate: changed}
		case evt.Key.Code == tea.KeyPgUp || evt.Key.Code == tea.KeyPgDown:
			// The transcript stays readable while the field has the keyboard.
			if bar.owner.handleScrollInput(evt) {
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		case evt.Key.String() == "ctrl+f":
			bar.field.Edit().SelectAll()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	return bar.field.Handle(ctx, evt)
}

func (bar *findBar) TextEditor() ui.TextClipboardTarget { return bar.field.TextEditor() }
func (bar *findBar) Semantics() ui.Semantics            { return bar.field.Semantics() }

// FindID is the find field. Hosts focus it to give the bar the keyboard.
func (s *Screen) FindID() ui.ComponentID { return s.findBar.ID() }

// FindOpen reports whether the find bar is open.
func (s *Screen) FindOpen() bool { return s.findOpen }

// FindRows is how many rows the open find bar takes from a chat of height h.
func (s *Screen) FindRows(h int) int {
	if s.findOpen && h-s.activityRows(h) >= findBarMinRows+1 {
		return 1
	}
	return 0
}

// TranscriptRows is how many of h rows show the conversation.
func (s *Screen) TranscriptRows(h int) int {
	return max(0, h-s.activityRows(h)-s.FindRows(h))
}

func (s *Screen) activityRows(h int) int {
	if s.activitySet {
		return ActivityRows(h)
	}
	return 0
}

func (s *Screen) findVisible() bool { return s.FindRows(s.height) > 0 }

func (s *Screen) findRect(r ui.Rect) ui.Rect {
	pad := s.layout.padLR
	w := min(findBarMaxWidth, r.W-2*pad)
	if w < 1 {
		pad, w = 0, r.W
	}
	return ui.Rect{X: pad, Y: r.H - s.activityRows(r.H) - 1, W: w, H: 1}
}

// OpenFind shows the find bar with its text selected. A query from the last
// use comes back and is searched again.
func (s *Screen) OpenFind() {
	if !s.findOpen {
		s.findOpen = true
		s.find.setQuery(s.findBar.field.Value(), -1)
	}
	s.findBar.field.Edit().SelectAll()
	s.findDirty = true
}

// CloseFind hides the bar and drops every highlight and count. The text stays
// in the field for the next use.
func (s *Screen) CloseFind() {
	if !s.findOpen {
		return
	}
	s.findOpen = false
	s.find.stop()
	s.findDirty = true
}

// FindPending reports whether matches are still being counted, so the shell
// keeps frames coming until the count is final.
func (s *Screen) FindPending() bool {
	return s.findOpen && s.find.pending(len(s.rows)) && time.Since(s.findSeen) < time.Second
}

// FindWaiting reports whether the open search still lacks the text of rows
// that were never drawn, so the owner of those rows should finish drawing them.
func (s *Screen) FindWaiting() bool {
	return s.findOpen && s.find.querying() && s.find.waiting > 0 && !s.find.pending(len(s.rows))
}

// TakeFindDirty reports, once, that the bar text, a count or a highlight
// changed without a new set of rows, so the shell must repaint the chat.
func (s *Screen) TakeFindDirty() bool {
	dirty := s.findDirty
	s.findDirty = false
	return dirty
}

// StepFind selects the next (dir 1) or previous (dir -1) match and shows it.
func (s *Screen) StepFind(dir int) bool {
	if !s.findOpen || s.find.pending(len(s.rows)) {
		return false
	}
	if _, ok := s.find.rowOf(s.rows); ok {
		if !s.find.step(s.rows, dir) {
			return false
		}
	} else if !s.selectFind(-1) {
		return false
	}
	s.revealFind()
	s.findDirty = true
	return true
}

func (s *Screen) findQueryChanged(query string) {
	anchor := -1
	if line, ok := s.findLine(); ok {
		anchor = line
	}
	s.find.setQuery(query, anchor)
	s.findDirty = true
}

// observeFind advances counting after the rows were installed.
func (s *Screen) observeFind(source uint64, rebuilt bool) {
	if !s.findOpen {
		return
	}
	s.findSeen = time.Now()
	wasPending := s.find.pending(len(s.rows))
	settled := s.find.observe(s.rows, source, rebuilt)
	if settled {
		s.selectFind(s.find.anchor)
		s.revealFind()
	}
	if rebuilt || wasPending || settled {
		s.findDirty = true
	}
}

// selectFind selects the match nearest a global line, or nearest the middle of
// the viewport when anchor is negative.
func (s *Screen) selectFind(anchor int) bool {
	if anchor < 0 {
		scroll := s.list.Scroll()
		anchor = min(scroll.Offset()+scroll.Viewport()/2, max(0, scroll.Content()-1))
	}
	return s.find.selectNear(s.rows, s.paintState.globalStart, anchor)
}

// findLine is the global line of the selected match.
func (s *Screen) findLine() (int, bool) {
	row, ok := s.find.rowOf(s.rows)
	if !ok || row >= len(s.paintState.globalStart) {
		return 0, false
	}
	for _, seg := range s.find.segments(row, s.rows[row]) {
		if seg.match == s.find.cur.match {
			return s.paintState.globalStart[row] + seg.line, true
		}
	}
	return s.paintState.globalStart[row], true
}

// revealFind scrolls the selected match into view. A match already in view
// does not move the transcript; one outside lands in the middle with context.
func (s *Screen) revealFind() {
	line, ok := s.findLine()
	if !ok {
		return
	}
	scroll := s.list.Scroll()
	if line >= scroll.Offset() && line < scroll.Offset()+scroll.Viewport() {
		return
	}
	scroll.ScrollTo(line - scroll.Viewport()/2)
	s.syncScrollAfterUserScroll()
}

// paintFindMatches marks the matches on one painted line. It restyles cells
// the line painter just wrote, so selection and syntax ink stay the single
// owner of the line and a text selection stays visible over a match.
func (s *Screen) paintFindMatches(buf *surface.CellBuffer, x, y, maxW, index int, row chatprojection.MessageRow, localRow, selStart, selEnd int, tokens *ui.TokenSet) {
	segs := s.find.segments(index, row)
	i := sort.Search(len(segs), func(k int) bool { return segs[k].line >= localRow })
	if i == len(segs) || segs[i].line != localRow {
		return
	}
	plain := linePlain(row, localRow)
	currentRow, hasCurrent := s.find.rowOf(s.rows)
	for ; i < len(segs) && segs[i].line == localRow; i++ {
		seg := segs[i]
		from := rendering.VisibleWidth(plain[:seg.from])
		to := max(rendering.VisibleWidth(plain[:seg.to]), from+1)
		fill := tokens.Color(ui.ColorWarning)
		if hasCurrent && currentRow == index && seg.match == s.find.cur.match {
			fill = tokens.Color(ui.ColorAccent)
		}
		ink := cell.ContrastInk(fill)
		for col := from; col < to && col < maxW; col++ {
			if selStart < selEnd && col >= selStart && col < selEnd {
				continue
			}
			c := buf.Get(x+col, y)
			c.FG, c.BG = ink, fill
			c.Attr &^= cell.AttrDim | cell.AttrReverse
			buf.Set(x+col, y, c)
		}
	}
}
