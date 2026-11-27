// Package workspacehub is the Workspace panel: one surface with Overview,
// Tabs, Sessions, Local History and Storage tabs. Like every movable panel
// it docks, opens as a main tab or opens in its own window.
package workspacehub

import (
	"strings"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
)

// Tab is one page of the hub.
type Tab string

// The hub's pages.
const (
	TabOverview Tab = "overview"
	TabTabs     Tab = "tabs"
	TabSessions Tab = "sessions"
	TabHistory  Tab = "history"
	TabStorage  Tab = "storage"
)

// Tabs lists the pages in strip order.
var Tabs = []Tab{TabOverview, TabTabs, TabSessions, TabHistory, TabStorage}

// Component IDs of the hub, its tab strip and the Errand link.
const (
	HubID    ui.ComponentID = "workspace-hub"
	StripID  ui.ComponentID = "workspace-hub/tabs"
	ErrandID ui.ComponentID = "workspace-hub/errand"
)

const (
	// ActionTab reports the shown page (payload Tab) so its owner reads data.
	ActionTab = "workspacehub.tab"
	// ActionErrand asks the owner to open the workspace Errand.
	ActionErrand = "workspacehub.errand"
)

// errandLabels are the link's labels, longest first. The short label keeps
// the link on narrow panels.
var errandLabels = []string{"Open workspace Errand", "Errand"}

// minStripWidth keeps the tab strip usable when the link takes room.
const minStripWidth = 20

// Hub owns the tab strip and shows one page below it. The strip's right end
// holds Open workspace Errand on every page. Pages are ordinary components;
// the hub never paints their content.
type Hub struct {
	ui.Base
	tokens          *ui.TokenSet
	strip           *ui.Tabs
	errand          *ui.ActionLink
	active          Tab
	pages           map[Tab]ui.Component
	dockHeaderInset int
	focus           func(ui.ComponentID) bool
	focused         func() ui.ComponentID
}

// New builds the hub with its pages; it shows Tabs first.
func New(tokens *ui.TokenSet, pages map[Tab]ui.Component) *Hub {
	h := &Hub{Base: ui.NewBase(HubID), tokens: tokens, active: TabTabs, pages: pages}
	h.strip = ui.NewTabs(StripID, tokens)
	h.strip.SetChrome(ui.TabsShell)
	h.strip.SetRuleFullBleed(true)
	h.errand = ui.NewActionLink(ErrandID, errandLabels[0], ActionErrand)
	h.errand.SetTokens(tokens)
	h.errand.SetVariant(ui.VariantSecondary)
	h.syncStrip()
	return h
}

func (h *Hub) syncStrip() {
	labels := map[Tab][2]string{
		TabOverview: {"Overview", "Overview"},
		TabTabs:     {"Tabs", "Tabs"},
		TabSessions: {"Sessions", "Sessions"},
		TabHistory:  {"Local History", "History"},
		TabStorage:  {"Storage", "Storage"},
	}
	items := make([]ui.TabItem, 0, len(Tabs))
	for _, tab := range Tabs {
		items = append(items, ui.TabItem{ID: string(tab), Label: labels[tab][0], CompactLabel: labels[tab][1]})
	}
	h.strip.SetItems(items, string(h.active))
}

// SetTokens applies a theme to the strip, the link and every page.
func (h *Hub) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	h.tokens = tokens
	h.strip.SetTokens(tokens)
	h.errand.SetTokens(tokens)
	for _, page := range h.pages {
		if themed, ok := page.(interface{ SetTokens(*ui.TokenSet) }); ok {
			themed.SetTokens(tokens)
		}
	}
}

// SetFocusHandler installs the host's focus request and focus query.
func (h *Hub) SetFocusHandler(focus func(ui.ComponentID) bool, focused func() ui.ComponentID) {
	h.focus, h.focused = focus, focused
}

// SetDockHeaderInset leaves room for the dock's own header buttons.
func (h *Hub) SetDockHeaderInset(width int) { h.dockHeaderInset = max(0, width) }

// Active is the shown page.
func (h *Hub) Active() Tab { return h.active }

// SetActive shows tab.
func (h *Hub) SetActive(tab Tab) {
	if _, ok := h.pages[tab]; ok {
		h.active = tab
		h.syncStrip()
	}
}

// Page returns the component of tab.
func (h *Hub) Page(tab Tab) ui.Component { return h.pages[tab] }

// PreferredFocus is the shown page's own preferred control.
func (h *Hub) PreferredFocus() ui.ComponentID {
	page := h.pages[h.active]
	if page == nil {
		return StripID
	}
	if preferred, ok := page.(interface{ PreferredFocus() ui.ComponentID }); ok {
		return preferred.PreferredFocus()
	}
	return page.ID()
}

// OwnsFocus reports whether id is the hub, its strip, its link or a control
// of the shown page.
func (h *Hub) OwnsFocus(id ui.ComponentID) bool {
	if id == HubID || strings.HasPrefix(string(id), string(HubID)+"/") {
		return true
	}
	page := h.pages[h.active]
	if page == nil {
		return false
	}
	if owner, ok := page.(interface{ OwnsFocus(ui.ComponentID) bool }); ok {
		return owner.OwnsFocus(id)
	}
	return within(id, page.ID())
}

func within(id, root ui.ComponentID) bool {
	return id == root || strings.HasPrefix(string(id), string(root)+"/")
}

// Children are the strip, the link when it fits, and the shown page.
func (h *Hub) Children() []ui.Component {
	children := []ui.Component{h.strip}
	if !h.errand.Bounds().Empty() {
		children = append(children, h.errand)
	}
	if page := h.pages[h.active]; page != nil {
		children = append(children, page)
	}
	return children
}

// Measure fills the space the dock gives it.
func (h *Hub) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

// Layout places the strip beside the dock's own buttons with the Errand link
// at its right end, then the shown page below it.
func (h *Hub) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	h.SetBounds(bounds)
	width, height := bounds.W, bounds.H
	top := min(ui.StripHeight, height)
	stripW := max(0, width-h.dockHeaderInset)
	h.strip.SetRuleBleed(0, width)
	h.strip.Layout(ctx, ui.Rect{W: stripW, H: top})
	h.errand.Layout(ctx, ui.Rect{})
	if top > 0 {
		// The longest label that fits beside the whole strip wins; on a
		// narrow panel the short label stays and the strip compacts.
		labelsEnd := h.strip.LabelRowEnd()
		for index, label := range errandLabels {
			h.errand.SetLabel(label)
			linkW := h.errand.Measure(ctx, ui.Loose(stripW, 1)).W
			last := index == len(errandLabels)-1
			if stripW-labelsEnd > linkW || last && stripW-linkW-1 >= minStripWidth {
				h.errand.Layout(ctx, ui.Rect{X: stripW - linkW, W: linkW, H: 1})
				h.strip.Layout(ctx, ui.Rect{W: stripW - linkW - 1, H: top})
				break
			}
		}
	}
	for tab, page := range h.pages {
		if tab != h.active {
			page.Layout(ctx, ui.Rect{})
		}
	}
	if page := h.pages[h.active]; page != nil {
		page.Layout(ctx, ui.Rect{Y: top, W: width, H: max(0, height-top)})
	}
}

// Paint fills the hub's background; children paint themselves.
func (h *Hub) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	ui.FillRect(buf, originX, originY, h.Bounds(), cell.BlankCell(h.tokens.Color(ui.ColorBg)))
}

// focusOrder is the strip, the shown page's controls, then the link. The
// strip itself sends Tab into the page, so the link closes the cycle.
func (h *Hub) focusOrder() []ui.ComponentID {
	order := []ui.ComponentID{StripID}
	if page, ok := h.pages[h.active].(interface{ FocusOrder() []ui.ComponentID }); ok {
		order = append(order, page.FocusOrder()...)
	} else {
		order = append(order, h.PreferredFocus())
	}
	if !h.errand.Bounds().Empty() {
		order = append(order, ErrandID)
	}
	return order
}

// Handle moves keyboard focus through the strip, the link and the page with
// Tab when the focused control does not use the key itself.
func (h *Hub) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() || h.focus == nil || h.focused == nil {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "tab", "shift+tab":
		order := h.focusOrder()
		step := 1
		if evt.Key.String() == "shift+tab" {
			step = -1
		}
		current, focused := -1, h.focused()
		for index, id := range order {
			if within(focused, id) {
				current = index
			}
		}
		if current < 0 && step < 0 {
			current = 0
		}
		h.focus(order[(current+step+len(order))%len(order)])
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// HandleAction switches pages from the strip and reports the shown page.
func (h *Hub) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate":
		if id, ok := action.Payload.(string); ok {
			h.SetActive(Tab(id))
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionTab, Payload: h.active}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "tabs.enter":
		// Tab, Down or Enter on the strip enters the page at its first
		// control, so Tab visits every control in turn.
		if h.focus != nil {
			target := h.PreferredFocus()
			if order := h.focusOrder(); len(order) > 1 && order[1] != ErrandID {
				target = order[1]
			}
			h.focus(target)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

// Semantics names the surface.
func (h *Hub) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace"}
}

var _ ui.Parent = (*Hub)(nil)
var _ ui.ActionHandler = (*Hub)(nil)
