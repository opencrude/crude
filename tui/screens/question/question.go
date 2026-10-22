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

// Screen composes a wrapped question/choice reader, bounded native write-in,
// optional question rail, explicit review, and pinned ActionBar. Answers have
// one retained owner until explicit Submit; neither choosing the last option
// nor committing a write-in resolves the request.
type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	items      []Item
	answers    []string
	choices    [][]string
	custom     []string
	railLabels []string
	index      int

	railRows    *ui.List
	options     *answerList
	input       *answerInput
	actions     *ui.ActionBar
	railGate    *focusGate
	optionsGate *focusGate
	inputGate   *focusGate
	railScroll  *ui.ScrollbarControl
	divider     *railDivider

	reviewing     bool
	reviewVisited bool
	paneX         int
	paneW         int
	paneIndent    int
	inputH        int
	railVisible   bool
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{
		Base:   ui.NewBase(ScreenID),
		tokens: ui.NewTokenSet(th),
	}
	s.railRows = ui.NewList(ui.NewID(RailID, "rows"), s.tokens)
	s.railRows.SetActivateAction("question.rail.select")
	s.railRows.SetDefaultRowBackground(ui.ColorBg)

	s.options = newAnswerList(ui.NewID(OptionsID, "rows"), s.tokens)
	s.options.SetActivateAction("question.option.activate")

	field := ui.NewTextArea(InputID, s.tokens, true)
	field.SetSubmitAction("question.next")
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
	s.reviewing, s.reviewVisited = false, false
	s.railLabels = make([]string, len(items))
	for index, item := range items {
		label := item.Header
		if label == "" {
			label = item.Question
		}
		label = strings.ReplaceAll(rendering.SanitizeForTerminal(label), "\n", " ")
		s.railLabels[index] = fmt.Sprintf("%d. %s", index+1, rendering.TruncateVisual(label, 30, "…"))
	}
	s.syncRail()
	s.openCurrent()
	return len(items), nil
}

func (s *Screen) Dismiss() {
	s.items = nil
	s.answers = nil
	s.choices = nil
	s.custom = nil
	s.railLabels = nil
	s.index = 0
	s.reviewing, s.reviewVisited = false, false
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

// syncRail rebuilds the question rail rows. Each question paints as
// "○ N. Title" (unanswered) or "✓ N. Title" (answered); divider rules
// separate entries; the active question is indicated by background and text
// color only (the shared List selection row), never a marker glyph. The rail
// indexes questions only: review is a step of the pinned actions, so it never
// borrows the numbered-row style, and no rail row is highlighted while the
// review is open.
func (s *Screen) syncRail() {
	if s.railRows == nil {
		return
	}
	items := make([]ui.ListItem, 0, max(0, s.Count()*2-1))
	for i := range s.items {
		icon := "○"
		if i < len(s.answers) && strings.TrimSpace(s.answers[i]) != "" {
			icon = "✓"
		}
		items = append(items, ui.ListItem{
			ID:      fmt.Sprintf("rail/%d", i),
			Label:   s.railLabels[i],
			Icon:    icon,
			Payload: i,
		})
		if i < s.Count()-1 {
			items = append(items, ui.ListItem{ID: fmt.Sprintf("rail/sep/%d", i), Separator: true})
		}
	}
	s.railRows.SetItems(items)
	selected := -1
	if !s.reviewing {
		for index, item := range items {
			if payload, ok := item.Payload.(int); ok && payload == s.index {
				selected = index
				break
			}
		}
	}
	if selected >= 0 {
		s.railRows.SelectIndex(selected)
		return
	}
	s.railRows.Selection().ClearSelection()
}

// railWidth sizes the question rail like the old deck's sidebar: at most 25%
// of the width (capped at 35), never below 8, and never starving the form
// below the readable minimum.
func (s *Screen) railWidth(total int) int {
	if total < 64 {
		return 0
	}
	maxItemW := 0
	for _, title := range s.railLabels {
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

// Reviewing reports the explicit final pass; no answer is sent on entering it.
func (s *Screen) Reviewing() bool { return s.reviewing }

// InvalidateWidthProfile keeps cached line breaks in the terminal's calibrated
// column space even when the allocated number of columns did not change.
func (s *Screen) InvalidateWidthProfile() {
	if !s.options.dirty {
		s.options.offset = s.options.Scroll().Offset()
	}
	s.options.dirty = true
}

// InitialFocus names the actual mounted entry control, including text-only requests.
func (s *Screen) InitialFocus() ui.ComponentID { return s.entryFocusID() }

func (s *Screen) Answer(index int) string {
	if index < 0 || index >= len(s.answers) {
		return ""
	}
	return s.answers[index]
}

func (s *Screen) Children() []ui.Component {
	if s.Count() == 0 {
		return nil
	}
	children := []ui.Component{s.optionsGate, s.actions}
	if s.railVisible {
		children = append(children, s.railGate, s.divider)
	}
	if !s.reviewing {
		children = append(children, s.inputGate)
	}
	if s.railVisible && s.railScroll != nil && s.railScroll.Needed() {
		children = append(children, s.railScroll)
	}
	return children
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
	body := s.options.ContentHeight() + 1
	if !s.reviewing {
		body += min(6, s.preferredInputH(maxHeight))
	}
	// A request is a bounded dock, not the entire transcript. Both very long
	// questions and review answers scroll inside this budget.
	return min(maxHeight, min(20, max(8, body+chrome)))
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := 2
	if s.actions != nil {
		chrome = s.actions.PreferredHeight()
	}
	chrome = min(chrome, max(0, r.H))
	bodyH := max(0, r.H-chrome)
	railW := s.railWidth(r.W)
	s.railVisible = railW > 0
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

	s.paneX = 0
	if s.railVisible {
		s.paneX = railW + 2
	}
	s.paneW = max(0, r.W-s.paneX)
	s.paneIndent = s.inputIndent(s.paneW)
	inputW := max(0, s.paneW-s.paneIndent)
	// Keep reading, editing and actions in disjoint rectangles even on tiny
	// grids. The write-in is capped independently; it never hides a long choice.
	s.inputH = 0
	if !s.reviewing {
		room := max(0, (bodyH-1)/3)
		if bodyH > 1 {
			room = max(1, room)
		}
		s.inputH = min(6, room, s.inputNaturalH(inputW, max(1, room)))
	}
	readerH := max(0, bodyH-s.inputH-1)
	s.optionsGate.Layout(ctx, ui.Rect{X: s.paneX, W: s.paneW, H: readerH})
	s.inputGate.Layout(ctx, ui.Rect{
		X: s.paneX + s.paneIndent,
		Y: readerH,
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
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
	}
	ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))
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
		if !s.railVisible {
			// Narrow forms replace the rail with the pinned actions. Once
			// focus has climbed there, Esc is the explicit cancel step.
			return s.cancel()
		}
		return s.focusResult(s.visibleTarget(RailID), false)
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case focusForwardAction, focusBackwardAction:
		target, _ := action.Payload.(ui.ComponentID)
		return s.focusResult(s.visibleTarget(target), action.ID == focusForwardAction)
	case railFocusAction:
		return s.focusResult(s.visibleTarget(RailID), false)
	case "question.rail.select":
		item, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{}
		}
		index, ok := item.Payload.(int)
		if !ok || index < 0 || index >= len(s.items) {
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		s.reviewing = false
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
	case "question.submit":
		// The human may leave any question unanswered: review submits exactly
		// what is there, and an empty answer is reported as such to the model.
		if s.reviewing {
			result := s.Result()
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ResultMsg{Target: s, Result: result} }}
		}
		return ui.EventResult{Handled: true}
	case "question.previous":
		if s.reviewing {
			s.reviewing = false
			s.index = len(s.items) - 1
			s.syncRail()
			s.openCurrent()
		} else if s.index > 0 {
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
		return s.focusResult(OptionsID, false)
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
	if s.reviewing {
		row, ok := action.Payload.(ui.ListItem)
		index, valid := row.Payload.(int)
		if !ok || !valid || index < 0 || index >= s.Count() {
			return ui.EventResult{Handled: true}
		}
		s.reviewing = false
		s.index = index
		s.syncRail()
		s.openCurrent()
		result := s.focusResult(s.entryFocusID(), true)
		result.Invalidate |= ui.InvalidateLayout
		return result
	}
	item, ok := s.currentItem()
	row, rowOK := action.Payload.(ui.ListItem)
	if !ok || !rowOK {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	id, _ := row.Payload.(string)
	if id == customOptionID {
		s.selectCustom()
		target := InputID
		if item.Multiple && !containsCustom(s.choices[s.index]) {
			target = OptionsID
		}
		result := s.focusResult(target, true)
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
		s.syncRail()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateLayout}
	}
	s.choices[s.index] = []string{id}
	s.syncAnswer()
	s.syncRail()
	return s.advance()
}

// selectCustom activates the write-in answer. Single-choice questions replace
// their pick; multi-select toggles membership without losing the retained draft.
func (s *Screen) selectCustom() {
	item, ok := s.currentItem()
	if !ok {
		return
	}
	if item.Multiple {
		s.toggleChoice(customOptionID)
	} else {
		s.choices[s.index] = []string{customOptionID}
	}
	s.syncAnswer()
	s.syncOptions()
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

// onInputChanged runs on every keystroke/paste in the write-in line. Non-empty
// text claims the write-in answer (replacing a single-choice pick); clearing it
// releases that claim, so an accidental keystroke never keeps overriding the
// chosen option or leaves the question impossible to settle.
func (s *Screen) onInputChanged(value string) {
	item, ok := s.currentItem()
	if !ok {
		return
	}
	s.custom[s.index] = value
	if len(item.Options) > 0 {
		claims := strings.TrimSpace(value) != ""
		if hadCustom := containsCustom(s.choices[s.index]); claims != hadCustom {
			switch {
			case claims && item.Multiple:
				s.choices[s.index] = append(s.choices[s.index], customOptionID)
			case claims:
				s.choices[s.index] = []string{customOptionID}
			default:
				s.choices[s.index] = withoutCustom(s.choices[s.index])
			}
			s.syncOptions()
		}
	}
	s.syncAnswer()
	s.syncRail()
}

func (s *Screen) openCurrent() {
	if _, ok := s.currentItem(); !ok {
		s.syncRail()
		s.options.SetItems(nil)
		s.input.Clear()
		s.actions = ui.NewDockedActionBar(ActionsID, s.tokens)
		return
	}
	s.input.SetValue(s.custom[s.index])
	s.options.SetItems(nil)
	s.inputGate.escAction = focusBackwardAction
	s.inputGate.escPayload = OptionsID
	if item, _ := s.currentItem(); len(item.Options) == 0 {
		s.inputGate.escAction = railFocusAction
		s.inputGate.escPayload = RailID
	}
	s.syncOptions()
	s.options.ResetViewport()

	buttons := make([]ui.ActionBarButton, 0, 3)
	if s.index > 0 {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "question.previous", Variant: ui.VariantSecondary, Enabled: true})
	}
	label := "Next"
	if s.index == len(s.items)-1 || s.reviewVisited {
		label = "Review answers"
	}
	buttons = append(buttons,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "next"), Label: label, Action: "question.next", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "question.cancel", Variant: ui.VariantDanger, Enabled: true},
	)
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
	s.actions.SetHint(s.actionHint())
}

// actionHint is the trailing guidance painted at the far end of the pinned
// actions row. The write-in has no in-field placeholder, so the text-only form
// names its only input there; every form points at the pinned decisions.
func (s *Screen) actionHint() string {
	if !s.reviewing {
		if item, ok := s.currentItem(); ok && len(item.Options) == 0 {
			return "Type your answer · Tab: actions"
		}
	}
	return "Tab: actions"
}

// syncOptions rebuilds the reading rows, carrying the cursor across toggles.
// Opening a question clears that cursor first and restores its retained choice.
func (s *Screen) syncOptions() {
	item, ok := s.currentItem()
	if !ok || s.options == nil {
		return
	}
	carried := ""
	if selected, ok := s.options.Selected(); ok {
		carried = selected.ID
	}
	title, detail := s.currentTitleDetail()
	items := []ui.ListItem{{ID: "title", Label: title, Header: true}, {ID: "question", Label: detail, Disabled: true}, {ID: "question-rule", Separator: true}}
	items = append(items, optionItems(item, s.choices[s.index])...)
	s.options.SetItems(items)
	wants := make([]string, 0, 2)
	if carried != "" {
		wants = append(wants, carried)
	}
	if len(s.choices[s.index]) > 0 {
		wants = append(wants, s.choices[s.index][0])
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

// advance moves to the next question, or opens the review on the last one.
// Nothing here gates on completeness: a human may leave any question
// unanswered, and review is the only place that decides what is sent.
func (s *Screen) advance() ui.EventResult {
	if s.Count() == 0 || s.reviewing {
		return ui.EventResult{Handled: true}
	}
	s.syncAnswer()
	if !s.reviewVisited && s.index < len(s.items)-1 {
		s.index++
		s.syncRail()
		s.openCurrent()
		result := s.focusResult(s.entryFocusID(), true)
		result.Invalidate |= ui.InvalidateLayout
		return result
	}
	return s.openReview()
}

func (s *Screen) openReview() ui.EventResult {
	s.reviewing, s.reviewVisited = true, true
	rows := []ui.ListItem{{ID: "review/title", Label: "Review answers", Header: true}}
	for index, item := range s.items {
		answer := s.answers[index]
		if strings.TrimSpace(answer) == "" {
			answer = "Not answered"
		}
		title := fmt.Sprintf("%d. %s", index+1, item.Header)
		rows = append(rows, ui.ListItem{ID: fmt.Sprintf("review/%d", index), Label: strings.TrimSpace(title) + " · Edit", Detail: item.Question + "\n\nAnswer:\n" + answer, Payload: index})
		if index < len(s.items)-1 {
			rows = append(rows, ui.ListItem{ID: fmt.Sprintf("review/sep/%d", index), Separator: true})
		}
	}
	// A trailing gap row keeps the final answer off the viewport's bottom edge
	// when the review is scrolled to its end.
	rows = append(rows, ui.ListItem{ID: "review/trailer", Spacer: true})
	s.options.SetItems(rows)
	s.options.ResetViewport()
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "question.previous", Variant: ui.VariantSecondary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "submit"), Label: "Submit", Action: "question.submit", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "question.cancel", Variant: ui.VariantDanger, Enabled: true},
	)
	s.actions.SetHint(s.actionHint())
	s.syncRail()
	result := s.focusResult(OptionsID, true)
	result.Invalidate |= ui.InvalidateLayout
	return result
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

// visibleTarget redirects focus away from the hidden write-in during review
// and from the absent rail on narrow grids. The reader always remains reachable.
func (s *Screen) visibleTarget(target ui.ComponentID) ui.ComponentID {
	if s.reviewing && target == InputID {
		return ActionsID
	}
	if target == RailID && !s.railVisible {
		return ActionsID
	}
	return target
}

// entryFocusID is where keyboard traversal enters the current question: the
// stacked rows for option questions, the write-in line for free-text ones.
func (s *Screen) entryFocusID() ui.ComponentID {
	if s.reviewing {
		return OptionsID
	}
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

// withoutCustom returns values with the write-in membership removed; the
// remaining option picks keep their order.
func withoutCustom(values []string) []string {
	out := values[:0:0]
	for _, value := range values {
		if value != customOptionID {
			out = append(out, value)
		}
	}
	return out
}

// railDivider paints the vertical rule between the question rail and the reader.
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
