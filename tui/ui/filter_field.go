package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// FilterField is a single-line TextField specialized for filtering a
// collection. Editing runs through the shared TextEdit engine, and every edit
// (typing, paste, deletion, Esc-to-clear) invokes the OnChanged callback
// immediately so the owning collection can re-apply its filter without a
// debounce.
//
// It differs from SearchField in timing: SearchField debounces change
// notifications for async searches (launcher, Find in Files), while
// FilterField notifies synchronously for in-memory collection filters
// (file tree, file picker, session picker, settings categories).
type FilterField struct {
	Base
	field    *TextField
	onChange func(string)
}

// NewFilterField creates a single-line filter input.
func NewFilterField(id ComponentID, tokens *TokenSet) *FilterField {
	return &FilterField{
		Base:  NewBase(id),
		field: NewTextField(id, tokens),
	}
}

// OnChanged registers the callback invoked whenever the value changes through
// editing or Esc-to-clear. Programmatic value replacement via SetValue does
// not notify — screens restore model state with SetValue and must not re-enter
// the filter path.
func (f *FilterField) OnChanged(fn func(string)) { f.onChange = fn }

// SetTokens updates the theme token set.
func (f *FilterField) SetTokens(tokens *TokenSet) { f.field.SetTokens(tokens) }

// SetPrompt configures the visual prompt.
func (f *FilterField) SetPrompt(prompt string) { f.field.SetPrompt(prompt) }

// SetPromptPlacement puts the prompt at the start or end of the field.
func (f *FilterField) SetPromptPlacement(p PromptPlacement) { f.field.SetPromptPlacement(p) }

// SetPlaceholder configures the empty-state hint.
func (f *FilterField) SetPlaceholder(placeholder string) { f.field.SetPlaceholder(placeholder) }

// SetSubmitAction sets the action emitted when Enter is pressed.
func (f *FilterField) SetSubmitAction(actionID string) { f.field.SetSubmitAction(actionID) }

// SetValue replaces the input value without notifying OnChanged.
func (f *FilterField) SetValue(value string) { f.field.SetValue(value) }

// Value returns the current query.
func (f *FilterField) Value() string { return f.field.Value() }

// Edit exposes the shared editing model for advanced callers and tests.
func (f *FilterField) Edit() *TextEdit { return f.field.Edit() }

// Tokens returns the resolved token set.
func (f *FilterField) Tokens() *TokenSet { return f.field.tokens }

// SetBackgroundColor overrides the field background fill.
func (f *FilterField) SetBackgroundColor(c cell.Color) { f.field.SetBackgroundColor(c) }

// ClearBackgroundColor removes the background fill override.
func (f *FilterField) ClearBackgroundColor() { f.field.ClearBackgroundColor() }

// SetPromptColor overrides the prompt foreground color.
func (f *FilterField) SetPromptColor(c cell.Color) { f.field.SetPromptColor(c) }

// ClearPromptColor restores the default prompt foreground.
func (f *FilterField) ClearPromptColor() { f.field.ClearPromptColor() }

// Measure: one row, full available width.
func (f *FilterField) Measure(ctx LayoutContext, cs Constraints) Size {
	return f.field.Measure(ctx, cs)
}

func (f *FilterField) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	f.field.Layout(ctx, Rect{W: r.W, H: r.H})
}

func (f *FilterField) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := f.Bounds()
	f.field.Paint(buf, originX+r.X, originY+r.Y, ctx)
}

// Handle routes editing through the shared engine and notifies OnChanged on
// value change. Esc with a non-empty value clears the filter (and notifies);
// Esc with an empty value passes through so the owner can close or cancel.
func (f *FilterField) Handle(ctx EventContext, evt Event) EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		if f.field.Edit().Len() > 0 {
			f.field.SetValue("")
			if f.onChange != nil {
				f.onChange("")
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint | InvalidateLayout}
		}
		return EventResult{}
	}
	before := f.Value()
	result := f.field.Handle(ctx, evt)
	if f.Value() != before {
		if f.onChange != nil {
			f.onChange(f.Value())
		}
		result.Invalidate |= InvalidateLayout | InvalidatePaint
	}
	return result
}

func (f *FilterField) Semantics() Semantics { return f.field.Semantics() }
