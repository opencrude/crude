package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// FrameRole selects chrome behavior for WindowFrame.
type FrameRole uint8

const (
	// FrameDialog: modal chrome, content padding, optional footer.
	FrameDialog FrameRole = iota
	// FramePopover: non-modal chrome, content padding.
	FramePopover
	// FrameWindow: full frame with title/close; content fills body.
	FrameWindow
)

// WindowFrame paints a single unified chrome around child content.
// Phase 9 foundation: product surfaces migrate off PopupChrome / WM decoration
// / hand-drawn borders onto this frame.
type WindowFrame struct {
	Base
	role         FrameRole
	title        string
	icon         string
	tokens       *TokenSet
	content      Component
	closable     bool
	borderAccent cell.Color // optional accent override for border
	titleAccent  cell.Color // optional accent override for title
	// Content rect relative to frame bounds (after layout).
	contentR Rect
	// Close button hit region relative to frame bounds.
	closeR Rect
}

// NewWindowFrame creates a framed container.
func NewWindowFrame(id ComponentID, role FrameRole, tokens *TokenSet) *WindowFrame {
	return &WindowFrame{
		Base:     NewBase(id),
		role:     role,
		tokens:   tokens,
		closable: true,
	}
}

// SetTitle sets the title text.
func (f *WindowFrame) SetTitle(s string) { f.title = s }

// SetIcon sets an optional leading icon in the title bar.
func (f *WindowFrame) SetIcon(s string) { f.icon = s }

// SetContent sets the body component.
func (f *WindowFrame) SetContent(c Component) { f.content = c }

// Content returns the body component, or nil.
func (f *WindowFrame) Content() Component { return f.content }

// SetClosable toggles the close button.
func (f *WindowFrame) SetClosable(v bool) { f.closable = v }

// SetBorderAccent overrides the border color with an explicit accent.
// Used by diagnostic popups to tint the border by severity.
func (f *WindowFrame) SetBorderAccent(c cell.Color) { f.borderAccent = c }

// SetTitleAccent overrides the title color with an explicit accent.
func (f *WindowFrame) SetTitleAccent(c cell.Color) { f.titleAccent = c }

// SetTokens updates theme tokens.
func (f *WindowFrame) SetTokens(ts *TokenSet) { f.tokens = ts }

// ContentBounds returns the content rectangle in parent-local coords.
func (f *WindowFrame) ContentBounds() Rect { return f.contentR }

// CloseBounds returns the close-button rectangle in parent-local coords.
func (f *WindowFrame) CloseBounds() Rect { return f.closeR }

// Children implements Parent.
func (f *WindowFrame) Children() []Component {
	if f.content == nil {
		return nil
	}
	return []Component{f.content}
}

// Measure: content + chrome (title row + borders + optional pad).
func (f *WindowFrame) Measure(ctx LayoutContext, cs Constraints) Size {
	// Chrome: top border/title (1) + bottom border (1) + left/right borders (2)
	// Content pad: 2 left + 2 right for dialog/popover
	padX, padY := 0, 0
	if f.role == FrameDialog || f.role == FramePopover {
		padX, padY = 2, 0
	}
	innerCS := Constraints{
		MinW: max(0, cs.MinW-2-padX*2),
		MaxW: max(1, cs.MaxW-2-padX*2),
		MinH: max(0, cs.MinH-2-padY*2),
		MaxH: max(1, cs.MaxH-2-padY*2),
	}
	inner := Size{W: innerCS.MaxW, H: innerCS.MaxH}
	if f.content != nil {
		inner = f.content.Measure(ctx, innerCS)
	}
	return cs.ClampSize(Size{W: inner.W + 2 + padX*2, H: inner.H + 2 + padY*2})
}

// Layout assigns chrome and content rectangles.
func (f *WindowFrame) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	padX := 0
	if f.role == FrameDialog || f.role == FramePopover {
		padX = 2
	}
	// Title row is y=0 of the frame buffer; content starts at y=1.
	// Borders occupy x=0 and x=w-1, y=h-1 bottom.
	f.contentR = Rect{
		X: 1 + padX,
		Y: 1,
		W: max(0, r.W-2-padX*2),
		H: max(0, r.H-2),
	}
	if f.closable && r.W >= 3 {
		f.closeR = Rect{X: r.W - 3, Y: 0, W: 3, H: 1}
	} else {
		f.closeR = Rect{}
	}
	if f.content != nil {
		// Content bounds are parent-local so Host paint/hit use contentR offset.
		f.content.Layout(ctx, Rect{X: f.contentR.X, Y: f.contentR.Y, W: f.contentR.W, H: f.contentR.H})
	}
}

// Paint draws border, title, close, background, then content.
func (f *WindowFrame) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := f.Bounds()
	if r.Empty() {
		return
	}
	borderFG := cell.NoColor
	titleFG := cell.NoColor
	bg := cell.NoColor
	if f.tokens != nil {
		if ctx.Focused {
			borderFG = f.tokens.Color(ColorPopupBorderFocused)
			titleFG = f.tokens.Color(ColorPopupTitleFocused)
		} else {
			borderFG = f.tokens.Color(ColorPopupBorder)
			titleFG = f.tokens.Color(ColorPopupTitle)
		}
		bg = f.tokens.Color(ColorPopupBg)
	}
	// Apply severity accent overrides
	if !f.borderAccent.IsDefault() {
		borderFG = f.borderAccent
	}
	if !f.titleAccent.IsDefault() {
		titleFG = f.titleAccent
	}
	ox, oy := originX+r.X, originY+r.Y
	// Background fill
	if !bg.IsDefault() {
		FillRect(buf, 0, 0, Rect{X: ox, Y: oy, W: r.W, H: r.H}, cell.BlankCell(bg))
	}
	// Corners and edges (rounded)
	// top-left, top-right, bottom-left, bottom-right
	setRune := func(x, y int, ch rune) {
		buf.Set(ox+x, oy+y, cell.NewCell(ch, borderFG, bg, 0))
	}
	if r.W >= 2 && r.H >= 2 {
		setRune(0, 0, '╭')
		setRune(r.W-1, 0, '╮')
		setRune(0, r.H-1, '╰')
		setRune(r.W-1, r.H-1, '╯')
		for x := 1; x < r.W-1; x++ {
			setRune(x, 0, '─')
			setRune(x, r.H-1, '─')
		}
		for y := 1; y < r.H-1; y++ {
			setRune(0, y, '│')
			setRune(r.W-1, y, '│')
		}
	}
	// Title in top border
	title := f.title
	if f.icon != "" {
		title = f.icon + " " + title
	}
	if title != "" {
		// "─ Title ─" style: start after corner + space
		tx := 2
		avail := r.W - 4
		if f.closable {
			avail -= 3
		}
		if avail > 0 {
			runes := []rune(title)
			if rendering.VisibleWidth(title) > avail {
				// truncate by runes roughly
				if len(runes) > avail {
					runes = runes[:max(0, avail-1)]
					title = string(runes) + "…"
				}
			} else {
				title = string(runes)
			}
			buf.WriteString(ox+tx, oy, " "+title+" ", titleFG, bg, cell.AttrBold)
		}
	}
	// Close button
	if f.closable && !f.closeR.Empty() {
		cx := ox + f.closeR.X
		buf.WriteString(cx, oy, " ✕ ", titleFG, bg, 0)
	}
	// Content is painted by Host via Children(); frame only draws chrome.
	// When used without Host, callers should PaintTree(frame) or use ComponentWidget.
}

// Handle: close click emits action; otherwise forwards to content.
func (f *WindowFrame) Handle(ctx EventContext, msg tea.Msg) EventResult {
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
		if f.IsOnClose(ctx.LocalX, ctx.LocalY) {
			return EventResult{
				Handled:    true,
				Action:     &Action{ID: "window.close"},
				Invalidate: InvalidatePaint,
			}
		}
	}
	if f.content == nil {
		return EventResult{}
	}
	cctx := ctx
	cctx.LocalX = ctx.LocalX - f.contentR.X
	cctx.LocalY = ctx.LocalY - f.contentR.Y
	return f.content.Handle(cctx, msg)
}

// Semantics for the frame.
func (f *WindowFrame) Semantics() Semantics {
	return Semantics{
		Role:  RoleDialog,
		Label: f.title,
	}
}

// IsOnClose reports whether local (lx,ly) is on the close button.
func (f *WindowFrame) IsOnClose(lx, ly int) bool {
	return f.closable && f.closeR.Contains(lx, ly)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
