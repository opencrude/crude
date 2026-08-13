package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
)

// PanelPolicy describes interaction without coupling it to placement.
type PanelPolicy uint8

const (
	PanelPassive PanelPolicy = iota
	PanelInteractive
	PanelBlocking
)

// PanelEntry is one retained panel in a docked deck.
type PanelEntry struct {
	ID              string
	Title           string
	OwnerID         string
	Content         Component
	Closable        bool
	Attention       bool
	Policy          PanelPolicy
	PreferredHeight int
}

// PreferredHeightComponent allows panel content to report dynamic intrinsic
// height without coupling PanelDeck to a feature screen.
type PreferredHeightComponent interface {
	PreferredHeight(maxHeight int) int
}

// PanelDeck is a native docked panel region. It is session-agnostic; product
// projections decide which deck belongs to which session.
type PanelDeck struct {
	Base
	tokens  *TokenSet
	tabs    *Tabs
	panels  []PanelEntry
	active  int
	content Rect
}

func NewPanelDeck(id ComponentID, tokens *TokenSet) *PanelDeck {
	return &PanelDeck{Base: NewBase(id), tokens: tokens, tabs: NewTabs(NewID(id, "tabs"), tokens), active: -1}
}

func (d *PanelDeck) SetTokens(tokens *TokenSet) {
	d.tokens = tokens
	d.tabs.SetTokens(tokens)
	if panel := d.Active(); panel != nil {
		applyTokenSet(panel.Content, tokens)
	}
}

func (d *PanelDeck) Open(panel PanelEntry) {
	if panel.ID == "" || panel.Content == nil {
		return
	}
	blocking := d.Active()
	blocksActivation := blocking != nil && blocking.Policy == PanelBlocking && blocking.ID != panel.ID
	queuesPassive := blocking != nil && panel.Policy == PanelPassive && blocking.ID != panel.ID
	if blocksActivation {
		panel.Attention = true
	} else if !queuesPassive {
		panel.Attention = false
	}
	for i := range d.panels {
		if d.panels[i].ID == panel.ID {
			d.panels[i] = panel
			if !blocksActivation && !queuesPassive {
				d.active = i
			}
			d.syncTabs()
			return
		}
	}
	d.panels = append(d.panels, panel)
	if !blocksActivation && !queuesPassive {
		d.active = len(d.panels) - 1
	}
	d.syncTabs()
}

func (d *PanelDeck) Close(id string) bool {
	for i := range d.panels {
		if d.panels[i].ID != id {
			continue
		}
		closedBlocking := i == d.active && d.panels[i].Policy == PanelBlocking
		d.panels = append(d.panels[:i], d.panels[i+1:]...)
		if len(d.panels) == 0 {
			d.active = -1
		} else if closedBlocking {
			d.active = min(i, len(d.panels)-1)
			for index := range d.panels {
				if d.panels[index].Attention {
					d.active = index
					break
				}
			}
		} else if i <= d.active {
			d.active--
			if d.active < 0 {
				d.active = 0
			}
		}
		if d.active >= len(d.panels) {
			d.active = len(d.panels) - 1
		}
		if d.active >= 0 {
			d.panels[d.active].Attention = false
		}
		d.syncTabs()
		return true
	}
	return false
}

func (d *PanelDeck) CloseActive() bool {
	if panel := d.Active(); panel != nil && panel.Closable {
		return d.Close(panel.ID)
	}
	return false
}

func (d *PanelDeck) Activate(id string) bool {
	if panel := d.Active(); panel != nil && panel.Policy == PanelBlocking && panel.ID != id {
		return false
	}
	for i := range d.panels {
		if d.panels[i].ID == id {
			d.active = i
			d.panels[i].Attention = false
			d.syncTabs()
			return true
		}
	}
	return false
}

func (d *PanelDeck) Active() *PanelEntry {
	if d.active < 0 || d.active >= len(d.panels) {
		return nil
	}
	return &d.panels[d.active]
}

func (d *PanelDeck) IsOpen() bool { return len(d.panels) > 0 }
func (d *PanelDeck) Count() int   { return len(d.panels) }

// Panels returns a copy of the docked panel entries.
func (d *PanelDeck) Panels() []PanelEntry {
	if d == nil || len(d.panels) == 0 {
		return nil
	}
	out := make([]PanelEntry, len(d.panels))
	copy(out, d.panels)
	return out
}

func (d *PanelDeck) PreferredHeight(maxHeight int) int {
	panel := d.Active()
	if panel == nil || maxHeight <= 0 {
		return 0
	}
	chrome := 1
	if len(d.panels) > 1 {
		chrome = 3
	}
	contentH := panel.PreferredHeight
	if provider, ok := panel.Content.(PreferredHeightComponent); ok {
		contentH = provider.PreferredHeight(max(0, maxHeight-chrome))
	}
	if contentH < 4 {
		contentH = 4
	}
	return min(maxHeight, contentH+chrome)
}

func (d *PanelDeck) Children() []Component {
	panel := d.Active()
	if panel == nil {
		return nil
	}
	if len(d.panels) > 1 {
		return []Component{d.tabs, panel.Content}
	}
	return []Component{panel.Content}
}

func (d *PanelDeck) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: cs.MaxW, H: d.PreferredHeight(cs.MaxH)})
}

func (d *PanelDeck) Layout(ctx LayoutContext, r Rect) {
	d.SetBounds(r)
	contentY := 1
	if len(d.panels) > 1 {
		d.tabs.Layout(ctx, Rect{X: 0, Y: 1, W: r.W, H: 1})
		contentY = 3
	}
	d.content = Rect{X: 0, Y: contentY, W: r.W, H: max(0, r.H-contentY)}
	if panel := d.Active(); panel != nil {
		panel.Content.Layout(ctx, d.content)
	}
}

func (d *PanelDeck) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	r := d.Bounds()
	bg, divider := d.color(ColorPopupBg), d.color(ColorPopupDivider)
	hBorder := FirstRune(d.tokens.Glyphs().BorderH, '─')
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	if r.W > 0 && r.H > 0 {
		for x := 0; x < r.W; x++ {
			buf.Set(originX+r.X+x, originY+r.Y, cell.NewCell(hBorder, divider, bg, 0))
		}
	}
	if len(d.panels) > 1 && r.H > 2 {
		for x := 0; x < r.W; x++ {
			buf.Set(originX+r.X+x, originY+r.Y+2, cell.NewCell(hBorder, divider, bg, 0))
		}
	}
}

func (d *PanelDeck) Handle(_ EventContext, evt Event) EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		if panel := d.Active(); panel != nil && panel.Closable {
			id := panel.ID
			d.Close(id)
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.Active().Content.ID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	}
	return EventResult{}
}

func (d *PanelDeck) HandleAction(_ EventContext, action Action) EventResult {
	switch action.ID {
	case "tabs.activate":
		id, _ := action.Payload.(string)
		if panel := d.Active(); panel != nil && panel.Policy == PanelBlocking && panel.ID != id {
			return EventResult{Handled: true}
		}
		if d.Activate(id) {
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.Active().Content.ID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	case "tabs.enter":
		if panel := d.Active(); panel != nil {
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: panel.Content.ID()}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
	case "tabs.close":
		if panel := d.Active(); panel != nil && panel.Policy == PanelBlocking {
			return EventResult{Handled: true}
		}
		id, _ := action.Payload.(string)
		if d.Close(id) {
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.Active().Content.ID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	case "panel.close":
		if panel := d.Active(); panel != nil {
			id := panel.ID
			d.Close(id)
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.Active().Content.ID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	}
	return EventResult{Action: &action}
}

func (d *PanelDeck) syncTabs() {
	items := make([]TabItem, len(d.panels))
	activeID := ""
	for i, panel := range d.panels {
		items[i] = TabItem{ID: panel.ID, Label: panel.Title, Closable: panel.Closable, Attention: panel.Attention}
		if i == d.active {
			activeID = panel.ID
		}
	}
	d.tabs.SetItems(items, activeID)
}

func (d *PanelDeck) color(role SemanticColor) cell.Color {
	if d.tokens == nil {
		return cell.NoColor
	}
	return d.tokens.Color(role)
}

func (d *PanelDeck) Semantics() Semantics {
	label := "Docked panels"
	if panel := d.Active(); panel != nil {
		label = panel.Title
	}
	return Semantics{Role: RoleGroup, Label: label, Selected: d.IsOpen()}
}

var _ Parent = (*PanelDeck)(nil)
var _ ActionHandler = (*PanelDeck)(nil)
