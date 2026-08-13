package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// SearchDebouncedMsg is emitted after a SearchField remains unchanged for its
// configured debounce interval. The owning screen translates it into a domain
// search request after checking that the field still belongs to that screen.
type SearchDebouncedMsg struct {
	Field      *SearchField
	Generation uint64
	Value      string
}

// SearchField is a single-line TextField that schedules a debounced change
// notification. It keeps text editing and debounce identity in one control;
// screens retain ownership of search execution and cancellation.
type SearchField struct {
	Base
	field      *TextField
	debounce   time.Duration
	generation uint64
}

// NewSearchField creates a debounced single-line search input.
func NewSearchField(id ComponentID, tokens *TokenSet) *SearchField {
	return &SearchField{
		Base:     NewBase(id),
		field:    NewTextField(id, tokens),
		debounce: 120 * time.Millisecond,
	}
}

// SetTokens updates the theme token set.
func (s *SearchField) SetTokens(tokens *TokenSet) { s.field.SetTokens(tokens) }

// SetPrompt configures the visual prompt.
func (s *SearchField) SetPrompt(prompt string) { s.field.SetPrompt(prompt) }

// SetPromptPlacement puts the prompt at the start or end of the field.
func (s *SearchField) SetPromptPlacement(p PromptPlacement) { s.field.SetPromptPlacement(p) }

// SetPlaceholder configures the empty-state hint.
func (s *SearchField) SetPlaceholder(placeholder string) { s.field.SetPlaceholder(placeholder) }

// SetSubmitAction sets the action emitted when Enter is pressed.
func (s *SearchField) SetSubmitAction(actionID string) { s.field.SetSubmitAction(actionID) }

// SetValue replaces the input value without scheduling a search.
func (s *SearchField) SetValue(value string) { s.field.SetValue(value) }

// Value returns the current query.
func (s *SearchField) Value() string { return s.field.Value() }

// Edit exposes the shared editing model for advanced callers and tests.
func (s *SearchField) Edit() *TextEdit { return s.field.Edit() }

// Tokens returns the resolved token set.
func (s *SearchField) Tokens() *TokenSet { return s.field.tokens }

// SetBackgroundColor overrides the field background fill.
func (s *SearchField) SetBackgroundColor(c cell.Color) { s.field.SetBackgroundColor(c) }

// ClearBackgroundColor removes the background fill override.
func (s *SearchField) ClearBackgroundColor() { s.field.ClearBackgroundColor() }

// SetPromptColor overrides the prompt foreground color.
func (s *SearchField) SetPromptColor(c cell.Color) { s.field.SetPromptColor(c) }

// ClearPromptColor restores the default prompt foreground.
func (s *SearchField) ClearPromptColor() { s.field.ClearPromptColor() }

// SetDebounce changes the delay before SearchDebouncedMsg is emitted.
func (s *SearchField) SetDebounce(delay time.Duration) { s.debounce = maxDuration(0, delay) }

// Schedule invalidates any earlier debounce and schedules a notification for
// the current value. Empty queries do not schedule a search.
func (s *SearchField) Schedule() tea.Cmd {
	s.generation++
	if s.Value() == "" {
		return nil
	}
	generation := s.generation
	value := s.Value()
	return tea.Tick(s.debounce, func(time.Time) tea.Msg {
		return SearchDebouncedMsg{Field: s, Generation: generation, Value: value}
	})
}

// Cancel invalidates any queued debounce notification.
func (s *SearchField) Cancel() { s.generation++ }

// AcceptsDebounce reports whether msg is the current delayed value for this
// field. Screens use this before making a domain request.
func (s *SearchField) AcceptsDebounce(msg SearchDebouncedMsg) bool {
	return msg.Field == s && msg.Generation == s.generation && msg.Value == s.Value() && s.Value() != ""
}

func (s *SearchField) Measure(ctx LayoutContext, cs Constraints) Size {
	return s.field.Measure(ctx, cs)
}

func (s *SearchField) Layout(ctx LayoutContext, r Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, Rect{W: r.W, H: r.H})
}

func (s *SearchField) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := s.Bounds()
	s.field.Paint(buf, originX+r.X, originY+r.Y, ctx)
}

func (s *SearchField) Handle(ctx EventContext, evt Event) EventResult {
	before := s.Value()
	result := s.field.Handle(ctx, evt)
	if s.Value() != before {
		result.Cmd = combineCmd(result.Cmd, s.Schedule())
	}
	return result
}

func (s *SearchField) Semantics() Semantics { return s.field.Semantics() }

func combineCmd(first, second tea.Cmd) tea.Cmd {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return tea.Batch(first, second)
}

func maxDuration(minimum, value time.Duration) time.Duration {
	if value < minimum {
		return minimum
	}
	return value
}
