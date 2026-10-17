// Package question provides the retained session question screen.
package question

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID      ui.ComponentID = "session-question"
	ActionsID     ui.ComponentID = "session-question/actions"
	RailID        ui.ComponentID = "session-question/rail"
	RailScrollID  ui.ComponentID = "session-question/rail-scroll"
	RailDividerID ui.ComponentID = "session-question/rail-divider"

	customOptionID = "custom"
)

// Item is the presentation-neutral question payload supplied by the blocking
// tool event. Options are immutable screen input; answers remain retained UI
// state until the screen emits a result.
type Item struct {
	Question string   `json:"question"`
	Header   string   `json:"header,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

type Result struct {
	Answers   map[string]string
	Questions map[string]string
}

type ResultMsg struct {
	Target *Screen
	Result Result
}

type CancelMsg struct{ Target *Screen }

// Screen composes the dedicated answer stack, write-in field, question rail,
// and ActionBar for a blocking agent request. The right pane never renders a
// dropdown: every option is its own stacked row followed by a "Write your
// response" line, selectable by keyboard (arrows + Enter/Space) or mouse.
// A question rail mirrors the settings deck; the active row is indicated by
// background+text color only (no marker glyphs). It has no workspace
// dependencies.
type Screen struct {
	ui.Base
	tokens  *ui.TokenSet
	items   []Item
	answers []string
	choices [][]string
	custom  []string
	index   int
	errMsg  string

	railRows    *ui.List
	options     *ui.List
	input       *answerInput
	actions     *ui.ActionBar
	railGate    *focusGate
	optionsGate *focusGate
	inputGate   *focusGate
	railScroll  *ui.ScrollbarControl
	divider     *railDivider

	// Cached right-pane geometry from the latest Layout so Paint can place
	// the header rule, error line, and truncations without recomputation.
	paneX      int
	paneW      int
	paneIndent int
	headerH    int
	optH       int
	inputH     int

	ruleFullBleed bool
	ruleBleedX    int
	ruleBleedW    int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{
		Base:          ui.NewBase(ScreenID),
		tokens:        ui.NewTokenSet(th),
		ruleFullBleed: true,
	}
	s.railRows = ui.NewList(ui.NewID(RailID, "rows"), s.tokens)
	s.railRows.SetActivateAction("question.rail.select")
	s.railRows.SetDefaultRowBackground(ui.ColorBg)

	s.options = ui.NewList(ui.NewID(OptionsID, "rows"), s.tokens)
	s.options.SetActivateAction("question.option.activate")
	s.options.SetDefaultRowBackground(ui.ColorBg)

	field := ui.NewTextArea(InputID, s.tokens, true)
	field.SetSubmitAction("question.next")
	field.SetPlaceholder("Type your answer")
	// Prompt-area chrome rule (mirrors the session chat composer): the
	// shared InputColors default is a raised popup fill; decks stay flat.
	field.SetBackgroundColor(s.tokens.Color(ui.ColorBg))
	s.input = &answerInput{TextArea: field}
	s.input.onChange = func(value string) { s.onInputChanged(value) }

	// Focus cycle: rail ⇄ options ⇄ input ⇄ actions. Esc climbs one level at
	// a time: the write-in hands back to the rows, the rows hand back to the
	// rail, and only Esc on the rail cancels the deck.
	s.railGate = newFocusGate(RailID, s.railRows, OptionsID, ActionsID, "question.cancel", nil)
	s.optionsGate = newFocusGate(OptionsID, s.options, InputID, RailID, railFocusAction, RailID)
	s.inputGate = newFocusGate(InputID, s.input, ActionsID, OptionsID, focusBackwardAction, OptionsID)

	s.railScroll = ui.NewScrollbarControl(RailScrollID, s.railRows.Scroll(), s.tokens)
	s.divider = &railDivider{Base: ui.NewBase(RailDividerID), tokens: s.tokens}
	s.openCurrent()
	return s
}

// Reset replaces the retained request data and returns the parsed question count.
func (s *Screen) Reset(questionsJSON string) int {
	count, _ := s.ResetWithError(questionsJSON)
	return count
}

// ResetWithError retains the parser failure so the caller can report an
// actionable validation outcome to the model instead of claiming user cancel.
func (s *Screen) ResetWithError(questionsJSON string) (int, error) {
	var items []Item
	if strings.TrimSpace(questionsJSON) == "" {
		s.Dismiss()
		return 0, fmt.Errorf("questions payload is empty")
	}
	if err := json.Unmarshal([]byte(questionsJSON), &items); err != nil {
		s.Dismiss()
		return 0, fmt.Errorf("questions payload is not valid JSON: %w", err)
	}
	if len(items) == 0 {
		s.Dismiss()
		return 0, fmt.Errorf("questions payload contains no questions")
	}
	s.items = items
	s.answers = make([]string, len(items))
	s.choices = make([][]string, len(items))
	s.custom = make([]string, len(items))
	s.index = 0
	s.syncRail()
	s.openCurrent()
	return len(items), nil
}

func (s *Screen) Dismiss() {
	s.items = nil
	s.answers = nil
	s.choices = nil
	s.custom = nil
	s.index = 0
	s.syncRail()
	s.openCurrent()
}

func (s *Screen) Count() int { return len(s.items) }

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
	s.railRows.SetTokens(tokens)
	s.options.SetTokens(tokens)
	if s.input != nil {
		s.input.SetTokens(tokens)
		// Re-assert the flat deck chrome after the token swap, exactly like
		// the session chat composer's syncShellChrome does.
		s.input.SetBackgroundColor(tokens.Color(ui.ColorBg))
	}
	s.railScroll.SetTokens(tokens)
	s.divider.tokens = tokens
}

// SetRuleFullBleed toggles whether the header rule may span the deck row
// (docked panels do; the parent publishes the row geometry via SetRuleBleed).
func (s *Screen) SetRuleFullBleed(v bool) { s.ruleFullBleed = v }

// SetRuleBleed records the deck row geometry supplied by the parent deck at
// layout time (mirrors Form/ActionBar).
func (s *Screen) SetRuleBleed(offsetX, width int) { s.ruleBleedX, s.ruleBleedW = offsetX, width }

func (s *Screen) ruleSpan(contentW int) (x, w int) {
	if s.ruleFullBleed && s.ruleBleedW-s.ruleBleedX > contentW {
		return -s.ruleBleedX, s.ruleBleedW
	}
	return 0, contentW
}

// syncRail rebuilds the question rail rows. Each question paints as
// "○ N. Title" (unanswered) or "✓ N. Title" (answered); divider rules
// separate entries; the active question is indicated by background and text
// color only (the shared List selection row), never a marker glyph.
func (s *Screen) syncRail() {
	if s.railRows == nil {
		return
	}
	items := make([]ui.ListItem, 0, max(0, s.Count()*2-1))
	for i, q := range s.items {
		label := q.Header
		if label == "" {
			label = q.Question
		}
		if rendering.VisibleWidth(label) > 30 {
			label = rendering.TruncateVisual(label, 29, "…")
		}
		icon := "○"
		if i < len(s.answers) && strings.TrimSpace(s.answers[i]) != "" {
			icon = "✓"
		}
		items = append(items, ui.ListItem{
			ID:      fmt.Sprintf("rail/%d", i),
			Label:   fmt.Sprintf("%d. %s", i+1, label),
			Icon:    icon,
			Payload: i,
		})
		if i < s.Count()-1 {
			items = append(items, ui.ListItem{ID: fmt.Sprintf("rail/sep/%d", i), Separator: true})
		}
	}
	s.railRows.SetItems(items)
	for index, item := range items {
		if payload, ok := item.Payload.(int); ok && payload == s.index {
			s.railRows.SelectIndex(index)
			break
		}
	}
}

// railWidth sizes the question rail like the old deck's sidebar: at most 25%
// of the width (capped at 35), never below 8, and never starving the form
// below the readable minimum.
func (s *Screen) railWidth(total int) int {
	if total <= 0 {
		return 12
	}
	maxItemW := 0
	for i, q := range s.items {
		label := q.Header
		if label == "" {
			label = q.Question
		}
		if rendering.VisibleWidth(label) > 30 {
			label = rendering.TruncateVisual(label, 29, "…")
		}
		title := fmt.Sprintf("%d. %s", i+1, label)
		w := rendering.VisibleWidth(title) + 4 // icon + indent spacing
		if w > maxItemW {
			maxItemW = w
		}
	}
	ideal := maxItemW + 1
	maxW := total * 25 / 100
	if maxW > 35 {
		maxW = 35
	}
	if maxW < 8 {
		maxW = 8
	}
	const minContent = 24
	if fromContent := total - minContent - 1; fromContent > 8 && fromContent < maxW {
		maxW = fromContent
	}
	if ideal < 8 {
		ideal = 8
	}
	if ideal > maxW {
		ideal = maxW
	}
	return ideal
}

func (s *Screen) Current() int { return s.index }

func (s *Screen) Answer(index int) string {
	if index < 0 || index >= len(s.answers) {
		return ""
	}
	return s.answers[index]
}

func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.railGate, s.optionsGate, s.inputGate, s.actions}
	if s.railScroll != nil && s.railScroll.Needed() {
		children = append(children, s.railScroll)
	}
	// The pane divider paints last so form rules and rail fills never mask it.
	children = append(children, s.divider)
	return children
}

// headerHeight is the right-pane chrome above the answer stack: title, optional
// question detail, and the separator rule (mirrors the docked-form header).
func (s *Screen) headerHeight() int {
	if s.Count() == 0 {
		return 0
	}
	item, ok := s.currentItem()
	if !ok || strings.TrimSpace(item.Question) == "" {
		return 2
	}
	return 3
}

func (s *Screen) optionRowCount() int {
	item, ok := s.currentItem()
	if !ok || s.index < 0 || s.index >= len(s.choices) {
		return 0
	}
	return len(optionItems(item, s.choices[s.index]))
}

func (s *Screen) errHeight() int {
	if s.errMsg == "" {
		return 0
	}
	return 1
}

func (s *Screen) inputIndent(paneW int) int {
	indent := 0
	if s.tokens != nil {
		indent = max(0, s.tokens.FormIndent())
	}
	return min(indent, max(0, paneW-8))
}

// inputNaturalH reports the prompt-area's wrap-aware row count at the given
// width, clamped to at least one row and at most maxH (Constraints clamp
// against MaxH, so a zero bound would collapse the measure to zero).
func (s *Screen) inputNaturalH(w, maxH int) int {
	if s.input == nil || w < 1 {
		return 1
	}
	h := s.input.Measure(ui.LayoutContext{}, ui.Constraints{MaxW: w, MaxH: max(1, maxH)}).H
	return max(1, h)
}

// preferredInputH sizes the write-in for deck-height math. Before the first
// layout there is no real width to wrap against, so it claims one row and
// converges on the next pass once paneW is known.
func (s *Screen) preferredInputH(maxHeight int) int {
	if s.paneW <= 0 || maxHeight <= 0 {
		return 1
	}
	return s.inputNaturalH(max(1, s.paneW-s.paneIndent), maxHeight)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: s.PreferredHeight(cs.MaxH)})
}

func (s *Screen) PreferredHeight(maxHeight int) int {
	if s.Count() == 0 {
		return 0
	}
	chrome := 2
	if s.actions != nil {
		chrome = s.actions.PreferredHeight()
	}
	body := s.headerHeight() + s.optionRowCount() + s.preferredInputH(maxHeight) + s.errHeight()
	return min(maxHeight, min(body, max(0, maxHeight-chrome))+chrome)
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := 2
	if s.actions != nil {
		chrome = s.actions.PreferredHeight()
	}
	bodyH := max(1, r.H-chrome)
	railW := s.railWidth(r.W)
	scroll := s.railScroll != nil && s.railScroll.NeededForViewport(bodyH)
	railContentW := railW
	if scroll {
		railContentW = max(1, railW-1)
	}
	s.railGate.Layout(ctx, ui.Rect{X: 0, Y: 0, W: railContentW, H: bodyH})
	if scroll {
		s.railScroll.Layout(ctx, ui.Rect{X: max(0, railW-1), Y: 0, W: 1, H: bodyH})
	} else if s.railScroll != nil {
		s.railScroll.Layout(ctx, ui.Rect{})
	}

	s.headerH = s.headerHeight()
	s.paneX = railW + 2
	s.paneW = max(1, r.W-s.paneX)
	s.paneIndent = s.inputIndent(s.paneW)
	errH := s.errHeight()
	inputW := max(1, s.paneW-s.paneIndent)
	avail := max(1, bodyH-s.headerH-errH)
	s.optH = 0
	if rows := s.optionRowCount(); rows > 0 {
		s.optH = min(rows, avail)
	}
	// The write-in is a prompt area: it grows with its content but never
	// pushes the option stack or the action bar out of the pane; overflow
	// scrolls inside the control instead.
	room := max(1, avail-s.optH)
	s.inputH = min(s.inputNaturalH(inputW, room), room)
	s.optionsGate.Layout(ctx, ui.Rect{X: s.paneX, Y: s.headerH, W: s.paneW, H: s.optH})
	s.inputGate.Layout(ctx, ui.Rect{
		X: s.paneX + s.paneIndent,
		Y: s.headerH + s.optH,
		W: inputW,
		H: s.inputH,
	})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-chrome), W: r.W, H: chrome})
	s.divider.Layout(ctx, ui.Rect{X: railW, Y: 0, W: 1, H: bodyH})
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg := cell.NoColor
	fg, muted, divider, errorFG := cell.NoColor, cell.NoColor, cell.NoColor, cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
		fg = s.tokens.Color(ui.ColorFg)
		muted = s.tokens.Color(ui.ColorMuted)
		divider = s.tokens.Color(ui.ColorPopupDivider)
		errorFG = s.tokens.Color(ui.ColorError)
	}
	ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	if s.Count() == 0 {
		return
	}
	x, y := originX+r.X, originY+r.Y
	title, detail := s.currentTitleDetail()
	buf.WriteString(x+s.paneX, y, rendering.TruncateVisual(title, s.paneW, ""), fg, bg, cell.AttrBold)
	if s.headerH == 3 {
		buf.WriteString(x+s.paneX, y+1, rendering.TruncateVisual(detail, s.paneW, ""), muted, bg, 0)
	}
	// Header rule spans the deck row edge to edge when the parent published
	// bleed geometry (paint-last ordering keeps it visible over fills).
	ruleX, ruleW := s.ruleSpan(s.paneW)
	startX := max(x, x+s.paneX+ruleX)
	endX := min(x+r.W, x+s.paneX+ruleX+ruleW)
	if endX > startX {
		glyph := ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '─')
		buf.WriteString(startX, y+s.headerH-1, strings.Repeat(string(glyph), endX-startX), divider, bg, 0)
	}
	if s.errMsg != "" {
		errX := x + s.paneX + s.paneIndent
		errY := y + s.headerH + s.optH + s.inputH
		buf.WriteString(errX, errY, rendering.TruncateVisual(s.errMsg, max(1, s.paneW-s.paneIndent), "…"), errorFG, bg, 0)
	}
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	// Keys reaching the screen directly (screen-focused or unhandled past the
	// gates): Tab enters the answer pane, Shift+Tab wraps to the actions,
	// Esc lands on the rail instead of cancelling.
	switch evt.Key.Code {
	case tea.KeyTab:
		if evt.Key.Mod.Contains(tea.ModShift) {
			return s.focusResult(ActionsID, false)
		}
		return s.focusResult(s.entryFocusID(), true)
	case tea.KeyEsc:
		return s.focusResult(RailID, false)
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case focusForwardAction, focusBackwardAction:
		target, _ := action.Payload.(ui.ComponentID)
		return s.focusResult(s.visibleTarget(target), action.ID == focusForwardAction)
	case railFocusAction:
		return s.focusResult(RailID, false)
	case "question.rail.select":
		item, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{}
		}
		index, ok := item.Payload.(int)
		if !ok || index < 0 || index >= len(s.items) || index == s.index {
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		s.index = index
		s.syncRail()
		s.openCurrent()
		result := s.focusResult(s.entryFocusID(), true)
		result.Invalidate |= ui.InvalidateLayout
		return result
	case "question.option.activate":
		return s.handleOptionActivate(action)
	case "question.next":
		return s.advance()
	case "question.previous":
		if s.index > 0 {
			s.index--
			s.syncRail()
			s.openCurrent()
		}
		result := s.focusResult(s.entryFocusID(), true)
		result.Invalidate |= ui.InvalidateLayout
		return result
	case "question.cancel":
		return s.cancel()
	case "actionbar.exit.backward":
		return s.focusResult(RailID, false)
	case "actionbar.exit.forward":
		return s.focusResult(s.entryFocusID(), true)
	}
	return ui.EventResult{Action: &action}
}

// handleOptionActivate applies a stacked-row activation shared by Enter/Space
// and mouse clicks. Single-choice rows record the answer and advance;
// multi-choice rows toggle in place; the write-in row selects the custom
// answer and focuses the text line beneath the stack.
func (s *Screen) handleOptionActivate(action ui.Action) ui.EventResult {
	item, ok := s.currentItem()
	row, rowOK := action.Payload.(ui.ListItem)
	if !ok || !rowOK {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	id, _ := row.Payload.(string)
	if id == customOptionID {
		s.selectCustom()
		result := s.focusResult(InputID, true)
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		return result
	}
	index := optionIndex(id)
	if index < 0 || index >= len(item.Options) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if item.Multiple {
		s.toggleChoice(id)
		s.syncAnswer()
		s.syncOptions()
		s.refreshError()
		s.syncRail()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.choices[s.index] = []string{id}
	s.syncAnswer()
	s.syncRail()
	return s.advance()
}

// selectCustom marks the write-in row as the active answer. Single-choice
// questions replace their pick with the write-in; multi-select adds it.
func (s *Screen) selectCustom() {
	item, ok := s.currentItem()
	if !ok {
		return
	}
	if item.Multiple {
		if !containsCustom(s.choices[s.index]) {
			s.choices[s.index] = append(s.choices[s.index], customOptionID)
		}
	} else {
		s.choices[s.index] = []string{customOptionID}
	}
	s.syncAnswer()
	s.syncOptions()
	s.refreshError()
	s.syncRail()
}

// toggleChoice flips one option's membership in a multi-select question.
func (s *Screen) toggleChoice(id string) {
	values := s.choices[s.index]
	for i, value := range values {
		if value == id {
			s.choices[s.index] = append(values[:i:i], values[i+1:]...)
			return
		}
	}
	s.choices[s.index] = append(append([]string(nil), values...), id)
}

// onInputChanged runs on every keystroke/paste in the write-in line: typing
// selects the custom answer (replacing any single-choice pick), keeps the
// stack icons in sync, and re-runs live validation like the shared form did.
func (s *Screen) onInputChanged(value string) {
	item, ok := s.currentItem()
	if !ok {
		return
	}
	s.custom[s.index] = value
	if len(item.Options) > 0 && strings.TrimSpace(value) != "" {
		if item.Multiple {
			if !containsCustom(s.choices[s.index]) {
				s.choices[s.index] = append(s.choices[s.index], customOptionID)
			}
		} else if !containsCustom(s.choices[s.index]) {
			s.choices[s.index] = []string{customOptionID}
		}
		s.syncOptions()
	}
	s.syncAnswer()
	s.refreshError()
	s.syncRail()
}

func (s *Screen) openCurrent() {
	s.errMsg = ""
	if _, ok := s.currentItem(); !ok {
		s.syncRail()
		s.actions = ui.NewDockedActionBar(ActionsID, s.tokens)
		return
	}
	s.input.SetValue(s.custom[s.index])
	s.syncOptions()

	buttons := make([]ui.ActionBarButton, 0, 3)
	if s.index > 0 {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "question.previous", Variant: ui.VariantSecondary, Enabled: true})
	}
	label := "Next"
	if s.index == len(s.items)-1 {
		label = "Submit"
	}
	buttons = append(buttons,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "next"), Label: label, Action: "question.next", Shortcut: ui.CommitChord, Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "question.cancel", Variant: ui.VariantDanger, Enabled: true},
	)
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
}

// syncOptions rebuilds the stacked answer rows, anchoring the highlight on the
// current answer when one exists, otherwise carrying the cursor across.
func (s *Screen) syncOptions() {
	item, ok := s.currentItem()
	if !ok || s.options == nil {
		return
	}
	carried := ""
	if selected, ok := s.options.Selected(); ok {
		carried = selected.ID
	}
	items := optionItems(item, s.choices[s.index])
	s.options.SetItems(items)
	wants := make([]string, 0, 2)
	if len(s.choices[s.index]) > 0 {
		wants = append(wants, s.choices[s.index][0])
	}
	if carried != "" {
		wants = append(wants, carried)
	}
	for _, want := range wants {
		for index, it := range items {
			if it.ID == want {
				s.options.SelectIndex(index)
				return
			}
		}
	}
}

// syncAnswer projects choices + the write-in text onto the retained answers.
// The draft (s.custom) keeps exactly what was typed; the projected answer
// trims surrounding whitespace so a trailing newline from the prompt area
// never leaks into the resolved tool payload.
func (s *Screen) syncAnswer() {
	item, ok := s.currentItem()
	if !ok || s.index < 0 || s.index >= len(s.answers) {
		return
	}
	if len(item.Options) == 0 {
		s.answers[s.index] = strings.TrimSpace(s.input.Value())
		return
	}
	custom := strings.TrimSpace(s.input.Value())
	s.custom[s.index] = s.input.Value()
	values := make([]string, 0, len(s.choices[s.index]))
	for _, id := range s.choices[s.index] {
		if id == customOptionID {
			if strings.TrimSpace(custom) != "" {
				values = append(values, custom)
			}
			continue
		}
		if index := optionIndex(id); index >= 0 && index < len(item.Options) {
			values = append(values, item.Options[index])
		}
	}
	s.answers[s.index] = strings.Join(values, ", ")
}

// canAdvance reports whether the current question carries a submittable
// answer. An empty write-in blocks even though it paints no message: the
// prompt area's placeholder already asks for text, and repeating it as an
// error line under the input said the same thing twice.
func (s *Screen) canAdvance() bool {
	item, ok := s.currentItem()
	if !ok {
		return false
	}
	if len(item.Options) == 0 {
		return strings.TrimSpace(s.input.Value()) != ""
	}
	if len(s.choices[s.index]) == 0 {
		return false
	}
	if containsCustom(s.choices[s.index]) && strings.TrimSpace(s.input.Value()) == "" {
		return false
	}
	return true
}

// validationError reports the live blocking message for the current question,
// mirroring the required/custom validators the shared form enforced. Only the
// option-choice gaps speak up; text-input gaps stay silent (see canAdvance).
func (s *Screen) validationError() string {
	item, ok := s.currentItem()
	if !ok {
		return ""
	}
	if len(item.Options) == 0 || len(s.choices[s.index]) > 0 {
		return ""
	}
	if item.Multiple {
		return "Select at least one answer"
	}
	return "Choose an answer to continue"
}

func (s *Screen) refreshError() { s.errMsg = s.validationError() }

func (s *Screen) advance() ui.EventResult {
	if s.Count() == 0 {
		return ui.EventResult{Handled: true}
	}
	s.syncAnswer()
	if !s.canAdvance() {
		// Blocked stays put. Option-choice gaps paint their hint; empty
		// write-ins rely on the placeholder and stay silent.
		if message := s.validationError(); message != "" {
			s.errMsg = message
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	s.errMsg = ""
	if s.index < len(s.items)-1 {
		s.index++
		s.syncRail()
		s.openCurrent()
		result := s.focusResult(s.entryFocusID(), true)
		result.Invalidate |= ui.InvalidateLayout
		return result
	}
	return ui.EventResult{
		Handled:    true,
		Cmd:        func() tea.Msg { return ResultMsg{Target: s, Result: s.Result()} },
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled:    true,
		Cmd:        func() tea.Msg { return CancelMsg{Target: s} },
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
	}
}

// focusResult seeds the target pane and asks the host to move deck focus.
// Entering the action bar picks its first (forward) or last (backward) button.
func (s *Screen) focusResult(target ui.ComponentID, forward bool) ui.EventResult {
	if target == ActionsID && s.actions != nil {
		if forward {
			s.actions.FocusFirst()
		} else {
			s.actions.FocusLast()
		}
	}
	return ui.EventResult{
		Handled:    true,
		Action:     &ui.Action{ID: "ui.focus", Payload: target},
		Invalidate: ui.InvalidateFocus | ui.InvalidatePaint,
	}
}

// visibleTarget redirects focus hops away from panes that do not exist for
// the current question: text-only questions have no option rows, so leaving
// the write-in (Esc) or Tabbing from the rail lands on the rail directly.
func (s *Screen) visibleTarget(target ui.ComponentID) ui.ComponentID {
	if target == OptionsID {
		if item, ok := s.currentItem(); !ok || len(item.Options) == 0 {
			return RailID
		}
	}
	return target
}

// entryFocusID is where keyboard traversal enters the current question: the
// stacked rows for option questions, the write-in line for free-text ones.
func (s *Screen) entryFocusID() ui.ComponentID {
	if item, ok := s.currentItem(); ok && len(item.Options) == 0 {
		return InputID
	}
	return OptionsID
}

func (s *Screen) Result() Result {
	answers := make(map[string]string, len(s.items))
	questions := make(map[string]string, len(s.items))
	for index, item := range s.items {
		key := fmt.Sprintf("%d", index+1)
		answers[key] = s.answers[index]
		questions[key] = item.Question
	}
	return Result{Answers: answers, Questions: questions}
}

func (s *Screen) currentItem() (Item, bool) {
	if s.index < 0 || s.index >= len(s.items) {
		return Item{}, false
	}
	return s.items[s.index], true
}

// currentTitleDetail builds the pane header texts for painting.
func (s *Screen) currentTitleDetail() (string, string) {
	item, ok := s.currentItem()
	if !ok {
		return "", ""
	}
	return s.titleDetailFor(item)
}

func (s *Screen) titleDetailFor(item Item) (string, string) {
	title := item.Header
	if title == "" {
		title = fmt.Sprintf("Question %d of %d", s.index+1, len(s.items))
	} else {
		title = fmt.Sprintf("Question %d of %d: %s", s.index+1, len(s.items), title)
	}
	return title, item.Question
}

func optionIndex(id string) int {
	var index int
	if _, err := fmt.Sscanf(id, "option:%d", &index); err != nil {
		return -1
	}
	return index
}

func containsCustom(values []string) bool {
	for _, value := range values {
		if value == customOptionID {
			return true
		}
	}
	return false
}

// railDivider paints the vertical rule between the question rail and the form
// pane. It is the last child so form rules and rail fills never mask it (the
// settings deck uses the same paint-last split divider pattern).
type railDivider struct {
	ui.Base
	tokens *ui.TokenSet
}

func (d *railDivider) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 1, H: cs.MaxH})
}

func (d *railDivider) Layout(_ ui.LayoutContext, r ui.Rect) { d.SetBounds(r) }

func (d *railDivider) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := d.Bounds()
	if r.Empty() {
		return
	}
	fg, bg := cell.NoColor, cell.NoColor
	if d.tokens != nil {
		fg, bg = d.tokens.Color(ui.ColorPopupDivider), d.tokens.Color(ui.ColorBg)
	}
	x := originX + r.X
	glyph := ui.FirstRune(ui.DefaultGlyphsFromTokens(d.tokens).BorderV, '│')
	for y := originY + r.Y; y < originY+r.Y+r.H; y++ {
		buf.Set(x, y, cell.NewCell(glyph, fg, bg, 0))
	}
}

func (d *railDivider) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }
func (d *railDivider) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleSeparator, Label: "Questions"}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Agent questions"}
}

var _ ui.Component = (*railDivider)(nil)
var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
