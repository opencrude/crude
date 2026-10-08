package gitreview

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
)

// findBar searches every shown line of the review; typing re-runs the search,
// Enter/Shift+Enter step through matches, Esc closes and keeps the position.
type findBar struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func newFindBar(owner *Screen) *findBar {
	field := ui.NewFilterField(owner.FindID(), owner.tokens)
	field.SetPlaceholder("Find in changes")
	field.SetPrompt(ui.SearchGlyphEnd(owner.tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetSubmitAction("")
	bar := &findBar{Base: ui.NewBase(owner.FindID()), field: field, owner: owner}
	field.OnChanged(func(query string) { owner.document.SetQuery(query) })
	return bar
}

func (bar *findBar) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	bar.SetBounds(bounds)
	bar.field.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (bar *findBar) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bar.field.Paint(buf, originX+bar.Bounds().X, originY+bar.Bounds().Y, ctx)
}

func (bar *findBar) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.String() {
		case "enter", "f3":
			bar.owner.document.NextMatch(false)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case "shift+enter", "shift+f3":
			bar.owner.document.NextMatch(true)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case "esc":
			bar.owner.closeFind()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
		case "tab", "down":
			bar.owner.focusID(bar.owner.DocumentID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if evt.Key.Code == tea.KeyEnter {
			return ui.EventResult{Handled: true}
		}
	}
	return bar.field.Handle(ctx, evt)
}

func (bar *findBar) TextEditor() ui.TextClipboardTarget { return bar.field.TextEditor() }
func (bar *findBar) Semantics() ui.Semantics            { return bar.field.Semantics() }

func (screen *Screen) focusID(id ui.ComponentID) {
	if screen.focus != nil {
		screen.focus(id)
	}
}

func (screen *Screen) openFind() {
	screen.findOpen = true
	if query := screen.find.field.Value(); query != "" {
		screen.document.SetQuery(query)
	}
}

func (screen *Screen) closeFind() {
	screen.findOpen = false
	screen.document.SetQuery("")
	screen.focusID(screen.DocumentID())
}

// FindOpen reports whether the find bar is shown.
func (screen *Screen) FindOpen() bool { return screen.findOpen }
