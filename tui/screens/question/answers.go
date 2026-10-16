package question

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/ui"
)

// This file owns the dedicated answer controls for the question deck: the
// stacked option rows, the write-in prompt area, and the focus gates that give
// each pane its Tab/Esc policy. Nothing here touches the shared form field
// types; the deck composes existing shared primitives (List, TextArea)
// instead.

const (
	// OptionsID is the focusable stacked answer list (one row per option plus
	// the "Write your response" row).
	OptionsID ui.ComponentID = "session-question/options"
	// InputID is the free-text write-in prompt area under the option rows.
	InputID ui.ComponentID = "session-question/input"
)

// Focus actions emitted by the gates. The screen resolves them so entering
// the action bar can also seed its first/last button.
const (
	focusForwardAction  = "question.focus.forward"
	focusBackwardAction = "question.focus.backward"
	railFocusAction     = "question.rail.focus"
)

// focusGate wraps a retained control and carries the pane's canonical focus
// ID. Arrow keys, Enter, and editing keys pass straight through to the child;
// Tab/Shift+Tab become forward/backward focus actions and Esc climbs the
// deck's escape ladder one level at a time (input → rows → rail → cancel), so
// leaving the write-in never denies the whole tool in a single keystroke.
type focusGate struct {
	ui.Base
	child      ui.Component
	next       ui.ComponentID
	prev       ui.ComponentID
	escAction  string // action emitted on Esc; "" focuses the rail
	escPayload any
}

func newFocusGate(id ui.ComponentID, child ui.Component, next, prev ui.ComponentID, escAction string, escPayload any) *focusGate {
	return &focusGate{
		Base:       ui.NewBase(id),
		child:      child,
		next:       next,
		prev:       prev,
		escAction:  escAction,
		escPayload: escPayload,
	}
}

func (g *focusGate) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return g.child.Measure(ctx, cs)
}

// Layout mirrors the gate rect onto the child in parent-local space; the
// child keeps a zero-origin rect so delegated paint/hit math stays aligned.
func (g *focusGate) Layout(ctx ui.LayoutContext, r ui.Rect) {
	g.SetBounds(r)
	g.child.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: r.H})
}

func (g *focusGate) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	// Framework contract: Paint receives the PARENT's origin; this component
	// adds its own bounds (exactly like List/TextField/railDivider do) before
	// delegating to the wrapped control, whose own bounds sit at zero.
	r := g.Bounds()
	g.child.Paint(buf, originX+r.X, originY+r.Y, ctx)
}

func (g *focusGate) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyTab:
			if evt.Key.Mod.Contains(tea.ModShift) {
				return focusTargetResult(g.prev, false)
			}
			return focusTargetResult(g.next, true)
		case tea.KeyEsc:
			action := g.escAction
			if action == "" {
				action = railFocusAction
			}
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: action, Payload: g.escPayload}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return g.child.Handle(ctx, evt)
}

func (g *focusGate) Semantics() ui.Semantics { return g.child.Semantics() }

// TextEditor exposes the wrapped control's editor so a context menu inside the
// gate's pane can copy or cut it (TextEditorProvider). The gate owns the pane's
// rect; the child keeps a zero-origin rect, so the editor spans the whole gate.
func (g *focusGate) TextEditor() ui.TextClipboardTarget {
	return ui.TextClipboardTargetIn(g.child)
}

var _ ui.Component = (*focusGate)(nil)

// answerInput wraps the shared prompt-area (TextArea) with change detection
// so typing in the write-in editor updates answer state without polling. It
// is deliberately the same control as the session chat composer: multi-line,
// Enter inserts a newline, Ctrl+Enter submits, and it carries no raised form
// background; the deck paints flat ColorBg everywhere.
type answerInput struct {
	*ui.TextArea
	onChange func(value string)
}

func (a *answerInput) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	before := a.Value()
	result := a.TextArea.Handle(ctx, evt)
	if a.Value() != before {
		if a.onChange != nil {
			a.onChange(a.Value())
		}
		// Growing/shrinking rows must re-run deck layout, not just repaint.
		result.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	return result
}

var _ ui.Component = (*answerInput)(nil)

// optionItems builds the stacked answer rows: one line per option followed by
// a divider rule and the write-in row. Choice state rides the icon column
// (single ○/●, multiple [ ]/[x]) while the shared list row colors carry the
// highlight, never a prompt glyph.
func optionItems(item Item, choices []string) []ui.ListItem {
	if len(item.Options) == 0 {
		return nil
	}
	chosen := make(map[string]bool, len(choices))
	for _, id := range choices {
		chosen[id] = true
	}
	items := make([]ui.ListItem, 0, len(item.Options)+2)
	for index, option := range item.Options {
		id := fmt.Sprintf("option:%d", index)
		items = append(items, ui.ListItem{
			ID:      id,
			Label:   option,
			Icon:    choiceGlyph(item.Multiple, chosen[id]),
			Payload: id,
		})
	}
	items = append(items,
		ui.ListItem{ID: "custom-sep", Separator: true},
		ui.ListItem{ID: customOptionID, Label: "Write your response", Icon: choiceGlyph(item.Multiple, chosen[customOptionID]), Payload: customOptionID},
	)
	return items
}

func choiceGlyph(multiple, chosen bool) string {
	switch {
	case multiple && chosen:
		return "[x]"
	case multiple:
		return "[ ]"
	case chosen:
		return "●"
	default:
		return "○"
	}
}

// focusTargetResult asks the screen to move deck focus. Direction matters only
// when the target is the action bar (first vs last button seeding).
func focusTargetResult(target ui.ComponentID, forward bool) ui.EventResult {
	id := focusBackwardAction
	if forward {
		id = focusForwardAction
	}
	return ui.EventResult{
		Handled:    true,
		Action:     &ui.Action{ID: id, Payload: target},
		Invalidate: ui.InvalidateFocus | ui.InvalidatePaint,
	}
}
