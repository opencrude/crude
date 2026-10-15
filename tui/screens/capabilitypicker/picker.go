package capabilitypicker

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

// Card geometry: a rounded box around a title row (name, compact facts and
// the switch) and three description rows. Cards stack in one column.
const (
	cardHeight       = 6
	cardGap          = 1
	descriptionLines = 3
)

// ManageTarget is the Settings destination of the Install & manage button.
type ManageTarget struct {
	Kind captypes.Kind
	ID   string
}

// fact is one labelled property an expanded card shows.
type fact struct {
	label, value string
	tone         ui.SemanticColor
}

// item is one installed skill or MCP server as this session sees it. A card
// shows its summary; an expanded card shows all of it in place.
type item struct {
	id          string
	kind        captypes.Kind
	name        string
	description string
	// meta is the compact fact line beside the name; tone colors it.
	meta string
	tone ui.SemanticColor
	// registered is the application/workspace registration's enable state;
	// a registration turned off in Settings cannot be turned on per session.
	registered bool
	facts      []fact
}

// Screen is the session capability switcher: a list of self-contained
// cards, each with a switch that turns that skill or MCP server on or off for
// the current session and every agent it runs.
type Screen struct {
	ui.Base
	tokens   *ui.TokenSet
	snapshot captypes.Snapshot
	category captypes.Kind

	filter *ui.FilterField
	scroll *ui.ScrollbarControl
	model  *ui.ScrollModel
	status *ui.Label
	manage *ui.ActionBar

	// Session gate: whether a session owns this modal, the IDs it turned off
	// and optimistic targets of toggles still being saved.
	session  bool
	disabled map[string]bool
	pending  map[string]bool
	// expanded cards show their full description and facts in place.
	expanded map[string]bool

	cards   map[string]*card
	visible []*card

	// label names the owning session in the heading.
	label      string
	failure    string
	generation uint64
	focus      func(ui.ComponentID)

	list      ui.Rect
	heading   ui.Rect
	cardWidth int
}

func New(th *theme.Theme, snapshot captypes.Snapshot) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase("capability-picker"), tokens: tokens, snapshot: snapshot, category: captypes.KindSkill,
		disabled: map[string]bool{}, pending: map[string]bool{}, expanded: map[string]bool{}, cards: map[string]*card{}}
	s.filter = ui.NewFilterField("capability-picker/filter", tokens)
	s.filter.SetSubmitAction("capability-picker.focus-list")
	s.filter.OnChanged(func(string) { s.generation++; s.rebuild() })
	s.model = ui.NewScrollModel()
	s.scroll = ui.NewScrollbarControl("capability-picker/scroll", s.model, tokens)
	s.scroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.scroll.SetPointerTargetSize(1)
	s.status = ui.NewLabel("capability-picker/status", "", th)
	s.status.SetColorRole(ui.ColorMuted)
	// The one command: installing and configuring belong to Settings.
	s.manage = ui.NewActionBar("capability-picker/actions", tokens)
	s.manage.SetCancelAction("capability-picker.close")
	s.manage.SetButtons(ui.ActionBarButton{ID: "capability-picker/manage", Label: "Install & manage", Action: "capability-picker.manage", Enabled: true})
	s.SetCategory(captypes.KindSkill)
	return s
}

func (s *Screen) SetFocusHandler(focus func(ui.ComponentID)) { s.focus = focus }
func (s *Screen) FocusSearch()                               { s.focusID(s.filter.ID()) }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) SetCategory(kind captypes.Kind) {
	if kind != captypes.KindMCP {
		kind = captypes.KindSkill
	}
	s.category = kind
	s.filter.SetPlaceholder("Search " + s.lowerNoun())
	s.model.ScrollTo(0)
	s.generation++
	s.rebuild()
}

func (s *Screen) Category() captypes.Kind { return s.category }

// noun is the view's heading: Skills and MCP servers are separate views.
func (s *Screen) noun() string {
	if s.category == captypes.KindMCP {
		return "MCP servers"
	}
	return "Skills"
}

// lowerNoun is the noun inside sentences; acronyms keep their case.
func (s *Screen) lowerNoun() string {
	if s.category == captypes.KindMCP {
		return "MCP servers"
	}
	return "skills"
}

// SetSessionLabel names the owning session in the heading.
func (s *Screen) SetSessionLabel(label string) { s.label = strings.TrimSpace(label) }

func (s *Screen) SetQuery(query string) { s.filter.SetValue(query); s.generation++; s.rebuild() }

func (s *Screen) SetSnapshot(snapshot captypes.Snapshot) {
	s.snapshot = snapshot
	s.generation++
	s.rebuild()
}

// SetSession installs the owning session's capability gate. available false
// means no session owns this modal, so switches are read-only.
func (s *Screen) SetSession(available bool, disabled []string) {
	s.session = available
	s.disabled = make(map[string]bool, len(disabled))
	for _, id := range disabled {
		s.disabled[id] = true
	}
	s.generation++
	s.rebuild()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.filter.SetTokens(tokens)
	s.scroll.SetTokens(tokens)
	s.status.SetTokens(tokens)
	s.manage.SetTokens(tokens)
}

// on reports the switch position: optimistic while saving, otherwise the
// registration and session gate together.
func (s *Screen) on(value item) bool {
	if target, saving := s.pending[value.id]; saving {
		return target
	}
	return value.registered && !s.disabled[value.id]
}

// toggleable reports whether the switch accepts input.
func (s *Screen) toggleable(value item) bool {
	_, saving := s.pending[value.id]
	return s.session && value.registered && !saving
}

func (s *Screen) rebuild() {
	entries := make(map[string]captypes.Entry)
	for _, entry := range s.snapshot.Entries {
		if entry.Kind == s.category {
			entries[entry.ID] = entry
		}
	}
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	var items []item
	add := func(value item, name, source string, summary []string, entry captypes.Entry, details []fact) {
		value.name = firstNonEmpty(entry.Name, name, value.id)
		value.description = strings.Join(strings.Fields(entry.Description), " ")
		if query != "" && !strings.Contains(strings.ToLower(value.name+" "+value.id+" "+value.description+" "+source), query) {
			return
		}
		switch {
		case !value.registered:
			value.meta, value.tone = "Off in Settings", ui.ColorMuted
		case entry.State == "unavailable":
			value.meta, value.tone = firstNonEmpty(entry.Reason, "Unavailable"), ui.ColorWarning
		case entry.State == "setup required":
			value.meta, value.tone = firstNonEmpty(entry.Reason, "Setup required"), ui.ColorWarning
		case value.meta == "":
			value.meta, value.tone = strings.Join(summary, " · "), ui.ColorMuted
		}
		value.facts = details
		if source != "" {
			value.facts = append(value.facts, fact{label: "Source", value: s.sourceName(source)})
		}
		if !value.registered {
			value.facts = append(value.facts, fact{label: "Note", value: "Turned off in Settings for every session", tone: ui.ColorWarning})
		} else if entry.Reason != "" {
			value.facts = append(value.facts, fact{label: "Note", value: entry.Reason, tone: ui.ColorWarning})
		}
		items = append(items, value)
	}
	if s.category == captypes.KindSkill {
		for id, config := range s.snapshot.Settings.Skills {
			entry := entries[id]
			summary := []string{skillModeLabel(config.Mode), scopeLabel(config.Scope)}
			if config.Source != "" {
				summary = append(summary, s.sourceName(config.Source))
			}
			details := []fact{{label: "Use", value: skillModeLabel(config.Mode)}, {label: "Installed for", value: scopeLabel(config.Scope)}}
			if version := firstNonEmpty(config.Version, entry.Version); version != "" {
				details = append(details, fact{label: "Version", value: version})
			}
			if path := firstNonEmpty(config.Path, entry.Path); path != "" {
				details = append(details, fact{label: "Path", value: path})
			}
			add(item{id: id, kind: captypes.KindSkill, registered: !config.Disabled && config.Mode != "disabled"}, config.Name, config.Source, summary, entry, details)
		}
	} else {
		for id, config := range s.snapshot.Settings.Connections {
			entry := entries[id]
			// Only observed transport facts; an enabled server is available
			// to agents, not necessarily running.
			connection, tone := s.connectionState(id, entry)
			value := item{id: id, kind: captypes.KindMCP, registered: !config.Disabled, meta: connection, tone: tone}
			details := []fact{{label: "Connection", value: connection, tone: tone}}
			if transport := firstNonEmpty(config.Transport, entry.Transport); transport != "" {
				value.meta += " · " + transport
				details = append(details, fact{label: "Transport", value: transport})
			}
			if endpoint := firstNonEmpty(config.URL, strings.TrimSpace(config.Command+" "+strings.Join(config.Args, " "))); endpoint != "" {
				details = append(details, fact{label: "Endpoint", value: endpoint})
			}
			if tools := s.toolNames(id); tools != "" {
				details = append(details, fact{label: "Tools", value: tools})
			}
			details = append(details, fact{label: "Installed for", value: scopeLabel(config.Scope)})
			add(value, config.Name, config.Source, nil, entry, details)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		first, second := strings.ToLower(items[i].name), strings.ToLower(items[j].name)
		if first == second {
			return items[i].id < items[j].id
		}
		return first < second
	})
	s.visible = s.visible[:0]
	retained := make(map[string]*card, len(items))
	for _, value := range items {
		c := s.cards[value.id]
		if c == nil {
			c = newCard(s, value.id)
		}
		c.item = value
		retained[value.id] = c
		s.visible = append(s.visible, c)
	}
	s.cards = retained
	s.syncStatus()
}

// connectionState reports only observed transport facts for an MCP server,
// explaining an idle server with its observed registration state.
func (s *Screen) connectionState(id string, entry captypes.Entry) (string, ui.SemanticColor) {
	for _, connection := range s.snapshot.Connections {
		if connection.ID != id {
			continue
		}
		switch {
		case connection.AuthPending:
			return "Authorization pending", ui.ColorWarning
		case connection.State == "connected" && !connection.Cached:
			noun := "tools"
			if len(connection.Tools) == 1 {
				noun = "tool"
			}
			return fmt.Sprintf("Connected · %d %s", len(connection.Tools), noun), ui.ColorSuccess
		case connection.Error != "":
			return "Failed: " + connection.Error, ui.ColorError
		}
	}
	switch entry.State {
	case "approval required":
		return "Not connected · asks before launching", ui.ColorMuted
	case "needs sign-in":
		return "Needs sign-in", ui.ColorWarning
	case "failed":
		return "Failed: " + firstNonEmpty(entry.Reason, "unknown error"), ui.ColorError
	}
	return "Not connected", ui.ColorMuted
}

// toolNames lists an observed connection's tools.
func (s *Screen) toolNames(id string) string {
	for _, connection := range s.snapshot.Connections {
		if connection.ID != id {
			continue
		}
		names := make([]string, 0, len(connection.Tools))
		for _, tool := range connection.Tools {
			names = append(names, firstNonEmpty(tool.OriginalName, tool.Name))
		}
		return strings.Join(names, ", ")
	}
	return ""
}

func (s *Screen) sourceName(id string) string {
	if source, ok := s.snapshot.Settings.Sources[id]; ok && source.Name != "" {
		return source.Name
	}
	return id
}

func scopeLabel(scope captypes.Scope) string {
	if scope == captypes.ScopeWorkspace {
		return "This workspace"
	}
	return "All workspaces"
}

func skillModeLabel(mode string) string {
	switch mode {
	case "manual":
		return "Only when asked"
	case "disabled":
		return "Never"
	default:
		return "Automatic"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// statusText is shown only when there is something to say: a failure or a
// modal without a session.
func (s *Screen) statusText() (string, ui.SemanticColor) {
	switch {
	case s.failure != "":
		return "Could not save: " + s.failure, ui.ColorError
	case !s.session:
		return "Open a session to turn skills and MCP servers on or off.", ui.ColorMuted
	}
	return "", ui.ColorMuted
}

func (s *Screen) syncStatus() {
	text, tone := s.statusText()
	s.status.SetText(text)
	s.status.SetColorRole(tone)
}

func (s *Screen) item(id string) (item, bool) {
	if c := s.cards[id]; c != nil {
		return c.item, true
	}
	return item{}, false
}

// target resolves an action's item: its payload, else the focused card.
func (s *Screen) target(payload any) string {
	if id, ok := payload.(string); ok && id != "" {
		return id
	}
	for _, c := range s.visible {
		if c.focused {
			return c.item.id
		}
	}
	return ""
}

// BeginToggle admits one switch change and shows it optimistically. It
// returns the exact item and its requested state.
func (s *Screen) BeginToggle(payload any) (string, bool, bool) {
	id := s.target(payload)
	value, ok := s.item(id)
	if !ok || !s.toggleable(value) {
		return "", false, false
	}
	enabled := !s.on(value)
	s.pending[id] = enabled
	s.failure = ""
	s.generation++
	s.syncStatus()
	return id, enabled, true
}

// FinishToggle settles a switch. The session gate must already reflect the
// committed result; a failure reverts the switch and stays visible.
func (s *Screen) FinishToggle(id string, err error) {
	delete(s.pending, id)
	s.failure = ""
	if err != nil {
		s.failure = err.Error()
	}
	s.generation++
	s.syncStatus()
}

func (s *Screen) Busy() bool                    { return len(s.pending) > 0 }
func (s *Screen) ControlEditGeneration() uint64 { return s.generation }

func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.filter}
	for _, c := range s.visible {
		children = append(children, c)
	}
	return append(children, s.scroll, s.status, s.manage)
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	// Everything keeps the shared side padding and one breathing row off the
	// window border: heading, search, then the cards.
	pad := min(s.tokens.PadLR(), max(0, r.W/10))
	x, w := pad, max(0, r.W-2*pad)
	s.heading = ui.Rect{X: x + 1, Y: min(1, r.H), W: max(0, w-1), H: min(1, max(0, r.H-1))}
	filterY := min(3, r.H)
	s.filter.Layout(ctx, ui.Rect{X: x, Y: filterY, W: w, H: min(1, max(0, r.H-filterY))})
	bottom := max(0, r.H-1)
	s.manage.Layout(ctx, ui.Rect{X: x, Y: bottom, W: w, H: min(1, r.H)})
	if text, _ := s.statusText(); text != "" && bottom > 0 {
		bottom--
		s.status.Layout(ctx, ui.Rect{X: x, Y: bottom, W: w, H: 1})
	} else {
		s.status.Layout(ctx, ui.Rect{})
	}
	bottom = max(0, bottom-1)
	top := min(filterY+2, bottom)
	s.layoutList(ctx, ui.Rect{X: x, Y: top, W: w, H: max(0, bottom-top)})
}

// layoutList stacks cards from the first visible one. Cards have variable
// height (expanded cards grow), so the scroll model counts cards: its
// viewport is chosen so the largest offset still fills the list.
func (s *Screen) layoutList(ctx ui.LayoutContext, body ui.Rect) {
	s.list = body
	gutter := 0
	s.cardWidth = body.W
	if s.lastFirst(body.W, body.H) > 0 {
		gutter = min(ui.ScrollbarWidth+1, body.W)
		s.cardWidth = max(0, body.W-gutter)
	}
	maxFirst := s.lastFirst(s.cardWidth, body.H)
	s.model.SetContent(len(s.visible))
	s.model.SetViewport(len(s.visible) - maxFirst)
	s.model.Clamp()
	first := s.model.Offset()
	y, bottom := body.Y, body.Y+body.H
	for index, c := range s.visible {
		height := min(c.height(s.cardWidth), body.H)
		if index < first || y+height > bottom {
			c.Layout(ctx, ui.Rect{})
			continue
		}
		c.Layout(ctx, ui.Rect{X: body.X, Y: y, W: s.cardWidth, H: height})
		y += height + cardGap
	}
	stroke := min(ui.ScrollbarWidth, gutter)
	s.scroll.Layout(ctx, ui.Rect{X: body.X + body.W - stroke, Y: body.Y, W: stroke, H: body.H * min(1, gutter)})
}

// lastFirst is the largest first-card offset whose remaining cards still
// fill height rows; 0 when every card fits.
func (s *Screen) lastFirst(width, height int) int {
	used := 0
	for index := len(s.visible) - 1; index >= 0; index-- {
		next := min(s.visible[index].height(width), height)
		if used > 0 {
			next += cardGap
		}
		if used+next > height {
			return index + 1
		}
		used += next
	}
	return 0
}

// ensureVisible scrolls the fewest cards so index is fully in view.
func (s *Screen) ensureVisible(index int) {
	if index < 0 || index >= len(s.visible) {
		return
	}
	width, height := s.cardWidth, s.list.H
	if width <= 0 || height <= 0 {
		s.model.EnsureVisible(index)
		return
	}
	first := min(s.model.Offset(), index)
	for first < index {
		used := 0
		for i := first; i <= index; i++ {
			used += min(s.visible[i].height(width), height)
			if i > first {
				used += cardGap
			}
		}
		if used <= height {
			break
		}
		first++
	}
	s.model.SetContent(len(s.visible))
	s.model.SetViewport(1)
	s.model.ScrollTo(first)
}

// expand opens or closes a card's full details in place.
func (s *Screen) expand(id string, open bool) ui.EventResult {
	if open {
		s.expanded[id] = true
	} else {
		delete(s.expanded, id)
	}
	s.generation++
	for index, c := range s.visible {
		if c.item.id == id {
			s.ensureVisible(index)
		}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

// Paint draws the heading and the empty-collection message; children paint
// everything else.
func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if !s.heading.Empty() {
		x, y := originX+s.heading.X, originY+s.heading.Y
		end := buf.WriteString(x, y, s.noun(), s.tokens.Color(ui.ColorFg), cell.NoColor, cell.AttrBold)
		if s.label != "" {
			buf.WriteString(end, y, rendering.TruncateVisual(" · "+s.label, max(0, x+s.heading.W-end), "…"), s.tokens.Color(ui.ColorMuted), cell.NoColor, 0)
		}
	}
	if len(s.visible) > 0 || s.list.Empty() {
		return
	}
	s.paintEmpty(buf, originX+s.list.X, originY+s.list.Y, s.list.W, s.list.H)
}

// emptyStep is one "where to start" line of the empty state.
type emptyStep struct{ action, detail string }

// emptyGuide explains what this view holds and where to start, or how to
// recover from a search that matched nothing.
func (s *Screen) emptyGuide() (title, intro string, steps []emptyStep) {
	if query := strings.TrimSpace(s.filter.Value()); query != "" {
		return "No " + s.lowerNoun() + " match “" + query + "”", "Try a shorter name or another word from the description.",
			[]emptyStep{{"Install & manage", "find more in the catalogs"}}
	}
	if s.category == captypes.KindMCP {
		return "No MCP servers installed yet",
			"MCP servers connect agents to outside tools and data: browsers, issue trackers, databases, design files and more.",
			[]emptyStep{
				{"Install & manage", "browse the official MCP registry"},
				{"Add your own", "a local command or a remote URL, in Settings"},
				{"Or just ask", "agents can find and connect one when a task needs it"},
			}
	}
	return "No skills installed yet",
		"Skills are reusable instructions agents load when a task calls for them: reverse engineering, document formats, house style and more.",
		[]emptyStep{
			{"Install & manage", "browse Anthropic skills and the Skills Directory"},
			{"Add your own", "any folder with a SKILL.md, in Settings"},
			{"Or just ask", "agents can find and install one when a task needs it"},
		}
}

// paintEmpty draws the guide as a quiet framed panel centred in the list
// area: a bold title, a short explanation and where to start. Step
// details wrap under their own column rather than being cut off.
func (s *Screen) paintEmpty(buf *surface.CellBuffer, x, y, width, height int) {
	tokens := s.tokens
	title, intro, steps := s.emptyGuide()
	boxW := min(width, 72)
	inner := max(0, boxW-6)
	introLines := rendering.WordWrap(intro, inner)
	actionWidth := 0
	for _, step := range steps {
		actionWidth = max(actionWidth, rendering.VisibleWidth(step.action))
	}
	detailWidth := max(1, inner-2-actionWidth-2)
	details := make([][]string, len(steps))
	stepRows := 0
	for index, step := range steps {
		details[index] = rendering.WordWrap(step.detail, detailWidth)
		stepRows += max(1, len(details[index]))
	}
	// Frame, padding row, title, gap, intro, gap, steps, padding row, frame.
	boxH := min(height, 2+1+1+1+len(introLines)+1+stepRows+1)
	if boxW < 24 || boxH < 5 {
		buf.WriteString(x, y, rendering.TruncateVisual(title, width, "…"), tokens.Color(ui.ColorFg), cell.NoColor, cell.AttrBold)
		return
	}
	left := x + (width-boxW)/2
	y += max(0, (height-boxH)/2)
	paintBox(buf, tokens, left, y, boxW, boxH, tokens.Color(ui.ColorBorder))
	row, last := y+2, y+boxH-2
	write := func(column int, text string, color cell.Color, attrs cell.Attrs) int {
		if row > last {
			return column
		}
		return buf.WriteString(column, row, text, color, cell.NoColor, attrs)
	}
	write(left+3, rendering.TruncateVisual(title, inner, "…"), tokens.Color(ui.ColorFg), cell.AttrBold)
	row += 2
	for _, line := range introLines {
		write(left+3, line, tokens.Color(ui.ColorMuted), 0)
		row++
	}
	row++
	for index, step := range steps {
		end := write(left+3, "→ ", tokens.Color(ui.ColorMuted), 0)
		color, attrs := tokens.Color(ui.ColorFg), cell.Attrs(0)
		if step.action == "Install & manage" {
			// Matches the button below, which is where this step happens.
			color, attrs = tokens.Color(ui.ColorAccent), cell.AttrBold
		}
		write(end, step.action, color, attrs)
		detailX := end + actionWidth + 2
		for _, line := range details[index] {
			write(detailX, line, tokens.Color(ui.ColorMuted), 0)
			row++
		}
		if len(details[index]) == 0 {
			row++
		}
	}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Capabilities"}
}

// scrollBy moves the list by whole cards.
func (s *Screen) scrollBy(delta int) ui.EventResult {
	before := s.model.Offset()
	s.model.ScrollBy(delta)
	if s.model.Offset() == before {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

// move focuses the card delta positions from the focused one, keeping it in
// view; moving above the first card returns to search.
func (s *Screen) move(delta int) ui.EventResult {
	if len(s.visible) == 0 {
		return ui.EventResult{Handled: true}
	}
	current := -1
	for index, c := range s.visible {
		if c.focused {
			current = index
			break
		}
	}
	next := 0
	if current >= 0 {
		next = current + delta
		if next < 0 {
			s.FocusSearch()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		next = min(len(s.visible)-1, next)
	}
	s.ensureVisible(next)
	s.focusID(s.visible[next].ID())
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if !event.IsKey() || event.Key.Mod != 0 {
		return ui.EventResult{}
	}
	switch event.Key.Code {
	case tea.KeyEsc:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "capability-picker.close"}}
	case tea.KeyDown:
		return s.move(1)
	case tea.KeyUp:
		return s.move(-1)
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case "capability-picker.focus-list":
		return s.move(0)
	case "capability-picker.manage":
		action.Payload = ManageTarget{Kind: s.category}
		result.Action = &action
	case "capability-picker.toggle", "capability-picker.close":
		result.Action = &action
	default:
		return ui.EventResult{Action: &action}
	}
	return result
}
