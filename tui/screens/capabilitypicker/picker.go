package capabilitypicker

import (
	"encoding/json"
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
	"crude/types/control"
)

// fact is one labelled property of the selected capability.
type fact struct {
	label, value string
	tone         ui.SemanticColor
}

type selection struct {
	id          string
	kind        captypes.Kind
	scope       captypes.Scope
	active      bool
	ready       bool
	name        string
	description string
	chip        string
	tone        ui.SemanticColor
	facts       []fact
}

// Screen is an installed-capability switcher, not a store or invocation editor:
// a searchable list of friendly names with a status chip per row, and a detail
// pane that explains the selected skill or MCP server before it is toggled.
type Screen struct {
	ui.Base
	tokens     *ui.TokenSet
	snapshot   captypes.Snapshot
	tabs       *ui.Tabs
	filter     *ui.FilterField
	list       *capabilityList
	scroll     *ui.ScrollbarControl
	actions    *ui.ActionBar
	status     *ui.Label
	category   captypes.Kind
	busy       bool
	failure    string
	summary    string
	generation uint64
	focus      func(ui.ComponentID)
	detail     ui.Rect
}

func New(th *theme.Theme, snapshot captypes.Snapshot) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase("capability-picker"), tokens: tokens, snapshot: snapshot, category: captypes.KindSkill}
	s.tabs = ui.NewTabs("capability-picker/tabs", tokens)
	s.tabs.SetChrome(ui.TabsPopup)
	s.filter = ui.NewFilterField("capability-picker/filter", tokens)
	s.filter.SetSubmitAction("capability-picker.focus-list")
	s.filter.OnChanged(func(string) { s.generation++; s.rows() })
	s.list = &capabilityList{List: ui.NewList("capability-picker/list", tokens), owner: s}
	s.list.SetActivateAction("capability-picker.select")
	s.list.SetRowPainter(s.paintRow)
	s.scroll = ui.NewScrollbarControl("capability-picker/scroll", s.list.Scroll(), tokens)
	s.scroll.SetPointerTargetSize(1)
	s.actions = ui.NewActionBar("capability-picker/actions", tokens)
	s.actions.SetCancelAction("capability-picker.close")
	s.actions.SetExitActions("capability-picker.focus-list", "capability-picker.focus-list")
	s.status = ui.NewLabel("capability-picker/status", "", th)
	s.status.SetColorRole(ui.ColorMuted)
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
	active, noun := "skills", "skills"
	if kind == captypes.KindMCP {
		active, noun = "mcps", "MCP servers"
	}
	s.tabs.SetItems([]ui.TabItem{{ID: "skills", Label: "Skills"}, {ID: "mcps", Label: "MCPs"}}, active)
	s.filter.SetPlaceholder("Search " + noun)
	s.generation++
	s.rows()
}

func (s *Screen) SetQuery(query string) { s.filter.SetValue(query); s.generation++; s.rows() }
func (s *Screen) SetSnapshot(snapshot captypes.Snapshot) {
	s.snapshot = snapshot
	s.generation++
	s.rows()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.tabs.SetTokens(tokens)
	s.filter.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scroll.SetTokens(tokens)
	s.actions.SetTokens(tokens)
	s.status.SetTokens(tokens)
}

func (s *Screen) rows() {
	entries := make(map[string]captypes.Entry)
	for _, entry := range s.snapshot.Entries {
		if entry.Kind == s.category {
			entries[entry.ID] = entry
		}
	}
	query := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	var rows []ui.ListItem
	total, enabled := 0, 0
	add := func(value selection, name string, source string, facts func(*selection, captypes.Entry)) {
		total++
		if value.active {
			enabled++
		}
		entry := entries[value.id]
		if entry.Name != "" {
			name = entry.Name
		}
		if name == "" {
			name = value.id
		}
		if !strings.Contains(strings.ToLower(name+" "+value.id+" "+entry.Description+" "+source), query) {
			return
		}
		if value.scope == "" {
			value.scope = captypes.ScopeApplication
		}
		value.name = name
		value.description = strings.Join(strings.Fields(entry.Description), " ")
		value.ready = entry.State != "unavailable" && entry.State != "setup required"
		value.chip, value.tone = "Inactive", ui.ColorMuted
		if value.active {
			value.chip, value.tone = "Active", ui.ColorSuccess
		}
		switch entry.State {
		case "unavailable":
			value.chip, value.tone = value.chip+" · Unavailable", ui.ColorWarning
		case "setup required":
			value.chip, value.tone = value.chip+" · Setup required", ui.ColorWarning
		}
		value.facts = append(value.facts, fact{label: "Status", value: value.chip, tone: value.tone})
		facts(&value, entry)
		if source != "" {
			value.facts = append(value.facts, fact{label: "Source", value: s.sourceName(source)})
		}
		value.facts = append(value.facts, fact{label: "Scope", value: scopeLabel(value.scope)})
		if entry.Reason != "" {
			value.facts = append(value.facts, fact{label: "Note", value: entry.Reason, tone: ui.ColorWarning})
		}
		rows = append(rows, ui.ListItem{ID: value.id, Label: name, Detail: value.chip, Payload: value})
	}
	if s.category == captypes.KindSkill {
		for id, value := range s.snapshot.Settings.Skills {
			config := value
			add(selection{id: id, kind: s.category, scope: value.Scope, active: !value.Disabled && value.Mode != "disabled"}, value.Name, value.Source,
				func(item *selection, entry captypes.Entry) {
					item.facts = append(item.facts, fact{label: "Mode", value: skillModeLabel(config.Mode)})
					if version := firstNonEmpty(config.Version, entry.Version); version != "" {
						item.facts = append(item.facts, fact{label: "Version", value: version})
					}
					if path := firstNonEmpty(config.Path, entry.Path); path != "" {
						item.facts = append(item.facts, fact{label: "Path", value: path})
					}
				})
		}
	} else {
		for id, value := range s.snapshot.Settings.Connections {
			config := value
			add(selection{id: id, kind: s.category, scope: value.Scope, active: !value.Disabled}, value.Name, value.Source,
				func(item *selection, entry captypes.Entry) {
					connection, tone := s.connectionState(config.ID)
					if item.active && entry.State != "unavailable" && entry.State != "setup required" {
						// Activation is availability for future use, never a claim
						// that a transport is running.
						item.chip += " · " + connection
					}
					item.facts = append(item.facts, fact{label: "Connection", value: connection, tone: tone})
					item.facts = append(item.facts, fact{label: "Transport", value: firstNonEmpty(config.Transport, entry.Transport)})
					if endpoint := firstNonEmpty(config.URL, strings.TrimSpace(config.Command+" "+strings.Join(config.Args, " "))); endpoint != "" {
						item.facts = append(item.facts, fact{label: "Endpoint", value: endpoint})
					}
				})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Label == rows[j].Label {
			return rows[i].ID < rows[j].ID
		}
		return strings.ToLower(rows[i].Label) < strings.ToLower(rows[j].Label)
	})
	noun := "skills"
	if s.category == captypes.KindMCP {
		noun = "MCP servers"
	}
	if len(rows) == 0 {
		label := "No installed " + noun + ". Install them from Settings."
		if query != "" {
			label = "No " + noun + " match “" + strings.TrimSpace(s.filter.Value()) + "”"
		}
		rows = append(rows, ui.ListItem{ID: "empty", Label: label, Disabled: true})
	}
	s.summary = fmt.Sprintf("%d of %d %s active · changes apply to future agent use", enabled, total, noun)
	if total == 0 {
		s.summary = "Changes apply to future agent use"
	}
	s.list.SetItemsStable(rows)
	s.buttons()
	s.syncStatus()
}

// connectionState reports only observed transport facts for an MCP server.
func (s *Screen) connectionState(id string) (string, ui.SemanticColor) {
	for _, connection := range s.snapshot.Connections {
		if connection.ID != id {
			continue
		}
		switch {
		case connection.AuthPending:
			return "Authorization pending", ui.ColorWarning
		case connection.State == "connected" && !connection.Cached:
			tools := len(connection.Tools)
			noun := "tools"
			if tools == 1 {
				noun = "tool"
			}
			return fmt.Sprintf("Connected · %d %s", tools, noun), ui.ColorSuccess
		case connection.Error != "":
			return "Failed: " + connection.Error, ui.ColorError
		}
	}
	return "Not connected", ui.ColorMuted
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
		return "Manual only"
	case "disabled":
		return "Disabled"
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

func (s *Screen) syncStatus() {
	switch {
	case s.busy:
		s.status.SetText("Applying change…")
		s.status.SetColorRole(ui.ColorMuted)
	case s.failure != "":
		s.status.SetText("Could not apply change: " + s.failure)
		s.status.SetColorRole(ui.ColorError)
	default:
		s.status.SetText(s.summary)
		s.status.SetColorRole(ui.ColorMuted)
	}
}

func (s *Screen) selected() (selection, bool) {
	row, ok := s.list.Selected()
	if !ok || row.Disabled {
		return selection{}, false
	}
	value, ok := row.Payload.(selection)
	return value, ok
}

func (s *Screen) buttons() {
	value, ok := s.selected()
	label := "Activate"
	if value.active {
		label = "Deactivate"
	}
	if s.busy {
		label = "Applying…"
	}
	s.actions.SetButtons(
		ui.ActionBarButton{ID: "capability-picker/toggle", Label: label, Action: "capability-picker.toggle", Enabled: ok && !s.busy && (value.active || value.ready), Variant: ui.VariantPrimary},
		ui.ActionBarButton{ID: "capability-picker/close", Label: "Close", Action: "capability-picker.close", Enabled: true})
}

func (s *Screen) BeginAction() (captypes.Action, bool) {
	value, ok := s.selected()
	if !ok || s.busy || (!value.active && !value.ready) {
		return captypes.Action{}, false
	}
	operation := "enable"
	if value.active {
		operation = "disable"
	}
	s.busy = true
	s.failure = ""
	s.generation++
	s.syncStatus()
	s.buttons()
	return captypes.Action{Operation: operation, Kind: value.kind, ID: value.id, Scope: value.scope}, true
}

func (s *Screen) Apply(snapshot captypes.Snapshot, err error) {
	s.busy = false
	s.failure = ""
	if err != nil {
		s.failure = err.Error()
	}
	s.SetSnapshot(snapshot)
}

func (s *Screen) Busy() bool                    { return s.busy }
func (s *Screen) ControlEditGeneration() uint64 { return s.generation }
func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.tabs, s.filter, s.list, s.scroll, s.status, s.actions}
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// detailMinRows is the smallest detail pane worth showing; below it the list
// keeps the whole body so cramped windows still work.
const detailMinRows = 6

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.tabs.Layout(ctx, ui.Rect{W: r.W, H: min(ui.StripHeight, r.H)})
	s.filter.Layout(ctx, ui.Rect{Y: min(ui.StripHeight+1, r.H), W: r.W, H: min(1, max(0, r.H-ui.StripHeight-1))})
	bottom := max(0, r.H-3)
	s.status.Layout(ctx, ui.Rect{Y: bottom, W: r.W, H: min(1, r.H)})
	s.actions.Layout(ctx, ui.Rect{Y: min(bottom+2, r.H), W: r.W, H: min(1, max(0, r.H-bottom-2))})
	bodyY := min(ui.StripHeight+3, bottom)
	bodyH := max(0, bottom-bodyY-1)
	listH := bodyH
	s.detail = ui.Rect{}
	if bodyH >= detailMinRows+4 {
		// The list takes what its rows need, up to about half the body; the
		// rest explains the selection.
		listH = min(len(s.list.Items()), bodyH/2)
		listH = max(listH, min(3, len(s.list.Items())))
		listH = max(listH, 1)
		s.detail = ui.Rect{Y: bodyY + listH + 1, W: r.W, H: bodyH - listH - 1}
	}
	gutter, stroke := 0, 0
	if s.scroll.NeededForViewport(listH) {
		gutter = min(ui.ScrollbarWidth+1, r.W)
		stroke = min(ui.ScrollbarWidth, gutter)
	}
	s.list.Layout(ctx, ui.Rect{Y: bodyY, W: max(0, r.W-gutter), H: listH})
	s.scroll.Layout(ctx, ui.Rect{X: max(0, r.W-stroke), Y: bodyY, W: stroke, H: listH})
}

// Paint draws the selected capability's detail pane under a divider rule. The
// list, search, status and buttons are children and paint themselves.
func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if s.detail.Empty() {
		return
	}
	tokens := s.tokens
	x, y, width := originX+s.detail.X, originY+s.detail.Y, s.detail.W
	rule := tokens.Glyphs().BorderH
	if rule == "" {
		rule = "─"
	}
	buf.WriteString(x, y-1, strings.Repeat(rule, width), tokens.Color(ui.ColorPopupDivider), cell.NoColor, 0)
	value, ok := s.selected()
	if !ok {
		return
	}
	fg, muted := tokens.Color(ui.ColorFg), tokens.Color(ui.ColorMuted)
	inner := max(0, width-2)
	row, last := y+1, y+s.detail.H-1
	kind := "Skill"
	if value.kind == captypes.KindMCP {
		kind = "MCP server"
	}
	end := buf.WriteString(x+1, row, rendering.TruncateVisual(value.name, max(0, inner-len(kind)-3), "…"), fg, cell.NoColor, cell.AttrBold)
	buf.WriteString(end+2, row, kind, muted, cell.NoColor, 0)
	row += 2
	labelWidth := 0
	for _, item := range value.facts {
		labelWidth = max(labelWidth, rendering.VisibleWidth(item.label))
	}
	factRows := len(value.facts)
	description := value.description
	descriptionColor := fg
	if description == "" {
		description, descriptionColor = "No description provided.", muted
	}
	// The description keeps at least one line; facts yield the remainder.
	lines := rendering.WordWrap(description, inner)
	room := max(1, last-row+1-factRows-1)
	if len(lines) > room {
		lines = lines[:room]
		lines[room-1] = rendering.TruncateVisual(lines[room-1]+" …", inner, "…")
	}
	for _, line := range lines {
		if row > last {
			return
		}
		buf.WriteString(x+1, row, line, descriptionColor, cell.NoColor, 0)
		row++
	}
	row++
	for _, item := range value.facts {
		if row > last {
			return
		}
		buf.WriteString(x+1, row, item.label, muted, cell.NoColor, 0)
		color := fg
		if item.tone != ui.ColorFg && item.tone != 0 {
			color = tokens.Color(item.tone)
		}
		valueX := x + 1 + labelWidth + 2
		buf.WriteString(valueX, row, rendering.TruncateVisual(item.value, max(0, x+width-1-valueX), "…"), color, cell.NoColor, 0)
		row++
	}
}

// paintRow shows each capability as a status marker, its friendly name and a
// right-aligned status chip. The list has already filled the state background.
func (s *Screen) paintRow(buf *surface.CellBuffer, x, y, width int, item ui.ListItem, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	value, ok := item.Payload.(selection)
	if !ok {
		buf.WriteString(x+1, y, rendering.TruncateVisual(item.Label, max(0, width-2), "…"), tokens.Color(ui.ColorMuted), cell.NoColor, 0)
		return
	}
	glyphs := tokens.Glyphs()
	marker := glyphs.SwitchOff
	if value.active {
		marker = glyphs.SwitchOn
	}
	buf.WriteString(x+1, y, marker, tokens.Color(value.tone), cell.NoColor, 0)
	nameColor := tokens.Color(ui.ColorFg)
	if state == ui.StateFocused || state == ui.StateSelected {
		nameColor = tokens.Color(ui.ColorAccent)
	}
	chipWidth := rendering.VisibleWidth(value.chip)
	nameX := x + 3
	nameWidth := max(0, width-3-chipWidth-3)
	if nameWidth < 8 {
		chipWidth, nameWidth = 0, max(0, width-4)
	}
	buf.WriteString(nameX, y, rendering.TruncateVisual(item.Label, nameWidth, "…"), nameColor, cell.NoColor, cell.AttrBold)
	if chipWidth > 0 {
		buf.WriteString(x+width-1-chipWidth, y, value.chip, tokens.Color(value.tone), cell.NoColor, 0)
	}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Capabilities"}
}
func (s *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		switch event.Key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "capability-picker.close"}}
		case tea.KeyDown:
			s.focusID(s.list.ID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case "tabs.activate", "tabs.enter":
		category, _ := action.Payload.(string)
		if category == "mcps" {
			s.SetCategory(captypes.KindMCP)
		} else {
			s.SetCategory(captypes.KindSkill)
		}
		s.FocusSearch()
	case "capability-picker.focus-list":
		s.focusID(s.list.ID())
	case "capability-picker.select":
		s.generation++
		s.buttons()
	case "capability-picker.toggle", "capability-picker.close":
		result.Action = &action
	default:
		return ui.EventResult{Action: &action}
	}
	return result
}

// The list owns selection/scroll; this adapter only gives Enter/Space the
// explicit toggle action while clicks remain non-mutating selections.
type capabilityList struct {
	*ui.List
	owner *Screen
}

func (l *capabilityList) ControlActions() []control.Descriptor {
	var out []control.Descriptor
	for _, id := range []string{"activate", "select", "scroll", "set_offset"} {
		out = append(out, control.Descriptor{ID: id, Scope: control.Shell, Effect: control.Write, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: control.Availability{Available: true}})
	}
	return out
}
func (l *capabilityList) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	result, err := l.List.HandleControl(ctx, op)
	if err == nil {
		l.owner.generation++
		l.owner.buttons()
		result.Invalidate |= ui.InvalidateLayout
	}
	return result, err
}
func (l *capabilityList) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Mod == 0 && (event.Key.Code == tea.KeyEnter || event.Key.Code == tea.KeySpace) {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "capability-picker.toggle"}}
	}
	result := l.List.Handle(ctx, event)
	if result.Handled {
		l.owner.generation++
		l.owner.buttons()
		result.Invalidate |= ui.InvalidateLayout
	}
	return result
}
