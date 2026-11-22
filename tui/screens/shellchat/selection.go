package shellchat

import (
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
)

const selScrollInterval = 60 * time.Millisecond

// textSelection owns mouse text selection over flattened message rows.
type textSelection struct {
	selecting               bool
	selAnchorLine           int
	selAnchorCol            int
	pressedWidth            int
	pressedEndSource        chatprojection.CopyAnchor
	selEndLine              int
	selEndCol               int
	selAutoScroll           bool
	selMouseY               int
	dragged                 bool
	sourceID                uint64
	pressAction             *ui.Action
	anchorSource, endSource chatprojection.CopyAnchor
	sourceAnchored          bool
}

func (ts *textSelection) Clear() {
	ts.selecting = false
	ts.selAutoScroll = false
	ts.selAnchorLine = 0
	ts.selAnchorCol = 0
	ts.pressedWidth = 0
	ts.pressedEndSource = chatprojection.CopyAnchor{}
	ts.selEndLine = 0
	ts.selEndCol = 0
	ts.selMouseY = 0
	ts.dragged = false
	ts.pressAction = nil
	ts.sourceAnchored = false
	ts.anchorSource = chatprojection.CopyAnchor{}
	ts.endSource = chatprojection.CopyAnchor{}
}

func (ts *textSelection) HasActive() bool {
	if !ts.selecting && ts.selAnchorLine == ts.selEndLine && ts.selAnchorCol == ts.selEndCol {
		return false
	}
	return ts.selAnchorLine != ts.selEndLine || ts.selAnchorCol != ts.selEndCol
}

func (ts *textSelection) IsSelecting() bool { return ts.selecting }

func (ts *textSelection) Range() (startLine, startCol, endLine, endCol int) {
	if ts.selAnchorLine < ts.selEndLine ||
		(ts.selAnchorLine == ts.selEndLine && ts.selAnchorCol <= ts.selEndCol) {
		return ts.selAnchorLine, ts.selAnchorCol, ts.selEndLine, ts.selEndCol
	}
	return ts.selEndLine, ts.selEndCol, ts.selAnchorLine, ts.projectedAnchor()
}

func (ts *textSelection) backward() bool {
	return ts.selEndLine < ts.selAnchorLine || ts.selEndLine == ts.selAnchorLine && ts.selEndCol < ts.selAnchorCol
}

// All cells of the pressed grapheme hit the same insertion boundary. A
// click on its continuation is still a click, not a one-column selection.
func (ts *textSelection) headColumn(line, col int) int {
	if line == ts.selAnchorLine && col >= ts.selAnchorCol && col < ts.selAnchorCol+ts.pressedWidth {
		return ts.selAnchorCol
	}
	return col
}

func (ts *textSelection) projectedAnchor() int {
	return rendering.CharacterDragAnchor(ts.selAnchorCol, ts.selAnchorCol+ts.pressedWidth, ts.backward())
}

func (ts *textSelection) SelectedText(rows []chatprojection.MessageRow) string {
	if !ts.selecting || !ts.HasActive() {
		return ""
	}
	if source, ok := ts.sourceRange(); ok {
		return source.String()
	}
	sLine, sCol, eLine, eCol := ts.Range()
	return extractSelectedText(rows, sLine, sCol, eLine, eCol)
}

func extractSelectedText(rows []chatprojection.MessageRow, startLine, startCol, endLine, endCol int) string {
	return chatprojection.CopySelection(rows, startLine, startCol, endLine, endCol)
}

func flattenMessageLines(rows []chatprojection.MessageRow) []string {
	total := 0
	for _, row := range rows {
		h := len(row.Lines)
		if h < 1 {
			h = 1
		}
		total += h
	}
	all := make([]string, 0, total)
	for _, row := range rows {
		if len(row.Lines) == 0 {
			all = append(all, "")
			continue
		}
		for i, line := range row.Lines {
			// After the ANSI-fallback deletion, native rows are always real
			// and parallel to Lines. Prefer the native plain text (grapheme
			// space) so selection column coordinates match painted cells.
			if i < len(row.Native) {
				all = append(all, row.Native[i].PlainText())
				continue
			}
			all = append(all, rendering.StripAnsi(line))
		}
	}
	return all
}

func (s *Screen) mouseToGlobalLine(localY int) (globalLine int, ok bool) {
	scroll := s.list.Scroll()
	contentH := scroll.Content()
	viewportH := scroll.Viewport()
	if contentH <= 0 || viewportH <= 0 {
		return -1, false
	}
	if localY < 0 || localY >= viewportH {
		return -1, false
	}
	scrollY := scroll.Offset()
	if contentH <= viewportH {
		emptyTop := viewportH - contentH
		if localY < emptyTop {
			return -1, false
		}
		globalLine = localY - emptyTop
	} else {
		globalLine = scrollY + localY
		if globalLine >= contentH {
			return -1, false
		}
	}
	if globalLine < 0 || globalLine >= contentH {
		return -1, false
	}
	return globalLine, true
}

func (s *Screen) chatAreaContentBounds() (firstLine, lastLine int) {
	scroll := s.list.Scroll()
	contentH := scroll.Content()
	viewportH := scroll.Viewport()
	if contentH <= 0 {
		return -1, -1
	}
	scrollY := scroll.Offset()
	endLine := scrollY + viewportH
	if endLine > contentH {
		endLine = contentH
	}
	startLine := scrollY
	if startLine >= contentH {
		return -1, -1
	}
	return startLine, endLine - 1
}

func (s *Screen) handleSelectionClick(ctx ui.EventContext, msg tea.MouseClickMsg) ui.EventResult {
	if msg.Button != tea.MouseLeft {
		s.selection.Clear()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	lineIdx, ok := s.mouseToGlobalLine(ctx.LocalY)
	if !ok {
		s.selection.Clear()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	padLR := s.layout.padLR
	s.selection.selecting = true
	s.selection.selAnchorLine = lineIdx
	hit := s.logicalColumn(lineIdx, rendering.Max(0, ctx.LocalX-padLR))
	start, end := s.pressedUnit(lineIdx, hit)
	s.selection.selAnchorCol, s.selection.pressedWidth = start, end-start
	s.selection.selEndLine = lineIdx
	s.selection.selEndCol = s.selection.selAnchorCol
	s.selection.selMouseY = ctx.LocalY
	s.selection.selAutoScroll = false
	s.selection.dragged = msg.Mod != 0
	s.selection.sourceID = s.rowsSourceID
	s.selection.pressAction = s.inlineActionAt(lineIdx, s.selection.selAnchorCol)
	if s.selection.pressAction == nil {
		s.captureSelectionSource()
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) handleSelectionMotion(ctx ui.EventContext, msg tea.MouseMotionMsg) ui.EventResult {
	if !s.selection.selecting {
		return ui.EventResult{}
	}
	scroll := s.list.Scroll()
	viewportH := scroll.Viewport()
	padLR := s.layout.padLR

	lineIdx, ok := s.mouseToGlobalLine(ctx.LocalY)
	col := rendering.Max(0, ctx.LocalX-padLR)
	if ok {
		col = s.selection.headColumn(lineIdx, s.logicalColumn(lineIdx, col))
	}
	if !ok || lineIdx != s.selection.selAnchorLine || col != s.selection.selAnchorCol {
		s.selection.dragged = true
	}
	if ok {
		s.selection.selEndLine = lineIdx
		s.selection.selEndCol = col
	} else if ctx.LocalY < 0 {
		firstLine, _ := s.chatAreaContentBounds()
		if firstLine >= 0 {
			s.selection.selEndLine = firstLine
			s.selection.selEndCol = 0
		}
	} else if ctx.LocalY >= viewportH {
		_, lastLine := s.chatAreaContentBounds()
		if lastLine >= 0 {
			s.selection.selEndLine = lastLine
			s.selection.selEndCol = 1 << 30
		}
	}
	s.selection.selMouseY = ctx.LocalY
	s.captureSelectionSource()

	if !s.selection.selAutoScroll && (s.selection.selMouseY <= -1 || s.selection.selMouseY >= viewportH) {
		s.selection.selAutoScroll = true
		return ui.EventResult{
			Handled:    true,
			Cmd:        selectionTickCmd(),
			Invalidate: ui.InvalidatePaint,
		}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) handleSelectionRelease(ctx ui.EventContext, msg tea.MouseReleaseMsg) ui.EventResult {
	if !s.selection.selecting || msg.Button != tea.MouseLeft {
		return ui.EventResult{}
	}
	lineIdx, ok := s.mouseToGlobalLine(ctx.LocalY)
	if ok {
		s.selection.selEndLine = lineIdx
		s.selection.selEndCol = s.selection.headColumn(lineIdx, s.logicalColumn(lineIdx, rendering.Max(0, ctx.LocalX-s.layout.padLR)))
	}
	if s.selection.HasActive() {
		s.captureSelectionSource()
	}
	// A click without drag on a disclosure row toggles the block instead of
	// copying an empty selection. Drags (anchor != end) always select.
	clickNoDrag := !s.selection.dragged && s.selection.sourceID == s.rowsSourceID && s.selection.selAnchorLine == s.selection.selEndLine &&
		s.selection.selAnchorCol == s.selection.selEndCol
	toggleMsgID := ""
	var inlineAction *ui.Action
	if clickNoDrag && ok {
		inlineAction = s.inlineActionAt(lineIdx, s.selection.selEndCol)
		if inlineAction != s.selection.pressAction {
			inlineAction = nil
		}
		if inlineAction == nil {
			toggleMsgID = s.disclosureRowAt(lineIdx)
		}
	}
	text := s.selection.SelectedText(s.rows)

	s.selection.Clear()
	if inlineAction != nil {
		if inlineAction.ID == chatprojection.ChatFileOpenActionID {
			index := sort.Search(len(s.paintState.globalStart), func(i int) bool { return s.paintState.globalStart[i] > lineIdx }) - 1
			if index >= 0 && index < len(s.rows) && s.rows[index].ExecutionID != "" {
				if value, ok := inlineAction.Payload.(string); ok {
					inlineAction = &ui.Action{ID: inlineAction.ID, Payload: chatprojection.FileReference{Target: value, ExecutionID: s.rows[index].ExecutionID}}
				}
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint, Action: inlineAction}
	}
	if toggleMsgID != "" {
		return ui.EventResult{
			Handled:    true,
			Invalidate: ui.InvalidatePaint,
			Action: &ui.Action{
				ID:      chatprojection.ChatToggleActionID,
				Payload: chatprojection.ChatTogglePayload{AgentID: s.agentID, MessageID: toggleMsgID},
			},
		}
	}
	var cmd tea.Cmd
	if text != "" {
		cmd = s.copyText(text)
	}
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) inlineActionAt(globalLine, col int) *ui.Action {
	starts := s.paintState.globalStart
	index := sort.Search(len(starts), func(i int) bool { return starts[i] > globalLine }) - 1
	if index < 0 || index >= len(s.rows) {
		return nil
	}
	row := s.rows[index]
	line := globalLine - starts[index]
	if line < 0 || line >= len(row.Native) {
		return nil
	}
	x := 0
	for _, span := range row.Native[line].Spans {
		width := rendering.VisibleWidth(span.Text)
		if col >= x && col < x+width {
			return span.Action
		}
		x += width
	}
	return nil
}

// disclosureRowAt returns the message ID whose toggle row occupies the global
// line, or "" when the line is not a disclosure anchor.
func (s *Screen) disclosureRowAt(globalLine int) string {
	line := globalLine
	for _, row := range s.rows {
		h := len(row.Lines)
		if h < 1 {
			h = 1
		}
		if line >= h {
			line -= h
			continue
		}
		if leafID := row.LeafToggles[line]; leafID != "" {
			return leafID
		}
		if row.Toggle && (row.ToggleAny || row.ToggleLine >= 0 && line >= row.ToggleLine) {
			return row.ID
		}
		return ""
	}
	return ""
}

func (s *Screen) handleSelectionTick() ui.EventResult {
	if !s.selection.selecting || !s.selection.selAutoScroll {
		return ui.EventResult{}
	}
	scroll := s.list.Scroll()
	viewportH := scroll.Viewport()

	if s.selection.selMouseY <= -1 {
		scroll.ScrollBy(-1)
		scroll.SetAutoScroll(false)
		if lineIdx, ok := s.mouseToGlobalLine(0); ok {
			s.selection.selEndLine = lineIdx
			s.selection.selEndCol = 0
		}
	} else if s.selection.selMouseY >= viewportH {
		scroll.ScrollBy(1)
		scroll.SetAutoScroll(false)
		if lineIdx, ok := s.mouseToGlobalLine(viewportH - 1); ok {
			s.selection.selEndLine = lineIdx
			s.selection.selEndCol = 1 << 30
		}
	}

	s.captureSelectionSource()
	if s.selection.selMouseY <= -1 || s.selection.selMouseY >= viewportH {
		return ui.EventResult{
			Handled:    true,
			Cmd:        selectionTickCmd(),
			Invalidate: ui.InvalidatePaint,
		}
	}
	s.selection.selAutoScroll = false
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func selectionTickCmd() tea.Cmd {
	return tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
		return types.SelectionTickMsg(t)
	})
}

// Pointer input addresses two rows, not the whole transcript. The projection's
// retained row starts are also used by painting and bidi hit testing.
func (s *Screen) captureSelectionSource() {
	anchor := func(line, col int) (chatprojection.CopyAnchor, bool) {
		starts := s.paintState.globalStart
		index := sort.Search(len(starts), func(i int) bool { return starts[i] > line }) - 1
		if index < 0 || index >= len(s.rows) {
			return chatprojection.CopyAnchor{}, false
		}
		return chatprojection.AnchorAt(s.rows[index:index+1], line-starts[index], col)
	}
	ts := &s.selection
	a, aok := anchor(ts.selAnchorLine, ts.selAnchorCol)
	b, bok := anchor(ts.selEndLine, ts.selEndCol)
	trailing := a
	if ts.pressedWidth > 0 {
		if e, ok := anchor(ts.selAnchorLine, ts.selAnchorCol+ts.pressedWidth); ok && e.Source == a.Source {
			trailing = e
		}
	}
	ts.anchorSource, ts.pressedEndSource, ts.endSource, ts.sourceAnchored = a, trailing, b, aok && bok
}
func (s *textSelection) reflow(rows []chatprojection.MessageRow) {
	if !s.selecting || !s.sourceAnchored {
		return
	}
	a, ac, aok := chatprojection.LocateAnchor(rows, s.anchorSource)
	b, bc, bok := chatprojection.LocateAnchor(rows, s.endSource)
	if !aok || !bok {
		s.Clear()
		return
	}
	s.selAnchorLine, s.selAnchorCol, s.selEndLine, s.selEndCol = a, ac, b, bc
	// The new layout can use a different CopyText instance for the same source.
	s.anchorSource, _ = chatprojection.AnchorAt(rows, a, ac)
	s.endSource, _ = chatprojection.AnchorAt(rows, b, bc)
	s.pressedWidth = 0
	if y, x, ok := chatprojection.LocateAnchor(rows, s.pressedEndSource); ok && y == a {
		s.pressedWidth = max(0, x-ac)
		s.pressedEndSource, _ = chatprojection.AnchorAt(rows, y, x)
	}
}

// One semantic text owns its logical order even when table layout interleaves
// neighboring cells between its wrapped rows. Copy and highlight use this same
// range; mixed-source selections still collect the selected mapped runs.
func (s *textSelection) sourceRange() (rendering.CopyRange, bool) {
	a, b := s.anchorSource, s.endSource
	if !s.selecting || !s.sourceAnchored || a.Source != b.Source || a.Offset == b.Offset {
		return rendering.CopyRange{}, false
	}
	// Table cells and bidi runs need not appear in source order. Choose the
	// edge by source offsets once both endpoints identify the same source.
	a = rendering.CharacterDragAnchor(a, s.pressedEndSource, b.Offset < a.Offset)
	source := rendering.CopyRange{Text: a.Source, Start: min(a.Offset, b.Offset), End: max(a.Offset, b.Offset)}
	return source, source.Valid()
}

// pressedUnit finds both edges of the pressed grapheme once, in logical row
// coordinates. Copied source units expanded by the renderer (such as tabs)
// stay atomic. Layout padding and EOL carry no trailing source character.
func (s *Screen) pressedUnit(line, col int) (int, int) {
	starts := s.paintState.globalStart
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > line }) - 1
	if i < 0 || i >= len(s.rows) {
		return col, col
	}
	row := s.rows[i]
	y := line - starts[i]
	if y < 0 {
		return col, col
	}
	var pl chatprojection.PaintLine
	if y < len(row.Native) {
		pl = row.Native[y]
	} else if y < len(row.Lines) {
		pl.Spans = []chatprojection.RowSpan{{Text: rendering.StripAnsi(row.Lines[y])}}
	}
	x := 0
	for _, span := range pl.Spans {
		w := rendering.VisibleWidth(span.Text)
		if col >= x+w {
			x += w
			continue
		}
		if pl.SourceAware && !span.Copy.Valid() {
			return col, col
		}
		if span.Copy.Valid() && span.Copy.End-span.Copy.Start != len(span.Text) {
			return x, x + w
		}
		for text := span.Text; text != ""; {
			g, n := rendering.FirstGrapheme(text)
			if g == "" {
				break
			}
			if col < x+n {
				return x, x + n
			}
			x += n
			text = text[len(g):]
		}
		break
	}
	return col, col
}
