package shellchat

import (
	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/ui"
)

// PaintParams carries per-frame compose/selection state for message RowPainter.
type PaintParams struct {
	// SourceID scopes Generation to one retained chatprojection.Renderer.
	// A source switch always rebuilds the shared VirtualList, even when the
	// incoming renderer has the same local generation and row count.
	SourceID         uint64
	Generation       uint64
	ChangedIndices   []int
	StructureChanged bool
	Compose          ComposeParams
	PaneLineMap      []chatprojection.PaneLineInfo
	HasSelection     bool
	SelStartLine     int
	SelStartCol      int
	SelEndLine       int
	SelEndCol        int
	OnPaneScroll     func(string)
}

type messagePaintState struct {
	compose     ComposeParams
	paneLineMap []chatprojection.PaneLineInfo
	globalStart []int
	hasSel      bool
	selSL       int
	selSC       int
	selEL       int
	selEC       int
}

// SetMessages installs message-level VirtItems (Height = len(Lines)) and paint
// context for RowPainter compose + selection. ScrollModel content height follows
// the sum of item heights.
// Rows are borrowed on the UI goroutine. Item indices resolve against the current
// snapshot, never boxed row copies or pointers into replaced snapshot storage.
func (s *Screen) SetMessages(rows []chatprojection.MessageRow, paint PaintParams) {
	if s == nil {
		return
	}
	oldLen := len(s.rows)
	s.rows = rows
	s.onPaneScroll = paint.OnPaneScroll
	s.layout.padLR = paint.Compose.PadLR
	s.layout.mainSBVisible = paint.Compose.MainSBVisible
	sourceChanged := s.rowsSourceID != paint.SourceID
	sameRows := !sourceChanged && paint.Generation != 0 && s.rowsGeneration == paint.Generation
	starts := s.paintState.globalStart
	if sameRows {
		hasSel := paint.HasSelection || s.selection.HasActive()
		selSL, selSC, selEL, selEC := paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol
		if hasSel && !paint.HasSelection && s.selection.HasActive() {
			selSL, selSC, selEL, selEC = s.selection.Range()
		}
		s.paintState = messagePaintState{compose: paint.Compose, paneLineMap: paint.PaneLineMap, globalStart: starts, hasSel: hasSel, selSL: selSL, selSC: selSC, selEL: selEL, selEC: selEC}
		return
	}
	if !sourceChanged && paint.Generation != 0 && s.rowsGeneration != 0 && !paint.StructureChanged && oldLen == len(rows) {
		hasSel := paint.HasSelection || s.selection.HasActive()
		selSL, selSC, selEL, selEC := paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol
		if hasSel && !paint.HasSelection && s.selection.HasActive() {
			selSL, selSC, selEL, selEC = s.selection.Range()
		}
		s.paintState = messagePaintState{compose: paint.Compose, paneLineMap: paint.PaneLineMap, globalStart: starts, hasSel: hasSel, selSL: selSL, selSC: selSC, selEL: selEL, selEC: selEC}
		for _, i := range paint.ChangedIndices {
			if i < 0 || i >= len(rows) {
				continue
			}
			row := rows[i]
			s.list.UpdateItem(i, ui.VirtItem{ID: row.ID, Height: max(1, len(row.Lines)), Payload: i})
		}
		s.rowsSourceID = paint.SourceID
		s.rowsGeneration = paint.Generation
		return
	}
	items := s.list.Items()
	if cap(items) < len(rows) {
		items = make([]ui.VirtItem, len(rows))
	} else {
		if len(items) > len(rows) {
			clear(items[len(rows):]) // release removed source IDs and payloads
		}
		items = items[:len(rows)]
	}
	if cap(starts) < len(rows) {
		starts = make([]int, len(rows))
	} else {
		starts = starts[:len(rows)]
	}
	global := 0
	for i, row := range rows {
		starts[i] = global
		h := len(row.Lines)
		if h < 1 {
			h = 1
		}
		items[i] = ui.VirtItem{
			ID:      row.ID,
			Height:  h,
			Payload: i,
		}
		global += h
	}
	hasSel := paint.HasSelection || s.selection.HasActive()
	selSL, selSC, selEL, selEC := paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol
	if hasSel && !paint.HasSelection && s.selection.HasActive() {
		selSL, selSC, selEL, selEC = s.selection.Range()
	}
	s.paintState = messagePaintState{
		compose:     paint.Compose,
		paneLineMap: paint.PaneLineMap,
		globalStart: starts,
		hasSel:      hasSel,
		selSL:       selSL,
		selSC:       selSC,
		selEL:       selEL,
		selEC:       selEC,
	}
	s.list.SetItems(items)
	s.rowsSourceID = paint.SourceID
	s.rowsGeneration = paint.Generation
	if s.list.Scroll().AutoScroll() {
		s.list.StickToBottom()
	}
}

func (s *Screen) MessageAtLocalY(localY int) (chatprojection.MessageRow, bool) {
	if s == nil || s.list == nil {
		return chatprojection.MessageRow{}, false
	}
	item, ok := s.list.ItemAtViewportRow(localY)
	if !ok {
		return chatprojection.MessageRow{}, false
	}
	index, ok := item.Payload.(int)
	if !ok || index < 0 || index >= len(s.rows) {
		return chatprojection.MessageRow{}, false
	}
	return s.rows[index], true
}

func (s *Screen) paintMessageRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, _ ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	if buf == nil || item.Height < 1 {
		return
	}
	index, ok := item.Payload.(int)
	if !ok || index < 0 || index >= len(s.rows) || index >= len(s.paintState.globalStart) {
		return
	}
	row := s.rows[index]
	if localRow < 0 || localRow >= len(row.Lines) {
		return
	}

	globalLine := s.paintState.globalStart[index] + localRow

	var paneInfo chatprojection.PaneLineInfo
	if globalLine >= 0 && globalLine < len(s.paintState.paneLineMap) {
		paneInfo = s.paintState.paneLineMap[globalLine]
	}

	selStart, selEnd := selectionCols(s.paintState, globalLine)

	// Native rows are always real after the ANSI-fallback deletion. The
	// PreferANSI branch is gone; if a row ever lacks a native entry we build
	// a plain body line from the text so painting never touches the ANSI path.
	var pl chatprojection.PaintLine
	if localRow < len(row.Native) {
		pl = row.Native[localRow]
	} else {
		pl = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: row.Lines[localRow], Role: ui.ColorFg}}}
	}
	layout := LayoutRow(s.paintState.compose, pl.VisibleWidth(), paneInfo)
	lineX := x + layout.PadLeft
	if row.ContextBoundary && localRow >= row.ContextBoundaryStart {
		bg := tokens.Color(ui.ColorBg)
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
		if localRow == row.ContextBoundaryStart || localRow == len(row.Lines)-1 {
			fg := tokens.Color(ui.ColorAccent)
			rule := ui.FirstRune(ui.DefaultGlyphsFromTokens(tokens).BorderH, '─')
			for col := 0; col < width; col++ {
				cellFG, cellBG := fg, bg
				contentCol := col - layout.PadLeft
				if selStart < selEnd && contentCol >= selStart && contentCol < selEnd {
					cellFG = tokens.Color(ui.ColorFg)
					cellBG = tokens.Color(ui.ColorSelection)
				}
				buf.Set(x+col, y, cell.NewCell(rule, cellFG, cellBG, 0))
			}
			return
		}
		chatprojection.PaintLineAtBackground(buf, lineX, y, layout.ContentMax, pl, tokens, selStart, selEnd, bg)
		return
	}
	bg := cell.NoColor
	if pl.PaneBackground {
		bg = tokens.Color(ui.ColorSubPaneBg)
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}
	chatprojection.PaintLineAtBackground(buf, lineX, y, layout.ContentMax, pl, tokens, selStart, selEnd, bg)
	drawn := pl.VisibleWidth()
	if drawn > layout.ContentMax {
		drawn = layout.ContentMax
	}
	col := drawn
	padX := lineX + drawn
	paintNativeSpaces(buf, padX, y, layout.PadRight, tokens, selStart, selEnd, pl.PaneBackground, col)
	if pl.HeaderRule {
		// Only the margins are outside PaintLineAt's selection columns. The
		// label, faint rule, and timestamp inside the content use native spans.
		fg := tokens.Color(ui.ColorBorder)
		bg := tokens.Color(ui.ColorBg)
		rule := ui.FirstRune(ui.DefaultGlyphsFromTokens(tokens).BorderH, '─')
		for edge := x; edge < lineX && edge < x+width; edge++ {
			buf.Set(edge, y, cell.NewCell(rule, fg, bg, 0))
		}
		for edge := lineX + layout.ContentMax; edge < x+width; edge++ {
			buf.Set(edge, y, cell.NewCell(rule, fg, bg, 0))
		}
	}

	gx := NestedGutterX(s.paintState.compose, width)
	if gx >= 0 && paneInfo.PaneID != "" {
		s.paintNestedGutter(buf, x+gx, y, paneInfo, bg)
	}
	if gx >= 0 && layout.GutterCols > ui.ScrollbarWidth {
		// The separator remains blank and routes input to the outer chat, but
		// shares the row's background so the pane band is full width.
		paintNativeSpaces(buf, x+gx+ui.ScrollbarWidth, y, layout.GutterCols-ui.ScrollbarWidth, tokens, 0, 0, pl.PaneBackground, 0)
	}
}

func selectionCols(st messagePaintState, globalLine int) (start, end int) {
	if !st.hasSel || globalLine < st.selSL || globalLine > st.selEL {
		return 0, 0
	}
	start, end = 0, 1<<30
	if globalLine == st.selSL {
		start = st.selSC
	}
	if globalLine == st.selEL {
		end = st.selEC
	}
	return start, end
}

func paintNativeSpaces(buf *surface.CellBuffer, x, y, count int, tokens *ui.TokenSet, selStart, selEnd int, subPaneBg bool, colOffset int) {
	if count <= 0 {
		return
	}
	bg := cell.NoColor
	fg := cell.NoColor
	if subPaneBg && tokens != nil {
		bg = tokens.Color(ui.ColorSubPaneBg)
		fg = tokens.Color(ui.ColorFg)
	}
	col := colOffset
	for i := 0; i < count; i++ {
		cf, cb := fg, bg
		if selStart < selEnd && col >= selStart && col < selEnd && tokens != nil {
			// Text selection is not a focused list row — always Selection.
			cf = tokens.Color(ui.ColorFg)
			cb = tokens.Color(ui.ColorSelection)
		}
		buf.Set(x+i, y, cell.Cell{Char: ' ', Content: " ", Width: 1, FG: cf, BG: cb})
		col++
	}
}
