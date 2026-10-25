package shellchat

import (
	"strings"
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
	selecting     bool
	selAnchorLine int
	selAnchorCol  int
	selEndLine    int
	selEndCol     int
	selAutoScroll bool
	selMouseY     int
	dragged       bool
	sourceID      uint64
	pressAction   *ui.Action
}

func (ts *textSelection) Clear() {
	ts.selecting = false
	ts.selAutoScroll = false
	ts.selAnchorLine = 0
	ts.selAnchorCol = 0
	ts.selEndLine = 0
	ts.selEndCol = 0
	ts.selMouseY = 0
	ts.dragged = false
	ts.pressAction = nil
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
	return ts.selEndLine, ts.selEndCol, ts.selAnchorLine, ts.selAnchorCol
}

func (ts *textSelection) SelectedText(rows []chatprojection.MessageRow) string {
	if !ts.selecting {
		return ""
	}
	sLine, sCol, eLine, eCol := ts.Range()
	return extractSelectedText(rows, sLine, sCol, eLine, eCol)
}

func extractSelectedText(rows []chatprojection.MessageRow, startLine, startCol, endLine, endCol int) string {
	allLines := flattenMessageLines(rows)
	return rendering.ExtractSelectedText(allLines, startLine, startCol, endLine, endCol)
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
	s.selection.selAnchorCol = rendering.Max(0, ctx.LocalX-padLR)
	s.selection.selEndLine = lineIdx
	s.selection.selEndCol = s.selection.selAnchorCol
	s.selection.selMouseY = ctx.LocalY
	s.selection.selAutoScroll = false
	s.selection.dragged = msg.Mod != 0
	s.selection.sourceID = s.rowsSourceID
	s.selection.pressAction = s.inlineActionAt(lineIdx, s.selection.selAnchorCol)
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
	if !ok || lineIdx != s.selection.selAnchorLine || rendering.Max(0, ctx.LocalX-padLR) != s.selection.selAnchorCol {
		s.selection.dragged = true
	}
	if ok {
		s.selection.selEndLine = lineIdx
		s.selection.selEndCol = rendering.Max(0, ctx.LocalX-padLR)
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
		s.selection.selEndCol = rendering.Max(0, ctx.LocalX-s.layout.padLR)
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
		toggleMsgID = s.disclosureRowAt(lineIdx)
	}
	s.selection.selecting = false
	s.selection.selAutoScroll = false
	sLine, sCol, eLine, eCol := s.selection.Range()
	text := extractSelectedText(s.rows, sLine, sCol, eLine, eCol)
	text = strings.TrimRight(text, " \t\n\r")
	s.selection.selAnchorLine = 0
	s.selection.selAnchorCol = 0
	s.selection.selEndLine = 0
	s.selection.selEndCol = 0
	if inlineAction != nil {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint, Action: inlineAction}
	}
	if toggleMsgID != "" {
		return ui.EventResult{
			Handled:    true,
			Invalidate: ui.InvalidatePaint,
			Action: &ui.Action{
				ID:      chatprojection.ChatToggleActionID,
				Payload: chatprojection.ChatTogglePayload{MessageID: toggleMsgID},
			},
		}
	}
	var cmd tea.Cmd
	if text != "" {
		cmd = rendering.CopyToClipboardCmd(text)
	}
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) inlineActionAt(globalLine, col int) *ui.Action {
	for _, row := range s.rows {
		if globalLine >= max(1, len(row.Lines)) {
			globalLine -= max(1, len(row.Lines))
			continue
		}
		if globalLine < 0 || globalLine >= len(row.Native) {
			return nil
		}
		x := 0
		for _, span := range row.Native[globalLine].Spans {
			width := rendering.VisibleWidth(span.Text)
			if col >= x && col < x+width {
				return span.Action
			}
			x += width
		}
		return nil
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
		if row.Toggle && (row.ToggleAny || row.ToggleLine >= 0 && line == row.ToggleLine) {
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
