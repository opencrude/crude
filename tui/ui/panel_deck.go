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

// PanelWidth selects how much of the deck width a panel's content may use.
// The deck (the parent) enforces it; content never self-sizes horizontally.
type PanelWidth uint8

const (
	// PanelFullWidth lets content span the whole deck content row (default).
	PanelFullWidth PanelWidth = iota
	// PanelReadableWidth clamps content to a readable column sized by
	// ReadableWidth (left-aligned), so compact forms stay orderly on wide
	// decks.
	PanelReadableWidth
	// PanelWideReadable clamps content to a wider-than-readable column
	// (WideReadableWidth) so trailing detail columns (e.g. "inherited from
	// workspace") stay visible without spanning the whole deck row.
	PanelWideReadable
)

// ReadableWidth adapts a content column to the available deck width: ultra
// wide decks yield a third of their width, wide decks a half, and narrow
// decks keep the full width so controls never cramp.
func ReadableWidth(avail int) int {
	switch {
	case avail >= 200:
		return avail / 3
	case avail >= 110:
		return avail / 2
	default:
		return avail
	}
}

// WideReadableWidth is a looser clamp than ReadableWidth for panels that carry
// trailing detail columns (e.g. "inherited from workspace"): two thirds on
// ultra-wide decks, three quarters on wide decks, full width otherwise.
func WideReadableWidth(avail int) int {
	switch {
	case avail >= 200:
		return avail * 2 / 3
	case avail >= 110:
		return avail * 3 / 4
	default:
		return avail
	}
}

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
	Width           PanelWidth
	// Peers in a nonempty group can be selected without bypassing a blocker.
	BlockingGroup string
	// User close goes to the owner instead of discarding an unresolved request.
	CloseAction string
	FocusID     ComponentID
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
	tabs := NewTabs(NewID(id, "tabs"), tokens)
	tabs.SetChrome(TabsShell)
	return &PanelDeck{Base: NewBase(id), tokens: tokens, tabs: tabs, active: -1}
}

func (d *PanelDeck) SetTokens(tokens *TokenSet) {
	d.tokens = tokens
	d.tabs.SetTokens(tokens)
	if panel := d.Active(); panel != nil {
		applyTokenSet(panel.Content, tokens)
		applyDockedSurface(panel.Content)
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
			applyDockedSurface(panel.Content)
			d.syncTabs()
			return
		}
	}
	d.panels = append(d.panels, panel)
	if !blocksActivation && !queuesPassive {
		d.active = len(d.panels) - 1
	}
	applyDockedSurface(panel.Content)
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
			for index := range d.panels {
				if d.panels[index].Policy == PanelBlocking {
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
	for i := range d.panels {
		if d.panels[i].ID == id {
			if !d.canActivate(i) {
				return false
			}
			d.active = i
			d.panels[i].Attention = false
			d.syncTabs()
			return true
		}
	}
	return false
}

func (d *PanelDeck) canActivate(index int) bool {
	active := d.Active()
	if active == nil || active.Policy != PanelBlocking || active.ID == d.panels[index].ID {
		return true
	}
	peer := &d.panels[index]
	return active.BlockingGroup != "" && peer.Policy == PanelBlocking && peer.BlockingGroup == active.BlockingGroup
}

func (d *PanelDeck) activeFocusID() ComponentID {
	if panel := d.Active(); panel != nil {
		if panel.FocusID != "" {
			return panel.FocusID
		}
		return panel.Content.ID()
	}
	return RootID
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
		chrome = 1 + StripHeight
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

// contentPad derives the deck's horizontal content padding from the theme
// tokens (the parent enforces the theme): tabs and panel content keep the
// padding model while separator rules run edge to edge via rule bleed.
// Narrow decks cap the padding at half the width, like the shell's content
// geometry.
func (d *PanelDeck) contentPad(width int) int {
	if d.tokens == nil || width < 1 {
		return 0
	}
	return min(max(0, d.tokens.PadLR()), width/2)
}

func (d *PanelDeck) Layout(ctx LayoutContext, r Rect) {
	d.SetBounds(r)
	padX := d.contentPad(r.W)
	innerW := max(0, r.W-2*padX)
	contentY := 1
	if len(d.panels) > 1 {
		// The strip spans the deck row: labels sit flush on the deck's left
		// edge (matching the deck rule above them and the baseline they own),
		// while panel content keeps the padded column below.
		d.tabs.Layout(ctx, Rect{X: 0, Y: 1, W: r.W, H: StripHeight})
		contentY = 1 + StripHeight
	}
	contentW := innerW
	if panel := d.Active(); panel != nil {
		switch panel.Width {
		case PanelReadableWidth:
			contentW = min(innerW, ReadableWidth(innerW))
		case PanelWideReadable:
			contentW = min(innerW, WideReadableWidth(innerW))
		}
	}
	// Publish the deck row geometry for configurable full-bleed separator
	// rules BEFORE laying content out, so rule owners (forms, action bars,
	// picker dividers) already know the row when they size separator fields.
	// Rules span the deck edge to edge (offsetX columns left of the content
	// origin, width the full deck row); components keep rules bounded to
	// their own column unless they opted in with SetRuleFullBleed.
	d.content = Rect{X: padX, Y: contentY, W: contentW, H: max(0, r.H-contentY)}
	if panel := d.Active(); panel != nil {
		applyRuleBleed(panel.Content, d.content.X, r.W)
		panel.Content.Layout(ctx, d.content)
	}
}

func (d *PanelDeck) Paint(buf *surface.CellBuffer, originX, originY int, _ PaintContext) {
	r := d.Bounds()
	bg, divider := d.color(ColorBg), d.color(ColorPopupDivider)
	hBorder := FirstRune(d.tokens.Glyphs().BorderH, '─')
	FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	if r.W > 0 && r.H > 0 {
		for x := 0; x < r.W; x++ {
			buf.Set(originX+r.X+x, originY+r.Y, cell.NewCell(hBorder, divider, bg, 0))
		}
	}
}

func (d *PanelDeck) Handle(_ EventContext, evt Event) EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc {
		if panel := d.Active(); panel != nil && panel.Closable {
			id := panel.ID
			if panel.CloseAction != "" {
				return EventResult{Handled: true, Action: &Action{ID: panel.CloseAction, Payload: id}}
			}
			if panel.Policy == PanelBlocking {
				return EventResult{Handled: true}
			}
			d.Close(id)
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.activeFocusID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	}
	return EventResult{}
}

func (d *PanelDeck) HandleAction(_ EventContext, action Action) EventResult {
	switch action.ID {
	case "tabs.activate":
		id, _ := action.Payload.(string)
		if d.Activate(id) {
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.activeFocusID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
		return EventResult{Handled: true}
	case "tabs.enter":
		if panel := d.Active(); panel != nil {
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.activeFocusID()}, Invalidate: InvalidateFocus | InvalidatePaint}
		}
	case "tabs.close":
		id, _ := action.Payload.(string)
		for i := range d.panels {
			panel := &d.panels[i]
			if panel.ID != id {
				continue
			}
			if !panel.Closable || !d.canActivate(i) {
				return EventResult{Handled: true}
			}
			if panel.CloseAction != "" {
				return EventResult{Handled: true, Action: &Action{ID: panel.CloseAction, Payload: id}}
			}
			if panel.Policy == PanelBlocking {
				return EventResult{Handled: true}
			}
		}
		if d.Close(id) {
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.activeFocusID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
		}
	case "panel.close":
		if panel := d.Active(); panel != nil {
			id := panel.ID
			d.Close(id)
			if !d.IsOpen() {
				return EventResult{Handled: true, Action: &Action{ID: "paneldeck.empty", Payload: id}, Invalidate: InvalidateLayout | InvalidatePaint}
			}
			return EventResult{Handled: true, Action: &Action{ID: "ui.focus", Payload: d.activeFocusID()}, Invalidate: InvalidateLayout | InvalidateFocus | InvalidatePaint}
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

// ruleBleeder is implemented by components with separator rules that can
// expand to a parent-supplied row geometry (Form, ActionBar, deck screens
// with their own divider rows). offsetX is how far left of the component's
// own origin the row starts, width the total row width. Whether the
// geometry is actually used stays configurable per rule owner
// (SetRuleFullBleed); the parent only publishes the deck-wide row.
type ruleBleeder interface {
	SetRuleBleed(offsetX, width int)
}

func applyRuleBleed(c Component, offsetX, width int) {
	if c == nil {
		return
	}
	if rb, ok := c.(ruleBleeder); ok {
		rb.SetRuleBleed(offsetX, width)
	}
	if p, ok := c.(Parent); ok {
		for _, child := range p.Children() {
			applyRuleBleed(child, offsetX, width)
		}
	}
}

// applyDockedSurface paints session-owned deck content on the chat surface
// so settings, questions, confirmations, YOLO, and jobs do not sit on a
// raised popup fill. Screens that hide Form from Children must still use
// NewDockedForm when they construct or replace that form.
func applyDockedSurface(c Component) {
	if c == nil {
		return
	}
	if s, ok := c.(interface{ SetSurfaceBackground(SemanticColor) }); ok {
		s.SetSurfaceBackground(ColorBg)
	}
	if p, ok := c.(Parent); ok {
		for _, child := range p.Children() {
			applyDockedSurface(child)
		}
	}
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
