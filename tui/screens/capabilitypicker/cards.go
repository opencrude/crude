package capabilitypicker

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types/control"
)

// switchWidth is the painted track. Its square knob is two cells wide (one
// terminal cell is about half as wide as it is tall) and sits left when off
// and right when on. It is painted with backgrounds only, so it needs no
// ambiguous-width glyph.
const (
	switchWidth = 4
	knobWidth   = 2
)

// componentSegment keeps capability IDs (which may contain "/") a single
// ComponentID segment.
func componentSegment(id string) string { return strings.ReplaceAll(id, "/", "∕") }

// card is one self-contained capability: name, compact facts, switch and a
// three-line description. It paints its own switch so the whole card is one
// hover/hit target; the switch is exposed to semantic control as a
// ControlChildren component.
type card struct {
	ui.Base
	owner   *Screen
	item    item
	sw      *cardSwitch
	focused bool
}

func newCard(owner *Screen, id string) *card {
	c := &card{Base: ui.NewBase(ui.ComponentID("capability-picker/card/" + componentSegment(id))), owner: owner}
	c.sw = &cardSwitch{Base: ui.NewBase(c.ID() + "/switch"), owner: owner}
	return c
}

// switchRect is the switch's parent-local geometry inside the card.
func switchRect(width int) ui.Rect {
	return ui.Rect{X: max(0, width-2-switchWidth), Y: 1, W: switchWidth, H: 1}
}

func (c *card) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cardHeight})
}

func (c *card) Layout(_ ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	c.sw.id = c.item.id
	if r.Empty() {
		c.sw.SetBounds(ui.Rect{})
		return
	}
	c.sw.SetBounds(switchRect(r.W))
}

func (c *card) ControlChildren() []ui.Component { return []ui.Component{c.sw} }

func (c *card) expanded() bool { return c.owner.expanded[c.item.id] }

// height is the card's rows at width: collapsed cards keep a fixed summary;
// expanded ones fit the full description and every fact.
func (c *card) height(width int) int {
	if !c.expanded() {
		return cardHeight
	}
	return 2 + 1 + len(c.detailRows(max(0, width-4)))
}

// detailRow is one painted row of an expanded card.
type detailRow struct {
	text  string
	label string
	tone  ui.SemanticColor
}

// detailRows is the expanded body: full description, a blank row, then
// labelled facts with values wrapped under their own column.
func (c *card) detailRows(width int) []detailRow {
	description := c.item.description
	if description == "" {
		description = "No description"
	}
	var rows []detailRow
	for _, line := range rendering.WordWrap(description, width) {
		rows = append(rows, detailRow{text: line, tone: ui.ColorFg})
	}
	if len(c.item.facts) == 0 {
		return rows
	}
	rows = append(rows, detailRow{})
	labelWidth := 0
	for _, item := range c.item.facts {
		labelWidth = max(labelWidth, rendering.VisibleWidth(item.label))
	}
	valueWidth := max(1, width-labelWidth-2)
	for _, item := range c.item.facts {
		tone := item.tone
		if tone == 0 {
			tone = ui.ColorFg
		}
		for index, line := range rendering.WordWrap(item.value, valueWidth) {
			label := ""
			if index == 0 {
				label = item.label
			}
			rows = append(rows, detailRow{label: label + strings.Repeat(" ", labelWidth+2-rendering.VisibleWidth(label)), text: line, tone: tone})
		}
	}
	return rows
}

func (c *card) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	c.focused = ctx.Focused
	r := c.Bounds()
	if r.Empty() || r.W < 12 || r.H < cardHeight {
		return
	}
	tokens := c.owner.tokens
	x, y := originX+r.X, originY+r.Y
	border := tokens.Color(ui.ColorBorder)
	switch {
	case ctx.Focused:
		border = tokens.Color(ui.ColorAccent)
	case ctx.Hovered:
		border = tokens.Color(ui.ColorMuted)
	}
	paintBox(buf, tokens, x, y, r.W, r.H, border)
	value := c.item
	sw := switchRect(r.W)
	paintSwitch(buf, tokens, x+sw.X, y+sw.Y, c.owner.on(value), c.owner.toggleable(value), isPending(c.owner, value.id))

	// Title row: bold name, then compact facts in the remaining room.
	nameColor := tokens.Color(ui.ColorFg)
	if !value.registered {
		nameColor = tokens.Color(ui.ColorMuted)
	}
	// A disclosure chevron says the card opens; the name follows it.
	glyphs := tokens.Glyphs()
	chevron := glyphs.TreeCollapsed
	if c.expanded() {
		chevron = glyphs.TreeExpanded
	}
	buf.WriteString(x+2, y+1, chevron, tokens.Color(ui.ColorMuted), cell.NoColor, 0)
	nameX := x + 4
	inner := max(0, x+sw.X-1-nameX)
	end := buf.WriteString(nameX, y+1, rendering.TruncateVisual(value.name, inner, "…"), nameColor, cell.NoColor, cell.AttrBold)
	if room := nameX + inner - (end + 2); room > 3 && value.meta != "" {
		buf.WriteString(end+2, y+1, rendering.TruncateVisual(value.meta, room, "…"), tokens.Color(value.tone), cell.NoColor, 0)
	}

	width := max(0, r.W-4)
	if !c.expanded() {
		description := value.description
		if description == "" {
			description = "No description"
		}
		for index, line := range descriptionRows(description, width) {
			buf.WriteString(x+2, y+2+index, line, tokens.Color(ui.ColorMuted), cell.NoColor, 0)
		}
		return
	}
	// Expanded: everything, clipped only if the card is taller than the list.
	rows := c.detailRows(width)
	room := r.H - 3
	for index, row := range rows {
		if index >= room {
			break
		}
		text := row.text
		if index == room-1 && len(rows) > room {
			text = rendering.TruncateVisual(text+" …", max(0, width-rendering.VisibleWidth(row.label)), "…")
		}
		end := x + 2
		if row.label != "" {
			end = buf.WriteString(end, y+2+index, row.label, tokens.Color(ui.ColorMuted), cell.NoColor, 0)
		}
		buf.WriteString(end, y+2+index, text, tokens.Color(row.tone), cell.NoColor, 0)
	}
}

// descriptionRows wraps a description to at most descriptionLines rows,
// ending a longer one with an ellipsis.
func descriptionRows(text string, width int) []string {
	lines := rendering.WordWrap(text, width)
	if len(lines) > descriptionLines {
		rest := strings.Join(lines[descriptionLines-1:], " ")
		lines = append(lines[:descriptionLines-1], rendering.TruncateVisual(rest, width, "…"))
	}
	return lines
}

func isPending(s *Screen, id string) bool {
	_, saving := s.pending[id]
	return saving
}

// paintBox draws a rounded border in color; the interior keeps the window fill.
func paintBox(buf *surface.CellBuffer, tokens *ui.TokenSet, x, y, w, h int, color cell.Color) {
	glyphs := tokens.Glyphs()
	horizontal := strings.Repeat(glyphs.BorderH, max(0, w-2))
	buf.WriteString(x, y, glyphs.CornerTL+horizontal+glyphs.CornerTR, color, cell.NoColor, 0)
	for row := 1; row < h-1; row++ {
		buf.WriteString(x, y+row, glyphs.BorderV, color, cell.NoColor, 0)
		buf.WriteString(x+w-1, y+row, glyphs.BorderV, color, cell.NoColor, 0)
	}
	buf.WriteString(x, y+h-1, glyphs.CornerBL+horizontal+glyphs.CornerBR, color, cell.NoColor, 0)
}

// paintSwitch draws a track with a square knob. On is a success track with
// the knob right; off a quiet track with the knob left. Saving dims the
// track while the knob already shows the requested position; read-only
// switches stay muted.
func paintSwitch(buf *surface.CellBuffer, tokens *ui.TokenSet, x, y int, on, enabled, saving bool) {
	track, knob := tokens.Color(ui.ColorBorder), tokens.Color(ui.ColorMuted)
	if on {
		track, knob = tokens.Color(ui.ColorSuccess), tokens.Color(ui.ColorFg)
	}
	if !enabled && !saving {
		track, knob = tokens.Color(ui.ColorPopupSubtleBg), tokens.Color(ui.ColorBorder)
	}
	if saving {
		track = tokens.Color(ui.ColorMuted)
	}
	start := 0
	if on {
		start = switchWidth - knobWidth
	}
	for column := 0; column < switchWidth; column++ {
		fill := track
		if column >= start && column < start+knobWidth {
			fill = knob
		}
		buf.Set(x+column, y, cell.BlankCell(fill))
	}
}

func (c *card) Semantics() ui.Semantics {
	state := "off"
	if c.owner.on(c.item) {
		state = "on"
	}
	return ui.Semantics{Role: ui.RoleListItem, Label: c.item.name, Description: c.item.description, Value: state, Selected: c.focused}
}

func (c *card) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	switch event.Kind {
	case ui.EventMouseClick:
		if event.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		// The switch mutates; anywhere else opens or closes the details.
		if switchHit(switchRect(c.Bounds().W), ctx.LocalX, ctx.LocalY) {
			return c.toggle()
		}
		return c.owner.expand(c.item.id, !c.expanded())
	case ui.EventMouseWheel:
		delta := 1
		if event.MouseWheel.Button == tea.MouseWheelUp {
			delta = -1
		}
		return c.owner.scrollBy(delta)
	case ui.EventKey:
		if event.Key.Mod != 0 {
			return ui.EventResult{}
		}
		switch event.Key.Code {
		case tea.KeySpace:
			return c.toggle()
		case tea.KeyEnter:
			return c.owner.expand(c.item.id, !c.expanded())
		case tea.KeyRight:
			return c.owner.expand(c.item.id, true)
		case tea.KeyLeft:
			return c.owner.expand(c.item.id, false)
		}
	}
	return ui.EventResult{}
}

// switchHit widens the switch target by one cell on each side.
func switchHit(sw ui.Rect, x, y int) bool {
	return y == sw.Y && x >= sw.X-1 && x <= sw.X+sw.W
}

func (c *card) toggle() ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "capability-picker.toggle", Payload: c.item.id}, Invalidate: ui.InvalidatePaint}
}

func controlDescriptor(id string, available bool) control.Descriptor {
	availability := control.Availability{Available: available}
	if !available {
		availability.Reason, availability.Message = control.TargetUnavailable, "control is unavailable"
	}
	return control.Descriptor{ID: id, Scope: control.Shell, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: availability}
}

// ControlActions: activate opens or closes the card's details in place.
func (c *card) ControlActions() []control.Descriptor {
	return []control.Descriptor{controlDescriptor("activate", true)}
}

func (c *card) HandleControl(_ ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	if op.Verb != "activate" {
		return ui.EventResult{}, &control.Fault{Code: control.Invalid, Message: "unsupported semantic operation"}
	}
	return c.owner.expand(c.item.id, !c.expanded()), nil
}

// cardSwitch is the semantic-control projection of a card's switch. It never
// paints; its card does. Descriptor IDs are semantic verbs.
type cardSwitch struct {
	ui.Base
	owner *Screen
	id    string
}

func (sw *cardSwitch) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: switchWidth, H: 1})
}
func (sw *cardSwitch) Layout(_ ui.LayoutContext, r ui.Rect)                 { sw.SetBounds(r) }
func (sw *cardSwitch) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (sw *cardSwitch) Handle(ui.EventContext, ui.Event) ui.EventResult      { return ui.EventResult{} }
func (sw *cardSwitch) value() (item, bool)                                  { return sw.owner.item(sw.id) }
func (sw *cardSwitch) ControlActions() []control.Descriptor {
	value, ok := sw.value()
	available := ok && sw.owner.toggleable(value)
	return []control.Descriptor{controlDescriptor("activate", available), controlDescriptor("set_checked", available)}
}

func (sw *cardSwitch) Semantics() ui.Semantics {
	value, ok := sw.value()
	state := "off"
	if ok && sw.owner.on(value) {
		state = "on"
	}
	return ui.Semantics{Role: ui.RoleButton, Label: "Turn " + value.name + " on or off for this session", Value: state, Disabled: !ok || !sw.owner.toggleable(value)}
}

func (sw *cardSwitch) HandleControl(_ ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	value, ok := sw.value()
	if !ok || !sw.owner.toggleable(value) {
		return ui.EventResult{}, &control.Fault{Code: control.Unavailable, Message: "switch unavailable: no session, saving, or turned off in Settings"}
	}
	switch op.Verb {
	case "set_checked":
		var wanted bool
		if err := json.Unmarshal(op.Value, &wanted); err != nil {
			return ui.EventResult{}, err
		}
		if wanted == sw.owner.on(value) {
			return ui.EventResult{Handled: true}, nil
		}
	case "activate":
	default:
		return ui.EventResult{}, &control.Fault{Code: control.Invalid, Message: "unsupported semantic operation"}
	}
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "capability-picker.toggle", Payload: value.id}, Invalidate: ui.InvalidatePaint}, nil
}
