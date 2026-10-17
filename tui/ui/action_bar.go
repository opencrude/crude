package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
)

// ButtonVariant controls the visual style of a button in an ActionBar.
type ButtonVariant int

const (
	VariantPrimary ButtonVariant = iota
	VariantSecondary
	VariantDanger
	VariantGhost
)

// ActionBarButton is a button in an ActionBar.
type ActionBarButton struct {
	ID       ComponentID
	Label    string
	Action   string
	Shortcut string
	Variant  ButtonVariant
	Enabled  bool
}

// ActionBar is a horizontal row of buttons for form save/cancel/reset actions.
type ActionBar struct {
	Base
	buttons       []actionBarBtn
	tokens        *TokenSet
	gap           int
	alignEnd      bool
	focused       int
	hovered       int
	cancelAction  string
	exitForward   string
	exitBackward  string
	showRule      bool
	surfaceRole   SemanticColor
	surfaceSet    bool
	ruleFullBleed bool
	ruleBleedX    int
	ruleBleedW    int
	// labelGen is the keyboard profile generation the shortcut captions were
	// resolved for; a profile change re-resolves them before measuring.
	labelGen uint64
}

type actionBarBtn struct {
	title    string // label without the shortcut caption
	label    string
	action   string
	shortcut string
	variant  ButtonVariant
	enabled  bool
	button   *Button
}

// NewActionBar creates an action bar with the given buttons.
func NewActionBar(id ComponentID, tokens *TokenSet, btns ...ActionBarButton) *ActionBar {
	ab := &ActionBar{
		Base:         NewBase(id),
		tokens:       tokens,
		gap:          2,
		alignEnd:     true,
		hovered:      -1,
		exitForward:  "actionbar.exit.forward",
		exitBackward: "actionbar.exit.backward",
		buttons:      make([]actionBarBtn, len(btns)),
	}
	ab.labelGen = KeyboardProfileGeneration()
	for i, b := range btns {
		label := actionBarLabel(b.Label, b.Shortcut)
		bid := b.ID
		if bid == RootID {
			bid = NewID(id, b.Label)
		}
		btn := NewButton(bid, label, b.Action)
		if tokens != nil {
			btn.SetTokens(tokens)
		}
		btn.SetDisabled(!b.Enabled)
		btn.SetVariant(b.Variant)
		ab.buttons[i] = actionBarBtn{
			title:    b.Label,
			label:    label,
			action:   b.Action,
			shortcut: b.Shortcut,
			variant:  b.Variant,
			enabled:  b.Enabled,
			button:   btn,
		}
	}
	return ab
}

// SetButtons reconciles by stable component ID so live projections do not
// replace focused, hovered, or pressed controls on every update.
func (ab *ActionBar) SetButtons(btns ...ActionBarButton) {
	existing := make(map[ComponentID]actionBarBtn, len(ab.buttons))
	focusedID := ComponentID("")
	if ab.focused >= 0 && ab.focused < len(ab.buttons) {
		focusedID = ab.buttons[ab.focused].button.ID()
	}
	for _, button := range ab.buttons {
		existing[button.button.ID()] = button
	}
	next := make([]actionBarBtn, 0, len(btns))
	for _, spec := range btns {
		id := spec.ID
		if id == RootID {
			id = NewID(ab.ID(), spec.Label)
		}
		label := actionBarLabel(spec.Label, spec.Shortcut)
		button, ok := existing[id]
		if !ok {
			button.button = NewButton(id, label, spec.Action)
		}
		button.title, button.label, button.action, button.shortcut = spec.Label, label, spec.Action, spec.Shortcut
		button.variant, button.enabled = spec.Variant, spec.Enabled
		button.button.SetLabel(label)
		button.button.SetActionID(spec.Action)
		button.button.SetTokens(ab.tokens)
		button.button.SetDisabled(!spec.Enabled)
		button.button.SetVariant(spec.Variant)
		next = append(next, button)
	}
	ab.buttons = next
	ab.focused = min(ab.focused, max(0, len(ab.buttons)-1))
	for index := range ab.buttons {
		if focusedID != "" && ab.buttons[index].button.ID() == focusedID {
			ab.focused = index
			break
		}
	}
	ab.syncButtonSurface()
}

// NewDockedActionBar packs buttons from the left on the chat surface, with a
// rule above the row so dock actions read as the last form line.
func NewDockedActionBar(id ComponentID, tokens *TokenSet, btns ...ActionBarButton) *ActionBar {
	ab := NewActionBar(id, tokens, btns...)
	ab.SetAlignEnd(false)
	ab.SetShowRule(true)
	ab.SetSurfaceBackground(ColorBg)
	ab.SetRuleFullBleed(true)
	return ab
}

// SetCancelAction configures the typed action emitted for Escape.
func (ab *ActionBar) SetCancelAction(action string) { ab.cancelAction = action }

// SetAlignEnd right-aligns buttons (default). False packs them from the left.
func (ab *ActionBar) SetAlignEnd(v bool) { ab.alignEnd = v }

// SetShowRule paints a divider on the first row and places buttons below it.
func (ab *ActionBar) SetShowRule(v bool) { ab.showRule = v }

// SetRuleFullBleed configures whether the rule may expand beyond the bar's
// own width when the parent supplies a wider bleed row. Docked bars opt in
// so the rule spans the whole deck row edge to edge while buttons stay in
// the padded column. Per-rule control stays with the bar; the parent only
// supplies the row geometry.
func (ab *ActionBar) SetRuleFullBleed(v bool) { ab.ruleFullBleed = v }

// SetRuleBleed is called by the parent (PanelDeck) at layout time with the
// deck row geometry a full-bleed rule may span: offsetX columns left of the
// bar's own origin, width the total row width. Width 0 keeps the rule
// bounded to the bar's own width.
func (ab *ActionBar) SetRuleBleed(offsetX, width int) { ab.ruleBleedX, ab.ruleBleedW = offsetX, width }

// SetSurfaceBackground is the idle button fill (dock ColorBg, popup ColorPopupBg).
func (ab *ActionBar) SetSurfaceBackground(role SemanticColor) {
	ab.surfaceRole = role
	ab.surfaceSet = true
	ab.syncButtonSurface()
}

func (ab *ActionBar) syncButtonSurface() {
	if !ab.surfaceSet || ab.tokens == nil {
		return
	}
	bg := ab.tokens.Color(ab.surfaceRole)
	for i := range ab.buttons {
		ab.buttons[i].button.SetSurfaceBackground(bg)
	}
}

// ButtonLabelsForTest returns the visible button labels in layout order.
func (ab *ActionBar) ButtonLabelsForTest() []string {
	if ab == nil {
		return nil
	}
	out := make([]string, len(ab.buttons))
	for i, b := range ab.buttons {
		out[i] = b.label
	}
	return out
}

func (ab *ActionBar) FocusFirst() { ab.focused = 0 }

func (ab *ActionBar) FocusLast() {
	if n := len(ab.buttons); n > 0 {
		ab.focused = n - 1
	}
}

func (ab *ActionBar) PreferredHeight() int {
	if ab.showRule {
		return 2
	}
	return 1
}

// SetExitActions overrides the Tab-out actions (defaults actionbar.exit.*).
func (ab *ActionBar) SetExitActions(forward, backward string) {
	if forward != "" {
		ab.exitForward = forward
	}
	if backward != "" {
		ab.exitBackward = backward
	}
}

// SetEnabled updates a button by action ID without rebuilding the bar.
func (ab *ActionBar) SetEnabled(action string, enabled bool) bool {
	for i := range ab.buttons {
		if ab.buttons[i].action != action {
			continue
		}
		ab.buttons[i].enabled = enabled
		ab.buttons[i].button.SetDisabled(!enabled)
		return true
	}
	return false
}

// SetLabel updates a button's visible label without replacing the retained
// button component or its focus state.
func (ab *ActionBar) SetLabel(action, label string) bool {
	for i := range ab.buttons {
		if ab.buttons[i].action != action {
			continue
		}
		full := actionBarLabel(label, ab.buttons[i].shortcut)
		if ab.buttons[i].label == full {
			return true // action exists; callers must not recreate the bar
		}
		ab.buttons[i].title, ab.buttons[i].label = label, full
		ab.buttons[i].button.SetLabel(full)
		if !ab.Bounds().Empty() {
			ab.Layout(LayoutContext{}, ab.Bounds())
		}
		return true
	}
	return false
}

func (ab *ActionBar) SetTokens(ts *TokenSet) {
	ab.tokens = ts
	for i := range ab.buttons {
		ab.buttons[i].button.SetTokens(ts)
	}
	ab.syncButtonSurface()
}

func (ab *ActionBar) Children() []Component {
	// Buttons are retained by the bar so it can provide one focus scope and
	// translate actions at the screen boundary.
	return nil
}

// actionBarLabel appends a button's shortcut caption. Modifier chords go
// through FormatShortcut so "ctrl+s" and "Ctrl+S" read alike and semantic
// chords name the key this terminal delivers; a bare key ("a", "n") stays as
// typed so it never reads as Shift+key.
func actionBarLabel(title, shortcut string) string {
	if shortcut == "" {
		return title
	}
	caption := shortcut
	if _, ok := ChordLabel(shortcut); ok || strings.Contains(shortcut, "+") {
		caption = FormatShortcut(strings.ToLower(shortcut))
	}
	return title + " (" + caption + ")"
}

// syncShortcutLabels re-resolves shortcut captions after a keyboard profile
// change (the terminal's keyboard-enhancement answer can arrive after the bar
// was built).
func (ab *ActionBar) syncShortcutLabels() {
	gen := KeyboardProfileGeneration()
	if ab.labelGen == gen {
		return
	}
	ab.labelGen = gen
	for i := range ab.buttons {
		ab.buttons[i].label = actionBarLabel(ab.buttons[i].title, ab.buttons[i].shortcut)
		ab.buttons[i].button.SetLabel(ab.buttons[i].label)
	}
}

func (ab *ActionBar) Measure(_ LayoutContext, cs Constraints) Size {
	ab.syncShortcutLabels()
	totalW := 0
	for i := range ab.buttons {
		totalW += rendering.VisibleWidth(ab.buttons[i].label) + 4
		if i < len(ab.buttons)-1 {
			totalW += ab.gap
		}
	}
	return cs.ClampSize(Size{W: totalW, H: ab.PreferredHeight()})
}

func (ab *ActionBar) Layout(ctx LayoutContext, r Rect) {
	ab.syncShortcutLabels()
	ab.SetBounds(r)
	if r.Empty() {
		for i := range ab.buttons {
			ab.buttons[i].button.Layout(ctx, Rect{})
		}
		return
	}
	totalW := 0
	widths := make([]int, len(ab.buttons))
	for i := range ab.buttons {
		ab.buttons[i].button.SetLabel(ab.buttons[i].label)
		bw := rendering.VisibleWidth(ab.buttons[i].label) + 4
		widths[i] = bw
		totalW += bw
		if i < len(ab.buttons)-1 {
			totalW += ab.gap
		}
	}
	// Shortcut hints yield before any decision disappears. Keep the complete
	// labels as source state so widening restores them and key routing remains
	// unchanged. Remaining overflow is shared across the widest buttons.
	if totalW > r.W {
		totalW = max(0, len(widths)-1) * ab.gap
		for i := range ab.buttons {
			label := ab.buttons[i].title
			ab.buttons[i].button.SetLabel(label)
			widths[i] = rendering.VisibleWidth(label) + 4
			totalW += widths[i]
		}
	}
	gap := ab.gap
	if totalW > r.W {
		if len(widths) > 1 {
			gap = min(gap, max(0, (r.W-len(widths))/(len(widths)-1)))
		}
		available := max(0, r.W-max(0, len(widths)-1)*gap)
		lo, hi := 0, available
		for lo < hi {
			capWidth := lo + (hi-lo+1)/2
			sum := 0
			for _, width := range widths {
				sum += min(width, capWidth)
			}
			if sum <= available {
				lo = capWidth
			} else {
				hi = capWidth - 1
			}
		}
		remaining := available
		for _, width := range widths {
			remaining -= min(width, lo)
		}
		totalW = max(0, len(widths)-1) * gap
		for i, width := range widths {
			widths[i] = min(width, lo)
			if width > lo && remaining > 0 {
				widths[i]++
				remaining--
			}
			totalW += widths[i]
		}
	}
	x := 0
	if ab.alignEnd {
		x = max(0, r.W-totalW)
	}
	y := 0
	if ab.showRule {
		y = 1
	}
	for i := range ab.buttons {
		ab.buttons[i].button.Layout(ctx, Rect{X: x, Y: y, W: widths[i], H: 1})
		x += widths[i] + gap
	}
}

func (ab *ActionBar) Paint(buf *surface.CellBuffer, originX, originY int, ctx PaintContext) {
	r := ab.Bounds()
	if r.Empty() {
		return
	}
	if ab.showRule && ab.tokens != nil && r.W > 0 {
		bg := ab.tokens.Color(ColorPopupBg)
		if ab.surfaceSet {
			bg = ab.tokens.Color(ab.surfaceRole)
		}
		divider := ab.tokens.Color(ColorPopupDivider)
		ox, oy := originX+r.X, originY+r.Y
		ruleX, ruleW := 0, r.W
		if ab.ruleFullBleed && ab.ruleBleedW-ab.ruleBleedX > ruleW {
			ruleX, ruleW = -ab.ruleBleedX, ab.ruleBleedW
		}
		FillRect(buf, 0, 0, Rect{X: ox, Y: oy, W: r.W, H: r.H}, cell.BlankCell(bg))
		buf.WriteString(ox+ruleX, oy, strings.Repeat(string(FirstRune(ab.tokens.Glyphs().BorderH, '─')), max(0, ruleW)), divider, bg, 0)
	}
	for i := range ab.buttons {
		buttonCtx := ctx
		buttonCtx.Focused = ctx.Focused && i == ab.focused
		buttonCtx.Hovered = ctx.Hovered && i == ab.hovered
		ab.buttons[i].button.Paint(buf, originX+r.X, originY+r.Y, buttonCtx)
	}
}

func (ab *ActionBar) Handle(ctx EventContext, evt Event) EventResult {
	if ab.Bounds().Empty() {
		return EventResult{}
	}
	if evt.IsKey() {
		key := evt.Key
		if key.Code == tea.KeyEsc && ab.cancelAction != "" {
			return EventResult{Handled: true, Action: &Action{ID: ab.cancelAction}, Invalidate: InvalidatePaint}
		}
		for i := range ab.buttons {
			if MatchesShortcut(key, ab.buttons[i].shortcut) && ab.buttons[i].enabled {
				ab.focused = i
				return EventResult{Handled: true, Action: &Action{ID: ab.buttons[i].action}, Invalidate: InvalidateFocus | InvalidatePaint}
			}
		}
		switch key.Code {
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				if ab.focused == 0 {
					return EventResult{Handled: true, Action: &Action{ID: ab.exitBackward}, Invalidate: InvalidateFocus | InvalidatePaint}
				}
				ab.focused--
				return EventResult{Handled: true, Invalidate: InvalidatePaint}
			}
			if len(ab.buttons) > 0 && ab.focused == len(ab.buttons)-1 {
				return EventResult{Handled: true, Action: &Action{ID: ab.exitForward}, Invalidate: InvalidateFocus | InvalidatePaint}
			}
			ab.focused++
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyLeft:
			if n := len(ab.buttons); n > 0 {
				ab.focused = (ab.focused - 1 + n) % n
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyRight:
			if n := len(ab.buttons); n > 0 {
				ab.focused = (ab.focused + 1) % n
			}
			return EventResult{Handled: true, Invalidate: InvalidatePaint}
		case tea.KeyUp:
			return EventResult{Handled: true, Action: &Action{ID: ab.exitBackward}, Invalidate: InvalidateFocus | InvalidatePaint}
		case tea.KeyDown:
			return EventResult{Handled: true, Action: &Action{ID: ab.exitForward}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
		if ab.focused < 0 || ab.focused >= len(ab.buttons) {
			return EventResult{}
		}
		return ab.handleButton(ab.focused, ctx, evt)
	}
	if evt.Kind == EventMouseMotion {
		hovered := ab.buttonAt(ctx.LocalX, ctx.LocalY)
		if hovered != ab.hovered {
			ab.hovered = hovered
			return EventResult{Handled: hovered >= 0, Invalidate: InvalidatePaint}
		}
		return EventResult{Handled: hovered >= 0}
	}
	if evt.Kind == EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		index := ab.buttonAt(ctx.LocalX, ctx.LocalY)
		if index < 0 {
			return EventResult{}
		}
		ab.focused = index
		return ab.handleButton(index, ctx, evt)
	}
	return EventResult{}
}

func (ab *ActionBar) buttonAt(x, y int) int {
	for i := range ab.buttons {
		if ab.buttons[i].button.Bounds().Contains(x, y) {
			return i
		}
	}
	return -1
}

// ButtonBounds reports the bar-local bounds of the button carrying the
// given action ID. ok is false when no mounted button carries it.
func (ab *ActionBar) ButtonBounds(action string) (Rect, bool) {
	if ab == nil {
		return Rect{}, false
	}
	for i := range ab.buttons {
		if ab.buttons[i].action == action {
			return ab.buttons[i].button.Bounds(), true
		}
	}
	return Rect{}, false
}

// indexOfAction returns the layout index of the button carrying the action
// ID, or -1 when no mounted button carries it.
func (ab *ActionBar) indexOfAction(action string) int {
	for i := range ab.buttons {
		if ab.buttons[i].action == action {
			return i
		}
	}
	return -1
}

// ActionEnabled reports whether the button carrying the action ID is
// enabled. False when no mounted button carries the action.
func (ab *ActionBar) ActionEnabled(action string) bool {
	if ab == nil {
		return false
	}
	for i := range ab.buttons {
		if ab.buttons[i].action == action {
			return ab.buttons[i].enabled
		}
	}
	return false
}

// ButtonLabels returns the visible button labels in layout order.
func (ab *ActionBar) ButtonLabels() []string {
	if ab == nil {
		return nil
	}
	out := make([]string, len(ab.buttons))
	for i := range ab.buttons {
		out[i] = ab.buttons[i].label
	}
	return out
}

// FocusButton focuses the button at index (mouse path: click focuses
// without activation; activation stays an explicit Enter/second click).
// Returns false when the index is out of range.
func (ab *ActionBar) FocusButton(index int) bool {
	if ab == nil || index < 0 || index >= len(ab.buttons) {
		return false
	}
	if ab.focused == index {
		return true
	}
	ab.focused = index
	return true
}

func (ab *ActionBar) handleButton(index int, ctx EventContext, evt Event) EventResult {
	b := ab.buttons[index].button
	br := b.Bounds()
	return b.Handle(EventContext{
		Theme:   ctx.Theme,
		Focused: ctx.Focused && index == ab.focused,
		LocalX:  ctx.LocalX - br.X,
		LocalY:  ctx.LocalY - br.Y,
		ScreenX: ctx.ScreenX,
		ScreenY: ctx.ScreenY,
	}, evt)
}
