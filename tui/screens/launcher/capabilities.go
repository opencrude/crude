package launcher

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

type capabilityPanel struct {
	ui.Base
	owner         *Screen
	title, action string
	status        string
	ready         bool
	rows          []ui.ListItem
	list          *ui.List
	scrollbar     *ui.ScrollbarControl
}

func newCapabilityPanel(owner *Screen, id ui.ComponentID, title, action string) *capabilityPanel {
	p := &capabilityPanel{Base: ui.NewBase(id), owner: owner, title: title, action: action, status: "Checking…"}
	p.list = ui.NewList(id, owner.tokens)
	p.list.SetDefaultRowBackground(ui.ColorBg)
	p.list.SetActivateAction("launcher.capability")
	p.list.SetRowPainter(p.paintRow)
	p.scrollbar = ui.NewScrollbarControl(ui.NewID(id, "scrollbar"), p.list.Scroll(), owner.tokens)
	return p
}
func (p *capabilityPanel) SetTokens(tokens *ui.TokenSet) {
	p.list.SetTokens(tokens)
	p.scrollbar.SetTokens(tokens)
}
func (p *capabilityPanel) Children() []ui.Component { return []ui.Component{p.scrollbar} }
func (p *capabilityPanel) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	height := max(0, r.H-1)
	barW := 0
	if len(p.rows) > height && height > 0 && r.W > 0 {
		barW = 1
	}
	p.list.Layout(ctx, ui.Rect{Y: min(1, r.H), W: max(0, r.W-barW), H: height})
	p.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-barW), Y: min(1, r.H), W: barW, H: height})
}
func (p *capabilityPanel) setRows(rows []ui.ListItem, status string) {
	if p.ready && status == p.status && slices.EqualFunc(rows, p.rows, func(a, b ui.ListItem) bool { return a.ID == b.ID && a.Label == b.Label && a.Detail == b.Detail }) {
		return
	}
	p.ready = true
	p.status = status
	p.rows = rows
	p.list.SetItemsStable(rows)
}

func (p *capabilityPanel) reset() {
	p.ready = false
	p.status = "Checking…"
	p.rows = nil
	p.list.SetItems(nil)
}

// SetCapabilities distinguishes enabled/available skills from skills loaded by
// live agents, and connected MCP transports from merely configured entries.
// The viewmodel strips bindings belonging to inactive agents before delivery.
func (s *Screen) SetCapabilities(snapshot captypes.Snapshot) {
	loaded := make(map[string]bool)
	for _, binding := range snapshot.Bindings {
		if binding.Kind == captypes.KindSkill {
			loaded[binding.ID] = true
		}
	}
	var skills, connections []ui.ListItem
	activeSkills, connected, attention := 0, 0, 0
	for _, entry := range snapshot.Entries {
		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		switch entry.Kind {
		case captypes.KindSkill:
			if entry.State != "available" || entry.Mode == "disabled" || snapshot.Settings.Skills[entry.ID].Disabled {
				continue
			}
			state := "manual"
			if entry.Mode == "automatic" || entry.Mode == "" {
				state = "automatic"
			}
			if loaded[entry.ID] {
				state = "loaded"
			}
			skills = append(skills, ui.ListItem{ID: entry.ID, Label: name, Detail: state, Command: true})
			activeSkills++
		case captypes.KindMCP:
			if entry.State == "disabled" || snapshot.Settings.Connections[entry.ID].Disabled {
				continue
			}
			state := entry.State
			for _, connection := range snapshot.Connections {
				if connection.ID == entry.ConnectionID || connection.ID == entry.ID {
					// Invalid configuration is already authoritative attention
					// evidence; an idle transport must not hide it as not started.
					if entry.State == "not started" {
						state = connection.State
					}
					if connection.Cached && state == "connected" {
						state = "not started"
					}
					break
				}
			}
			if state == "not started" && snapshot.Settings.Connections[entry.ID].IsBuiltin() {
				// Crude's own servers start on an agent's first request.
				state = "ready"
			}
			switch state {
			case "connected", "ready":
				connected++
			case "failed", "needs sign-in", "unavailable", "setup required", "approval required":
				attention++
			default:
				continue
			}
			connections = append(connections, ui.ListItem{ID: entry.ID, Label: name, Detail: state, Command: true})
		}
	}
	sort.SliceStable(skills, func(i, j int) bool {
		if (skills[i].Detail == "loaded") != (skills[j].Detail == "loaded") {
			return skills[i].Detail == "loaded"
		}
		return skills[i].Label < skills[j].Label
	})
	sort.SliceStable(connections, func(i, j int) bool { return connections[i].Label < connections[j].Label })
	skillStatus := fmt.Sprintf("%d enabled", activeSkills)
	if len(skills) == 0 {
		skills = []ui.ListItem{{ID: "", Label: "No skills enabled", Command: true}}
	}
	mcpStatus := fmt.Sprintf("%d connected", connected)
	if attention > 0 {
		mcpStatus += fmt.Sprintf(" · %d need attention", attention)
	}
	if len(connections) == 0 {
		connections = []ui.ListItem{{ID: "", Label: "No active connections", Command: true}}
	}
	if snapshot.Error != "" {
		if activeSkills == 0 {
			skillStatus = "Unavailable"
			skills = []ui.ListItem{{Label: "Couldn’t read skill state", Detail: "Inspect", Command: true}}
		} else {
			skillStatus += " · refresh failed"
		}
		if connected+attention == 0 {
			mcpStatus = "Unavailable"
			connections = []ui.ListItem{{Label: "Couldn’t read connection state", Detail: "Inspect", Command: true}}
		} else {
			mcpStatus += " · refresh failed"
		}
	}
	s.skills.setRows(skills, skillStatus)
	s.mcp.setRows(connections, mcpStatus)
}
func (p *capabilityPanel) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := p.Bounds()
	if r.Empty() {
		return
	}
	tokens := p.owner.tokens
	title := rendering.TruncateVisual(p.title, r.W, "…")
	buf.WriteString(ox+r.X, oy+r.Y, title, tokens.Color(ui.ColorAccent2), cell.NoColor, cell.AttrBold)
	used := rendering.VisibleWidth(title) + 2
	if used < r.W {
		buf.WriteString(ox+r.X+used, oy+r.Y, rendering.TruncateVisual(p.status, r.W-used, "…"), tokens.Color(ui.ColorMuted), cell.NoColor, 0)
	}
	if p.ready {
		p.list.Paint(buf, ox+r.X, oy+r.Y, ctx)
	}
}
func (p *capabilityPanel) paintRow(buf *surface.CellBuffer, x, y, width int, item ui.ListItem, state ui.ControlState, ctx ui.PaintContext, tokens *ui.TokenSet) {
	fg, bg := tokens.Color(ui.ColorFg), tokens.Color(ui.ColorBg)
	if ctx.Focused && state == ui.StateFocused {
		bg = tokens.Color(ui.ColorSelection)
		fg = tokens.Color(ui.ColorAccent)
	} else if ctx.Hovered && state == ui.StateHovered {
		bg = tokens.Color(ui.ColorHover)
	}
	ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	detail := item.Detail
	if detail != "" && rendering.VisibleWidth(item.Label)+rendering.VisibleWidth(detail)+3 > width {
		detail = ""
	}
	metaW := rendering.VisibleWidth(detail)
	labelW := width
	if metaW > 0 {
		labelW -= metaW + 2
	}
	buf.WriteString(x, y, rendering.TruncateVisual(item.Label, max(0, labelW), "…"), fg, bg, 0)
	ink := tokens.Color(ui.ColorMuted)
	if detail == "connected" || detail == "loaded" {
		ink = tokens.Color(ui.ColorSuccess)
	}
	if detail == "failed" || detail == "needs sign-in" || detail == "unavailable" || detail == "setup required" || detail == "approval required" {
		ink = tokens.Color(ui.ColorWarning)
	}
	buf.WriteString(x+width-metaW, y, detail, ink, bg, 0)
}
func (p *capabilityPanel) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		p.owner.focused = p.ID()
		return p.owner.handleKey(event.Key)
	}
	if event.Kind == ui.EventMouseClick && event.MouseClick.Button == tea.MouseLeft && ctx.LocalY == 0 {
		return p.owner.command(p.action, "")
	}
	if event.IsKey() || event.Kind == ui.EventMouseClick {
		p.owner.focused = p.ID()
	}
	if event.IsMouse() {
		ctx.LocalY--
	}
	result := p.list.Handle(ctx, event)
	if result.Action != nil {
		if item, ok := p.list.Selected(); ok {
			return p.owner.command(p.action, item.ID)
		}
	}
	return result
}
func (p *capabilityPanel) Semantics() ui.Semantics {
	var rows []string
	for _, item := range p.rows {
		rows = append(rows, item.Label+" "+item.Detail)
	}
	return ui.Semantics{Role: ui.RoleList, Label: p.title, Value: p.status + "; " + strings.Join(rows, "; ")}
}
