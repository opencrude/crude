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
	FormID        ui.ComponentID = "session-question/form"
	ActionsID     ui.ComponentID = "session-question/actions"
	RailID        ui.ComponentID = "session-question/rail"
	RailScrollID  ui.ComponentID = "session-question/rail-scroll"
	RailDividerID ui.ComponentID = "session-question/rail-divider"

	answerFieldID  = "answer"
	customFieldID  = "custom-answer"
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

// Screen composes the shared Form, option collection, TextField, and ActionBar
// for a blocking agent question request. A question rail (shared List) mirrors
// the settings deck: every question appears as a row with an answered/unanswered
// icon; the active row is indicated by background+text color only (no marker
// glyphs), and hover/selection states come from the shared List row colors.
// It has no workspace dependencies.
type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	items      []Item
	answers    []string
	choices    [][]string
	custom     []string
	index      int
	formModel  *ui.FormModel
	form       *ui.Form
	actions    *ui.ActionBar
	rail       *ui.List
	railScroll *ui.ScrollbarControl
	divider    *railDivider
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th)}
	s.form = ui.NewDockedForm(FormID, nil, "", "", s.tokens)
	s.rail = ui.NewList(RailID, s.tokens)
	s.rail.SetActivateAction("question.rail.select")
	s.rail.SetDefaultRowBackground(ui.ColorBg)
	s.railScroll = ui.NewScrollbarControl(RailScrollID, s.rail.Scroll(), s.tokens)
	s.divider = &railDivider{Base: ui.NewBase(RailDividerID), tokens: s.tokens}
	s.openCurrent()
	return s
}

// Reset replaces the retained request data and returns the parsed question count.
func (s *Screen) Reset(questionsJSON string) int {
	var items []Item
	if questionsJSON == "" || json.Unmarshal([]byte(questionsJSON), &items) != nil || len(items) == 0 {
		s.Dismiss()
		return 0
	}
	s.items = items
	s.answers = make([]string, len(items))
	s.choices = make([][]string, len(items))
	s.custom = make([]string, len(items))
	s.index = 0
	s.syncRail()
	s.openCurrent()
	return len(items)
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
	s.form.SetTokens(tokens)
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
	s.rail.SetTokens(tokens)
	s.railScroll.SetTokens(tokens)
	s.divider.tokens = tokens
}

// syncRail rebuilds the question rail rows. Each question paints as
// "○ N. Title" (unanswered) or "✓ N. Title" (answered); divider rules
// separate entries; the active question is indicated by background and text
// color only (the shared List selection row) — never a marker glyph.
func (s *Screen) syncRail() {
	if s.rail == nil {
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
	s.rail.SetItems(items)
	for index, item := range items {
		if payload, ok := item.Payload.(int); ok && payload == s.index {
			s.rail.SelectIndex(index)
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
	children := []ui.Component{s.rail, s.form, s.actions}
	if s.railScroll != nil && s.railScroll.Needed() {
		children = append(children, s.railScroll)
	}
	// The pane divider paints last so form rules and rail fills never mask it.
	children = append(children, s.divider)
	return children
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
	return min(maxHeight, s.form.PreferredHeight(max(0, maxHeight-chrome))+chrome)
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	chrome := 2
	if s.actions != nil {
		chrome = s.actions.PreferredHeight()
	}
	bodyH := max(1, r.H-chrome)
	railW := s.railWidth(r.W)
	scroll := s.railScroll != nil && s.railScroll.Needed()
	railContentW := railW
	if scroll {
		railContentW = max(1, railW-1)
	}
	s.rail.Layout(ctx, ui.Rect{X: 0, Y: 0, W: railContentW, H: bodyH})
	if scroll {
		s.railScroll.Layout(ctx, ui.Rect{X: max(0, railW-1), Y: 0, W: 1, H: bodyH})
	} else if s.railScroll != nil {
		s.railScroll.Layout(ctx, ui.Rect{})
	}
	formX := railW + 2
	s.form.Layout(ctx, ui.Rect{X: formX, Y: 0, W: max(1, r.W-formX), H: bodyH})
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

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	// Keys unclaimed by the rail (Tab/Esc) bubble here: rail Tab enters the
	// form, Shift+Tab steps back to the action bar, Esc cancels the deck.
	switch evt.Key.Code {
	case tea.KeyTab:
		if evt.Key.Mod.Contains(tea.ModShift) {
			s.actions.FocusFirst()
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		s.form.FocusFirst()
		return focusFormResult(ui.InvalidateLayout)
	case tea.KeyEsc:
		return s.cancel()
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "form.select.open":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "form.changed":
		fieldID, _ := action.Payload.(string)
		if fieldID != answerFieldID {
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		s.syncRail()
		if s.customSelected() {
			s.form.FocusLast()
			return focusFormResult(ui.InvalidateLayout)
		}
		if item, ok := s.currentItem(); ok && !item.Multiple {
			return s.advance()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
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
		return focusFormResult(ui.InvalidateLayout)
	case "question.next":
		return s.advance()
	case "question.previous":
		if s.index > 0 {
			s.index--
			s.syncRail()
			s.openCurrent()
		}
		return focusFormResult(ui.InvalidateLayout)
	case "question.cancel":
		return s.cancel()
	case "form.cancel":
		if s.customSelected() {
			s.form.FocusFirst()
			return focusFormResult(0)
		}
		return s.cancel()
	case "form.exit.backward":
		s.actions.FocusLast()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		s.actions.FocusFirst()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: ActionsID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		if s.Count() > 0 {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: RailID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		s.form.FocusLast()
		return focusFormResult(0)
	case "actionbar.exit.forward":
		s.form.FocusFirst()
		return focusFormResult(0)
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) openCurrent() {
	model := ui.NewFormModel()
	item, ok := s.currentItem()
	if !ok {
		s.formModel = model
		s.form.SetForm(model, "Question", "")
		s.actions = ui.NewDockedActionBar(ActionsID, s.tokens)
		return
	}

	title := item.Header
	if title == "" {
		title = fmt.Sprintf("Question %d of %d", s.index+1, len(s.items))
	} else {
		title = fmt.Sprintf("Question %d of %d: %s", s.index+1, len(s.items), title)
	}
	detail := item.Question
	if len(item.Options) == 0 {
		value := ui.StringValue(s.answers[s.index])
		model.AddField(&ui.FieldState{
			ID: answerFieldID, Label: "Answer", Type: ui.FieldText, Value: value, Initial: value,
			Required: true, Placeholder: "Type your answer", SubmitAction: "question.next",
		})
	} else {
		options := questionOptions(item)
		if item.Multiple {
			value := ui.StringsValue(append([]string(nil), s.choices[s.index]...))
			model.AddField(&ui.FieldState{ID: answerFieldID, Label: "Answers", Type: ui.FieldMultiSelect, Value: value, Initial: value, Required: true, Options: options, Description: "Select all that apply."})
		} else {
			value := ui.StringValue("")
			if len(s.choices[s.index]) > 0 {
				value = ui.StringValue(s.choices[s.index][0])
			}
			model.AddField(&ui.FieldState{ID: answerFieldID, Label: "Answer", Type: ui.FieldSelect, Value: value, Initial: value, Required: true, Options: options})
		}
		customValue := ui.StringValue(s.custom[s.index])
		model.AddField(&ui.FieldState{
			ID: customFieldID, Label: "Custom answer", Type: ui.FieldText, Value: customValue, Initial: customValue,
			Disabled: !containsCustom(s.choices[s.index]), Placeholder: "Type your answer", SubmitAction: "question.next",
			Validator: func(value ui.FieldValue) []string {
				if s.customSelectedFrom(model) && strings.TrimSpace(value.String()) == "" {
					return []string{"Custom answer is required"}
				}
				return nil
			},
		})
	}
	model.OnChanged(func(_ string) {
		s.syncCustomField(model)
		s.syncAnswer(model)
		model.ValidateAll()
	})
	s.formModel = model
	s.form.SetForm(model, title, detail)
	buttons := make([]ui.ActionBarButton, 0, 3)
	if s.index > 0 {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(ActionsID, "back"), Label: "Back", Action: "question.previous", Variant: ui.VariantSecondary, Enabled: true})
	}
	label := "Next"
	if s.index == len(s.items)-1 {
		label = "Submit"
	}
	buttons = append(buttons,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "next"), Label: label, Action: "question.next", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "question.cancel", Variant: ui.VariantDanger, Enabled: true},
	)
	s.actions = ui.NewDockedActionBar(ActionsID, s.tokens, buttons...)
}

func (s *Screen) advance() ui.EventResult {
	if s.formModel == nil {
		return ui.EventResult{Handled: true}
	}
	s.syncAnswer(s.formModel)
	s.formModel.ValidateAll()
	if s.formModel.AnyErrors() {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	if s.index < len(s.items)-1 {
		s.index++
		s.syncRail()
		s.openCurrent()
		return focusFormResult(ui.InvalidateLayout)
	}
	return ui.EventResult{
		Handled:    true,
		Action:     &ui.Action{ID: "panel.close"},
		Cmd:        func() tea.Msg { return ResultMsg{Target: s, Result: s.Result()} },
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
	}
}

func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{
		Handled:    true,
		Action:     &ui.Action{ID: "panel.close"},
		Cmd:        func() tea.Msg { return CancelMsg{Target: s} },
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
	}
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

func questionOptions(item Item) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(item.Options)+1)
	for index, option := range item.Options {
		options = append(options, ui.FormOption{ID: fmt.Sprintf("option:%d", index), Label: option})
	}
	return append(options, ui.FormOption{ID: customOptionID, Label: "Type your own answer"})
}

func (s *Screen) customSelected() bool { return s.customSelectedFrom(s.formModel) }

func (s *Screen) customSelectedFrom(model *ui.FormModel) bool {
	if model == nil {
		return false
	}
	field := model.Field(answerFieldID)
	if field == nil {
		return false
	}
	if field.Type == ui.FieldMultiSelect {
		for _, value := range field.Value.Strings() {
			if value == customOptionID {
				return true
			}
		}
		return false
	}
	return field.Value.String() == customOptionID
}

func (s *Screen) syncCustomField(model *ui.FormModel) {
	custom := model.Field(customFieldID)
	if custom == nil {
		return
	}
	custom.Disabled = !s.customSelectedFrom(model)
}

func (s *Screen) syncAnswer(model *ui.FormModel) {
	item, ok := s.currentItem()
	if !ok || s.index < 0 || s.index >= len(s.answers) {
		return
	}
	field := model.Field(answerFieldID)
	if field == nil {
		return
	}
	if len(item.Options) == 0 {
		s.answers[s.index] = field.Value.String()
		return
	}
	custom := ""
	if customField := model.Field(customFieldID); customField != nil {
		custom = customField.Value.String()
	}
	s.custom[s.index] = custom
	if item.Multiple {
		s.choices[s.index] = append(s.choices[s.index][:0], field.Value.Strings()...)
		values := make([]string, 0, len(field.Value.Strings()))
		for _, id := range field.Value.Strings() {
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
		return
	}
	s.choices[s.index] = s.choices[s.index][:0]
	if field.Value.String() != "" {
		s.choices[s.index] = append(s.choices[s.index], field.Value.String())
	}
	if field.Value.String() == customOptionID {
		s.answers[s.index] = custom
		return
	}
	if index := optionIndex(field.Value.String()); index >= 0 && index < len(item.Options) {
		s.answers[s.index] = item.Options[index]
	}
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

func focusFormResult(extra ui.Invalidation) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: FormID}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint | extra}
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
