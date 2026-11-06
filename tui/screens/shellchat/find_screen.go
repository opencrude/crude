package shellchat

import (
	"sort"
	"time"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// The find window is the shell's one find design (see tui/ui/findreplace_frame.go);
// a chat owns the search, not the chrome. The Screen exposes the live result so
// the window can show the same counter and toggles an editor does.

// OpenFind starts a search on this chat. The query itself lives in the window's
// field, so the caller sets it with SetFindQuery.
func (s *Screen) OpenFind() {
	s.findOpen = true
	s.findSeen = time.Now()
	s.findDirty = true
}

// CloseFind drops the search and every highlight. The chat keeps nothing.
func (s *Screen) CloseFind() {
	if !s.findOpen {
		return
	}
	s.findOpen = false
	s.find.stop()
	s.findDirty = true
}

// FindOpen reports whether this chat is searching.
func (s *Screen) FindOpen() bool { return s.findOpen }

// SetFindQuery searches for query with the window's filter toggles. A new query
// starts from the current match's line, or from the middle of the view.
func (s *Screen) SetFindQuery(query string, opts FindOptions) {
	anchor := -1
	if line, ok := s.findLine(); ok {
		anchor = line
	}
	s.find.setQuery(query, opts, anchor)
	s.findSeen = time.Now()
	s.findDirty = true
	s.findChanged()
}

// FindCounts is the live match counter: how many matches there are and which
// one is selected (0-based, -1 when none). The window renders it exactly as it
// does for an editor.
func (s *Screen) FindCounts() (matchCount, currentIdx int) {
	if !s.find.querying() || len(s.find.counts) != len(s.rows) {
		return 0, -1
	}
	if !s.find.cur.ok {
		return s.find.total, -1
	}
	return s.find.total, s.find.ordinal() - 1
}

// FindError is an invalid regular expression, empty when the query is valid.
func (s *Screen) FindError() string { return s.find.err }

// FindStatusText is the counter text while a pass is still running; a settled
// search lets the shared frame derive "N of M" or "No results".
func (s *Screen) FindStatusText() string { return s.find.findStatusText() }

// SetFindChanged installs the callback that tells the owner of the find window
// that the count or the selected match moved.
func (s *Screen) SetFindChanged(fn func()) { s.onFindChanged = fn }

func (s *Screen) findChanged() {
	if s.onFindChanged != nil {
		s.onFindChanged()
	}
}

// FindPending reports whether matches are still being counted, so the shell
// keeps frames coming until the count is final.
func (s *Screen) FindPending() bool {
	return s.findOpen && s.find.pending(len(s.rows)) && time.Since(s.findSeen) < time.Second
}

// FindWaiting reports whether the open search still lacks the text of rows that
// were never drawn, so the owner of those rows should finish drawing them.
func (s *Screen) FindWaiting() bool {
	return s.findOpen && s.find.querying() && s.find.waiting > 0 && !s.find.pending(len(s.rows))
}

// TakeFindDirty reports, once, that a count or a highlight changed without a
// new set of rows, so the shell must repaint the chat.
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
	s.findChanged()
	return true
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
		s.findChanged()
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

// revealFind scrolls the selected match into view. A match already in view does
// not move the transcript; one outside lands in the middle with context.
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

// paintFindMatches marks the matches on one painted line. It restyles cells the
// line painter just wrote, so selection and syntax ink stay the single owner of
// the line and a text selection stays visible over a match.
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
		current := hasCurrent && currentRow == index && seg.match == s.find.cur.match
		fill := tokens.FindMatchFill(current)
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
