package gitpanel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
)

// messageBox hosts the commit draft so every edit re-evaluates the Commit
// button, and Esc returns to the change list instead of closing the dock.
type messageBox struct {
	ui.Base
	area  *ui.TextArea
	owner *Panel
}

func (box *messageBox) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	box.SetBounds(bounds)
	box.area.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (box *messageBox) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	box.area.Paint(buf, originX+box.Bounds().X, originY+box.Bounds().Y, ctx)
}

func (box *messageBox) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.String() {
		case "tab", "shift+tab":
			return ui.EventResult{}
		case "esc":
			if box.owner.writing {
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionStopGenerate}}
			}
			if box.area.Value() == "" {
				box.owner.focusID(ListID)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
			}
		}
	}
	result := box.area.Handle(ctx, evt)
	box.owner.syncCommit()
	return result
}

func (box *messageBox) TextEditor() ui.TextClipboardTarget { return box.area }
func (box *messageBox) CancelPointerCapture()              { box.area.CancelPointerCapture() }
func (box *messageBox) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Commit message", Value: box.area.Value(), Description: ui.FormatShortcut(ui.CommitChord) + " commits"}
}

// selectList wraps a ui.List so keyboard movement can request a preview.
type selectList struct {
	list   *ui.List
	onMove func() *ui.Action
}

func (box *selectList) ID() ui.ComponentID                     { return box.list.ID() }
func (box *selectList) Bounds() ui.Rect                        { return box.list.Bounds() }
func (box *selectList) SetBounds(r ui.Rect)                    { box.list.SetBounds(r) }
func (box *selectList) Layout(ctx ui.LayoutContext, r ui.Rect) { box.list.Layout(ctx, r) }
func (box *selectList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return box.list.Measure(ctx, cs)
}
func (box *selectList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	box.list.Paint(buf, originX, originY, ctx)
}
func (box *selectList) Semantics() ui.Semantics { return box.list.Semantics() }

func (box *selectList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	before := box.list.SelectedIndex()
	result := box.list.Handle(ctx, evt)
	if box.onMove != nil && box.list.SelectedIndex() != before {
		if action := box.onMove(); action != nil {
			if evt.IsKey() && evt.Key.Code == tea.KeyEnter {
				return result
			}
			if evt.Kind == ui.EventMouseClick {
				// A click both selects (preview) and activates on a second click.
				result.Action = action
				return result
			}
			result.Action = action
		}
	}
	return result
}
