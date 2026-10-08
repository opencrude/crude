package gitreview

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	"crude/tui/surface"
	"crude/tui/ui"
)

// fileNavigator lists the review's files and highlights the one being read.
// Selecting a file scrolls the document; scrolling the document moves the
// highlight, so the two can never disagree about the current file.
type fileNavigator struct {
	ui.Base
	owner   *Screen
	files   []gitdiff.File
	scroll  *ui.ScrollModel
	cursor  int
	hover   int
	hovered bool
	focused bool
}

func newFileNavigator(owner *Screen) *fileNavigator {
	return &fileNavigator{Base: ui.NewBase(owner.NavigatorID()), owner: owner, scroll: ui.NewScrollModel(), cursor: -1, hover: -1}
}

func (navigator *fileNavigator) setTokens(*ui.TokenSet) {}

func (navigator *fileNavigator) setFiles(files []gitdiff.File) {
	navigator.files = files
	navigator.scroll.SetContent(len(files))
	navigator.scroll.Clamp()
	navigator.cursor = min(navigator.cursor, len(files)-1)
}

func (navigator *fileNavigator) Layout(_ ui.LayoutContext, bounds ui.Rect) {
	navigator.SetBounds(bounds)
	navigator.scroll.SetViewport(bounds.H)
	navigator.scroll.SetContent(len(navigator.files))
}

// current is the document's reading position, or the keyboard cursor while
// the navigator itself has focus.
func (navigator *fileNavigator) current() int {
	if navigator.focused && navigator.cursor >= 0 {
		return navigator.cursor
	}
	file, ok := navigator.owner.document.CurrentFile()
	if !ok {
		return -1
	}
	for index := range navigator.files {
		if navigator.files[index].Key == file.Key {
			return index
		}
	}
	return -1
}

func (navigator *fileNavigator) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bounds := navigator.Bounds()
	tokens := navigator.owner.tokens
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	navigator.hovered, navigator.focused = ctx.Hovered, ctx.Focused
	current := navigator.current()
	if current >= 0 && !ctx.Focused {
		navigator.scroll.EnsureVisible(current)
	}
	x, y := originX+bounds.X, originY+bounds.Y
	for local := 0; local < bounds.H; local++ {
		index := navigator.scroll.Offset() + local
		if index >= len(navigator.files) {
			break
		}
		file := &navigator.files[index]
		fill, ink, muted := bg, tokens.Color(ui.ColorFg), tokens.Color(ui.ColorMuted)
		switch {
		case index == current && ctx.Focused:
			fill = tokens.Color(ui.ColorSelection)
			ink = cell.ContrastColor(fill)
			muted = ink
		case index == current:
			fill = tokens.Color(ui.ColorSubPaneBg)
		case navigator.hovered && index == navigator.hover:
			fill = tokens.Color(ui.ColorHover)
		}
		buf.FillRect(surface.Rect{X: x, Y: y + local, W: bounds.W, H: 1}, cell.BlankCell(fill))
		code := file.Code()
		buf.WriteString(x+1, y+local, code, tokens.Color(gitdiff.CodeRole(code, file.Change.Area)), fill, cell.AttrBold)
		stats := ""
		if file.Note == "" {
			stats = fmt.Sprintf("+%d −%d", file.Added, file.Removed)
		}
		statsW := rendering.VisibleWidth(stats)
		limit := bounds.W - statsW - 2
		name, dir := gitdiff.SplitPath(file.Path())
		col := 3
		nameText := rendering.TruncateVisual(name, max(0, limit-col), "…")
		attr := cell.Attrs(0)
		if index == current {
			attr = cell.AttrBold
		}
		buf.WriteString(x+col, y+local, nameText, ink, fill, attr)
		col += rendering.VisibleWidth(nameText) + 1
		if dir != "" && col < limit {
			buf.WriteString(x+col, y+local, rendering.TruncateVisual(dir, limit-col, "…"), muted, fill, 0)
		}
		if stats != "" && limit > 3 {
			buf.WriteString(x+bounds.W-statsW-1, y+local, stats, muted, fill, 0)
		}
	}
}

func (navigator *fileNavigator) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	paint := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	if evt.IsKey() {
		if navigator.cursor < 0 {
			navigator.cursor = max(0, navigator.current())
		}
		switch evt.Key.String() {
		case "up", "k":
			navigator.cursor = max(0, navigator.cursor-1)
		case "down", "j":
			navigator.cursor = min(len(navigator.files)-1, navigator.cursor+1)
		case "home":
			navigator.cursor = 0
		case "end":
			navigator.cursor = len(navigator.files) - 1
		case "enter", "right":
			if navigator.cursor >= 0 && navigator.cursor < len(navigator.files) {
				key := navigator.files[navigator.cursor].Key
				navigator.cursor = -1
				paint.Action = &ui.Action{ID: actionNavigate, Payload: key}
			}
			return paint
		default:
			return ui.EventResult{}
		}
		navigator.scroll.EnsureVisible(navigator.cursor)
		if navigator.cursor >= 0 && navigator.cursor < len(navigator.files) {
			navigator.owner.document.RevealFile(navigator.files[navigator.cursor].Key)
		}
		return paint
	}
	switch evt.Kind {
	case ui.EventMouseMotion:
		index := navigator.scroll.Offset() + ctx.LocalY
		if index != navigator.hover {
			navigator.hover = index
			return paint
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			navigator.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			navigator.scroll.ScrollBy(3)
		}
		return paint
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		index := navigator.scroll.Offset() + ctx.LocalY
		if index >= 0 && index < len(navigator.files) {
			navigator.cursor = -1
			paint.Action = &ui.Action{ID: actionNavigate, Payload: navigator.files[index].Key}
		}
		return paint
	}
	return ui.EventResult{}
}

func (navigator *fileNavigator) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleList, Label: "Changed files", Description: "Select a file to scroll the review to it"}
}
