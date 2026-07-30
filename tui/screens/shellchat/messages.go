package shellchat

import (
	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// PaintParams carries per-frame compose/selection state for message RowPainter.
type PaintParams struct {
	Compose      ComposeParams
	PaneLineMap  []chatprojection.PaneLineInfo
	HasSelection bool
	SelStartLine int
	SelStartCol  int
	SelEndLine   int
	SelEndCol    int
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
func (s *Screen) SetMessages(rows []chatprojection.MessageRow, paint PaintParams) {
	if s == nil {
		return
	}
	s.rows = rows
	s.layout.padLR = paint.Compose.PadLR
	s.layout.mainSBVisible = paint.Compose.MainSBVisible
	items := make([]ui.VirtItem, len(rows))
	starts := make([]int, len(rows))
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
			Payload: row,
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
	s.list.SetRowPainter(s.paintMessageRow)
	s.list.SetItems(items)
	if s.list.Scroll().AutoScroll() {
		s.list.StickToBottom()
	}
}

func (s *Screen) paintMessageRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, _ ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	if buf == nil || item.Height < 1 {
		return
	}
	row, ok := item.Payload.(chatprojection.MessageRow)
	if !ok || localRow < 0 || localRow >= len(row.Lines) {
		return
	}

	globalLine := s.globalLineForItem(item.ID, localRow)
	scroll := s.list.Scroll()
	vpRow := globalLine - scroll.Offset()
	if vpRow < 0 {
		vpRow = 0
	}

	var paneInfo chatprojection.PaneLineInfo
	if globalLine >= 0 && globalLine < len(s.paintState.paneLineMap) {
		paneInfo = s.paintState.paneLineMap[globalLine]
	}

	selStart, selEnd := selectionCols(s.paintState, globalLine)
	useNative := localRow < len(row.Native) && !row.Native[localRow].PreferANSI()

	if useNative {
		pl := row.Native[localRow]
		layout := LayoutRow(s.paintState.compose, pl.VisibleWidth(), paneInfo)
		lineX := x + layout.PadLeft
		chatprojection.PaintLineAt(buf, lineX, y, layout.ContentMax, pl, tokens, selStart, selEnd, layout.HasPaneBg)
		drawn := pl.VisibleWidth()
		if drawn > layout.ContentMax {
			drawn = layout.ContentMax
		}
		col := drawn
		padX := lineX + drawn
		paintNativeSpaces(buf, padX, y, layout.PadRight, tokens, selStart, selEnd, layout.HasPaneBg, col)
		col += layout.PadRight
		if layout.GutterCols > ui.ScrollbarWidth {
			gap := layout.GutterCols - ui.ScrollbarWidth
			paintNativeSpaces(buf, padX+layout.PadRight, y, gap, tokens, 0, 0, layout.HasPaneBg, 0)
		}
	} else {
		raw := row.Lines[localRow]
		if selStart < selEnd {
			raw = rendering.ApplySelection(raw, selStart, selEnd)
		}
		line := ComposeRow(s.paintState.compose, vpRow, globalLine, raw, paneInfo)
		if line == "" {
			return
		}
		rendering.RenderToBufferAt(buf, x, y, line)
	}

	gx := NestedGutterX(s.paintState.compose, width)
	if gx >= 0 && paneInfo.PaneID != "" {
		s.paintNestedGutter(buf, x+gx, y, paneInfo)
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
			cf, cb = tokens.ListRowColors(ui.StateFocused)
		}
		buf.WriteString(x+i, y, " ", cf, cb, 0)
		col++
	}
}

func (s *Screen) globalLineForItem(id string, localRow int) int {
	items := s.list.Items()
	for i, it := range items {
		if it.ID != id {
			continue
		}
		if i < len(s.paintState.globalStart) {
			return s.paintState.globalStart[i] + localRow
		}
		break
	}
	return localRow
}
