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
}

type composeCtx struct {
	pad       string
	padW      int
	rowInnerW int // InnerW minus external main gutter
	paneSBW   int
	paneGapW  int
}

func buildComposeCtx(p ComposeParams) composeCtx {
	innerW := max(0, p.InnerW)
	padW := max(0, p.PadLR)
	nestingLevel := max(0, p.NestingLevel)

	mainGutter := 0
	if p.MainSBVisible && innerW > 0 {
		mainGutter = ui.ScrollbarWidth
	}
	paneSBW := 0
	if nestingLevel > 0 {
		paneSBW = ui.ScrollbarWidth * nestingLevel
	}
	rowInnerW := max(0, innerW-mainGutter)
	paneGapW := 0
	if paneSBW > 0 {
		paneGapW = 1
	}

	return composeCtx{
		pad:       strings.Repeat(" ", padW),
		padW:      padW,
		rowInnerW: rowInnerW,
		paneSBW:   paneSBW,
		paneGapW:  paneGapW,
	}
}

type composeRowGeometry struct {
	contentMax int
	padRight   int
	gutter     int
}

// layoutComposeRow is the single source for both native paint geometry and
// composed test rows. Every value is bounded by rowInnerW; narrow terminals
// never gain synthetic minimum-width columns.
func layoutComposeRow(ctx composeCtx, contentVisW int) composeRowGeometry {
	contentW := max(0, contentVisW)
	contentMax := ctx.rowInnerW
	gutter := 0
	if ctx.paneSBW > 0 {
		gutter = min(ctx.rowInnerW, ctx.paneSBW+ctx.paneGapW)
		contentMax = max(0, ctx.rowInnerW-gutter)
	}
	contentW = min(contentW, contentMax)

	padRight := max(0, contentMax-contentW)
	return composeRowGeometry{
		contentMax: contentMax,
		padRight:   padRight,
		gutter:     gutter,
	}
}

// RowLayout describes compose geometry for one chat row at paint time.
type RowLayout struct {
	PadLeft    int
	RowInnerW  int
	ContentMax int
	PadRight   int
	GutterCols int // pane scrollbar followed by one blank separator column
}

// LayoutRow mirrors ComposeRow width math without building ANSI strings.
func LayoutRow(p ComposeParams, contentVisW int, _ chatprojection.PaneLineInfo) RowLayout {
	ctx := buildComposeCtx(p)
	geometry := layoutComposeRow(ctx, contentVisW)

	return RowLayout{
		PadLeft:    ctx.padW,
		RowInnerW:  ctx.rowInnerW,
		ContentMax: geometry.contentMax,
		PadRight:   geometry.padRight,
		GutterCols: geometry.gutter,
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
	return composeOneRow(ctx, rawLine)
}

func composeOneRow(ctx composeCtx, line string) string {
	trimmed := rendering.TrimRightAnsiSpaces(line)
	visW := rendering.VisibleWidth(trimmed)
	geometry := layoutComposeRow(ctx, visW)
	if visW > geometry.contentMax {
		if geometry.contentMax == 0 {
			trimmed = ""
		} else {
			trimmed = rendering.TruncateLine(trimmed, geometry.contentMax)
		}
		visW = rendering.VisibleWidth(trimmed)
		geometry = layoutComposeRow(ctx, visW)
	}

	// The gutter reserve is spaces only; native paint fills the final gutter
	// cell. At narrow widths it shrinks with the row instead of overflowing.
	inner := trimmed + strings.Repeat(" ", geometry.padRight) + strings.Repeat(" ", geometry.gutter)
	return ctx.pad + inner + ctx.pad
}

// Compose builds full-width chat row geometry with scrollbar gutters.
// Backgrounds are native PaintLine metadata, not composed text. Tests consume
// these rows; production uses
// message-level VirtItems via Screen.SetMessages and PaintLineAt at paint time.
func Compose(p ComposeParams) []string {
	if len(p.RawLines) == 0 {
		return nil
	}
	ctx := buildComposeCtx(p)
	out := make([]string, len(p.RawLines))
	for i, line := range p.RawLines {
		out[i] = composeOneRow(ctx, line)
	}
	return out
}

// NestedGutterX returns the list-local X of the nested pane scrollbar. The
// column after it is a blank separator before the main chat scrollbar.
func NestedGutterX(p ComposeParams, listW int) int {
	ctx := buildComposeCtx(p)
	if ctx.paneSBW <= 0 || ctx.rowInnerW < ui.ScrollbarWidth || listW <= 0 {
		return -1
	}
	footprint := min(ctx.rowInnerW, ctx.paneSBW+ctx.paneGapW)
	x := ctx.padW + ctx.rowInnerW - footprint
	return min(x, listW-ui.ScrollbarWidth)
}
