package shellchat

import (
	"strings"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

// PaneGutterState carries per-pane scroll metadata for native nested gutter
// paint and input on shellchat.Screen.
type PaneGutterState struct {
	Scroll  *ui.ScrollModel
	TrackH  int
	Visible bool
}

// ComposeParams carries MessageList render output plus scrollbar/subpane
// nesting state needed to build chat rows (pad + content + nested pane gutters).
// Main chat scrollbar paint/drag is owned by ui.ScrollbarControl; rows reserve
// gutter width but never embed scrollbar glyphs (native paint only).
type ComposeParams struct {
	RawLines      []string
	InnerW        int
	PadLR         int
	Theme         *theme.Theme
	NestingLevel  int
	MainSBVisible bool // reserve external main gutter column (contentH > viewportH)
	PaneLineMap   []chatprojection.PaneLineInfo
	PaneGutters   map[string]PaneGutterState
	// HasPaneBg reports whether paneID should receive SubPaneBg (PaneAgent).
	HasPaneBg func(paneID string) bool
}

type composeCtx struct {
	pad          string
	rowInnerW    int // InnerW minus external main gutter
	paneSBW      int
	nestingLevel int
}

func buildComposeCtx(p ComposeParams) composeCtx {
	th := p.Theme
	if th == nil {
		th = theme.DefaultDark()
	}
	_ = th
	innerW := p.InnerW
	if innerW < 20 {
		innerW = 20
	}
	nestingLevel := p.NestingLevel

	mainGutter := 0
	if p.MainSBVisible {
		mainGutter = ui.ScrollbarWidth
	}
	paneSBW := 0
	if nestingLevel > 0 {
		paneSBW = ui.ScrollbarWidth*nestingLevel + nestingLevel
	}
	rowInnerW := innerW - mainGutter

	return composeCtx{
		pad:          strings.Repeat(" ", p.PadLR),
		rowInnerW:    rowInnerW,
		paneSBW:      paneSBW,
		nestingLevel: nestingLevel,
	}
}

// RowLayout describes compose geometry for one chat row at paint time.
type RowLayout struct {
	PadLeft    int
	RowInnerW  int
	ContentMax int
	PadRight   int
	GutterCols int // gap + scrollbar column reserve (spaces only)
	HasPaneBg  bool
}

// LayoutRow mirrors ComposeRow width math without building ANSI strings.
func LayoutRow(p ComposeParams, contentVisW int, paneInfo chatprojection.PaneLineInfo) RowLayout {
	ctx := buildComposeCtx(p)
	paneGap := 0
	if ctx.paneSBW > 0 {
		paneGap = 1
	}
	maxContentW := ctx.rowInnerW - ctx.paneSBW - paneGap
	if maxContentW < 10 {
		maxContentW = 10
	}

	hasBg := false
	if paneInfo.PaneID != "" && p.HasPaneBg != nil {
		hasBg = p.HasPaneBg(paneInfo.PaneID)
	}

	visW := contentVisW
	if visW > maxContentW {
		visW = maxContentW
	}

	padRight := ctx.rowInnerW - ctx.paneSBW - paneGap - visW
	if padRight < 0 {
		padRight = 0
	}
	gutterCols := 0
	if ctx.paneSBW > 0 {
		gutterCols = paneGap + ui.ScrollbarWidth
	} else {
		padRight = ctx.rowInnerW - visW
		if padRight < 0 {
			padRight = 0
		}
	}

	return RowLayout{
		PadLeft:    p.PadLR,
		RowInnerW:  ctx.rowInnerW,
		ContentMax: maxContentW,
		PadRight:   padRight,
		GutterCols: gutterCols,
		HasPaneBg:  hasBg,
	}
}

// ComposeRow builds one full-width composed chat row. Compose reserves nested
// gutter columns as spaces only; shellchat paints native gutter cells after
// content in the RowPainter.
func ComposeRow(p ComposeParams, vpRow int, globalLine int, rawLine string, paneInfo chatprojection.PaneLineInfo) string {
	if rawLine == "" && p.InnerW < 1 {
		return ""
	}
	ctx := buildComposeCtx(p)
	return composeOneRow(ctx, p, vpRow, globalLine, rawLine, paneInfo)
}

func composeOneRow(ctx composeCtx, p ComposeParams, _ int, _ int, line string, paneInfo chatprojection.PaneLineInfo) string {
	paneGap := 0
	if ctx.paneSBW > 0 {
		paneGap = 1
	}
	maxContentW := ctx.rowInnerW - ctx.paneSBW - paneGap
	if maxContentW < 10 {
		maxContentW = 10
	}

	isPaneLine := paneInfo.PaneID != ""
	hasBg := false
	if isPaneLine && p.HasPaneBg != nil {
		hasBg = p.HasPaneBg(paneInfo.PaneID)
	}

	var trimmed string
	var visW int
	if hasBg {
		trimmed = line
		visW = rendering.VisibleWidth(trimmed)
		if visW > maxContentW {
			trimmed = rendering.TruncateLine(trimmed, maxContentW)
			visW = rendering.VisibleWidth(trimmed)
		}
	} else {
		trimmed = rendering.TrimRightAnsiSpaces(line)
		visW = rendering.VisibleWidth(trimmed)
		if visW > maxContentW {
			trimmed = rendering.TruncateLine(trimmed, maxContentW)
			visW = rendering.VisibleWidth(trimmed)
		}
	}
	line = trimmed

	if ctx.paneSBW > 0 {
		padRight := ctx.rowInnerW - ctx.paneSBW - paneGap - visW
		if padRight < 0 {
			padRight = 0
		}
		// Reserve gap + one gutter column as spaces; native paint fills gutter cells.
		// The subpane background is applied natively by PaintLineAt (subPaneBg flag),
		// so no raw SGR background hack is emitted here.
		inner := line + strings.Repeat(" ", padRight) + strings.Repeat(" ", paneGap+ui.ScrollbarWidth)
		return ctx.pad + inner + ctx.pad
	}

	padRight := ctx.rowInnerW - visW
	if padRight < 0 {
		padRight = 0
	}
	inner := line + strings.Repeat(" ", padRight)
	return ctx.pad + inner + ctx.pad
}

// Compose builds full-width chat rows with scrollbar gutters and subpane
// backgrounds. Output rows are consumed by tests; production uses
// message-level VirtItems via Screen.SetMessages and PaintLineAt at paint time.
func Compose(p ComposeParams) []string {
	if len(p.RawLines) == 0 {
		return nil
	}
	ctx := buildComposeCtx(p)
	out := make([]string, len(p.RawLines))
	for i, line := range p.RawLines {
		var paneInfo chatprojection.PaneLineInfo
		if i < len(p.PaneLineMap) {
			paneInfo = p.PaneLineMap[i]
		}
		out[i] = composeOneRow(ctx, p, i, i, line, paneInfo)
	}
	return out
}

// NestedGutterX returns the list-local X of the nested pane gutter column.
func NestedGutterX(p ComposeParams, listW int) int {
	ctx := buildComposeCtx(p)
	if ctx.paneSBW <= 0 {
		return -1
	}
	_ = listW
	return p.PadLR + ctx.rowInnerW - ui.ScrollbarWidth
}
