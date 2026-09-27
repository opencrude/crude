package settings

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/agent"
	"crude/tools"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
)

const agentCatalogID ui.ComponentID = "settings/agents"
const AgentSettingsFormID ui.ComponentID = "settings/agents/form"

var agentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)
var agentRoles = []struct {
	key, label string
	index      int
}{{"default", "Main Agent", catMain}, {"researcher", "Researcher", catResearcher}, {"crafter", "Crafter", catCrafter}, {"aeron", "Aeron", catAeron}}

type agentCatalog struct {
	ui.Base
	owner                     *WorkspaceSettingsSurface
	model                     *Model
	filter                    *agentCatalogFilter
	table                     *ui.Table
	tabs                      *ui.Tabs
	form                      *ui.Form
	bar                       *ui.ActionBar
	listScroll, formScroll    *ui.ScrollbarControl
	draft                     *ui.FormModel
	base                      types.CategorySettings
	profile                   types.AgentProfileSettings
	roleIndex                 int
	id, page, confirm, notice string
}

func newAgentCatalog(owner *WorkspaceSettingsSurface) *agentCatalog {
	t := owner.tokens
	c := &agentCatalog{Base: ui.NewBase(agentCatalogID), owner: owner, model: owner.model, roleIndex: -1, page: "model"}
	c.filter = &agentCatalogFilter{FilterField: ui.NewFilterField("settings/agents/search", t), owner: c}
	c.filter.SetPlaceholder("Search agents and roles")
	c.table = ui.NewTable("settings/agents/table", t)
	c.table.SetActivateAction("agents.edit")
	c.table.SetShowHeader(true)
	c.table.SetColumnGap(2)
	c.tabs = ui.NewTabs("settings/agents/tabs", t)
	c.tabs.SetItems([]ui.TabItem{{ID: "model", Label: "Model"}, {ID: "instructions", Label: "Instructions"}, {ID: "tools", Label: "Tool Access"}}, "model")
	c.form = ui.NewForm(AgentSettingsFormID, nil, "", "", t)
	c.form.SetFieldGap(0)
	c.bar = ui.NewActionBar("settings/agents/actions", t)
	c.bar.SetAlignEnd(false)
	c.listScroll = ui.NewScrollbarControl("settings/agents/scroll", c.table.Scroll(), t)
	c.formScroll = ui.NewScrollbarControl("settings/agents/form-scroll", c.form.Scroll(), t)
	c.refreshRows()
	return c
}

func (c *agentCatalog) syncModel() {
	if c.model != c.owner.model {
		c.model = c.owner.model
		c.draft = nil
		c.confirm = ""
		c.refreshRows()
	}
}

func (c *agentCatalog) refreshRows() {
	if c.model == nil {
		return
	}
	selected := ""
	if row, ok := c.table.Selected(); ok {
		selected = row.ID
	}
	var rows []ui.TableRow
	query := strings.ToLower(c.filter.Value())
	add := func(id, name, role, model, description string) {
		if query != "" && !strings.Contains(strings.ToLower(name+" "+role+" "+model+" "+description), query) {
			return
		}
		if model == "" {
			model = "Inherit role"
		}
		rows = append(rows, ui.TableRow{ID: id, Cells: []ui.TableCell{{Text: name}, {Text: role, Role: ui.ColorMuted}, {Text: model, Role: ui.ColorMuted}}})
	}
	settings := c.model.AppSettings().Agents
	for _, role := range agentRoles {
		cfg := settings.Categories[role.key]
		add("role/"+role.key, role.label, "Built-in", cfg.Model, cfg.Description)
	}
	keys := make([]string, 0, len(settings.AgentProfiles))
	for key := range settings.AgentProfiles {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		p := settings.AgentProfiles[key]
		add("profile/"+key, key, p.Category, p.Model, p.Description)
	}
	c.table.SetRows(rows)
	for i, row := range rows {
		if row.ID == selected {
			c.table.SelectIndex(i)
		}
	}
	c.rebuildBar()
}

func (c *agentCatalog) rebuildBar() {
	var buttons []ui.ActionBarButton
	switch c.confirm {
	case "discard":
		buttons = []ui.ActionBarButton{{Label: "Discard edits", Action: "agents.discard", Variant: ui.VariantDanger, Enabled: true}, {Label: "Keep editing", Action: "agents.stay", Enabled: true}}
	case "remove":
		buttons = []ui.ActionBarButton{{Label: "Remove agent", Action: "agents.remove-confirm", Variant: ui.VariantDanger, Enabled: true}, {Label: "Keep agent", Action: "agents.stay", Enabled: true}}
	default:
		if c.draft != nil {
			buttons = []ui.ActionBarButton{{Label: "Apply agent", Action: "agents.apply", Variant: ui.VariantPrimary, Enabled: true}, {Label: "Back", Action: "agents.back", Enabled: true}}
		} else {
			row, ok := c.table.Selected()
			buttons = []ui.ActionBarButton{{Label: "Add", Action: "agents.add", Variant: ui.VariantPrimary, Enabled: true}, {Label: "Edit", Action: "agents.edit", Enabled: ok}, {Label: "Clone", Action: "agents.duplicate", Enabled: ok}, {Label: "Remove", Action: "agents.remove", Variant: ui.VariantDanger, Enabled: ok && strings.HasPrefix(row.ID, "profile/")}}
		}
	}
	c.bar.SetButtons(buttons...)
}

func (c *agentCatalog) begin(key string, duplicate bool) {
	c.roleIndex = -1
	c.id = ""
	c.base = types.CategorySettings{}
	c.profile = types.AgentProfileSettings{Category: "crafter"}
	settings := c.model.AppSettings().Agents
	for _, role := range agentRoles {
		if key == "role/"+role.key {
			c.roleIndex = role.index
			c.id = role.key
			c.base = settings.Categories[role.key]
			c.profile.Category = role.key
		}
	}
	if strings.HasPrefix(key, "profile/") {
		c.id = strings.TrimPrefix(key, "profile/")
		c.profile = settings.AgentProfiles[c.id]
		p := c.profile
		c.base = types.CategorySettings{AccountID: p.AccountID, Model: p.Model, Description: p.Description, Instructions: p.Instructions, AllowedTools: p.AllowedTools, DeniedTools: p.DeniedTools, ContextCompactionPercent: p.ContextCompactionPercent}
	}
	if duplicate {
		c.id = ""
		c.roleIndex = -1
		if c.profile.Category == "default" {
			c.profile.Category = "crafter"
		}
	}
	c.draft = c.model.buildAgentForm(c.base, c.roleIndex == catMain)
	if c.roleIndex < 0 {
		name := ui.TextFieldState("agent-name", "Name", c.id, "Stable name used when delegating to this agent.")
		name.Disabled = c.id != ""
		name.Validator = func(value ui.FieldValue) []string {
			id := strings.TrimSpace(value.String())
			if !agentIDPattern.MatchString(id) {
				return []string{"Use 1-48 lowercase letters, digits, dashes or underscores; start with a letter."}
			}
			if _, exists := c.model.agents.AgentProfiles[id]; exists && id != c.id {
				return []string{"An agent already uses this name."}
			}
			return nil
		}
		fields := []*ui.FieldState{name, ui.SelectFieldState("agent-role", "Role", c.profile.Category, []ui.FormOption{{ID: "researcher", Label: "Researcher", Detail: "Read and investigate"}, {ID: "crafter", Label: "Crafter", Detail: "Implement and test"}, {ID: "aeron", Label: "Aeron", Detail: "Focused mechanical work"}}, "Role rules remain a hard limit on tool access.")}
		for _, field := range c.draft.Fields() {
			switch field.ID {
			case fieldConnection, fieldModel, fieldContextCompaction, "agent-description", "agent-instructions":
				fields = append(fields, field)
			}
		}
		c.draft = formModel(fields...)
	}
	mode := "Role defaults"
	if len(c.base.AllowedTools) > 0 || len(c.base.DeniedTools) > 0 {
		mode = "Custom selection"
	}
	c.draft.AddField(ui.SelectFieldState("agent-tool-mode", "Tool access", mode, []ui.FormOption{{ID: "Role defaults", Label: "Inherit role"}, {ID: "Custom selection", Label: "Custom"}}, "Custom selection can restrict the role, never bypass its safety policy. Reporting remains required for child agents."))
	allowed := make(map[string]bool)
	for _, tool := range agent.FilterTools(tools.AgentTools, c.base.AllowedTools, c.base.DeniedTools, c.profile.Category, c.roleIndex != catMain) {
		allowed[tool.Function.Name] = true
	}
	for _, tool := range tools.AgentTools {
		name := tool.Function.Name
		field := ui.CheckboxFieldState("agent-tool/"+name, strings.ReplaceAll(name, "_", " "), allowed[name], "")
		c.draft.AddField(field)
	}
	c.model.categories[catAgents].Form = c.draft
	c.draft.OnChanged(func(id string) {
		c.model.handleFieldChanged(catAgents, id)
		if id == "agent-tool-mode" || id == "agent-role" {
			c.refreshToolLimits()
		}
	})
	c.confirm = ""
	c.notice = ""
	c.refreshToolLimits()
	c.setPage("model")
	c.rebuildBar()
	c.owner.focusID(c.form.ID())
	c.form.FocusFirst()
}

func (c *agentCatalog) refreshToolLimits() {
	role := c.profile.Category
	if field := c.draft.Field("agent-role"); field != nil {
		role = field.Value.String()
	}
	allowed := map[string]bool{}
	var roleAllowed, roleDenied []string
	if c.roleIndex < 0 {
		configured := c.model.AppSettings().Agents.Categories[role]
		roleAllowed, roleDenied = configured.AllowedTools, configured.DeniedTools
	}
	for _, tool := range agent.FilterTools(tools.AgentTools, roleAllowed, roleDenied, role, c.roleIndex != catMain) {
		allowed[tool.Function.Name] = true
	}
	if c.roleIndex == catMain {
		delete(allowed, "write_report")
	}
	custom := c.draft.Field("agent-tool-mode").Value.String() == "Custom selection"
	for _, tool := range tools.AgentTools {
		name := tool.Function.Name
		field := c.draft.Field("agent-tool/" + name)
		required := name == "write_report" && c.roleIndex != catMain
		field.Disabled = !custom || !allowed[name] || required
		if !allowed[name] || !custom || required {
			field.Value = ui.BoolValue(allowed[name])
		}
		if !allowed[name] {
			field.Description = "Unavailable for this role"
		} else if required {
			field.Description = "Required reporting"
		} else {
			field.Description = ""
		}
	}
}

func (c *agentCatalog) setPage(page string) {
	c.page = page
	c.tabs.SetItems([]ui.TabItem{{ID: "model", Label: "Model"}, {ID: "instructions", Label: "Instructions"}, {ID: "tools", Label: "Tool Access"}}, page)
	var fields []*ui.FieldState
	for _, field := range c.draft.Fields() {
		isPrompt := field.ID == "agent-instructions" || field.ID == "agent-description"
		isTools := strings.HasPrefix(field.ID, "agent-tool")
		if page == "instructions" && isPrompt || page == "tools" && isTools || page == "model" && !isPrompt && !isTools {
			fields = append(fields, field)
		}
	}
	form := formModel(fields...)
	form.OnChanged(func(id string) {
		c.model.handleFieldChanged(catAgents, id)
		if id == "agent-tool-mode" || id == "agent-role" {
			c.refreshToolLimits()
		}
	})
	c.form.SetForm(form, "", "")
	c.form.Scroll().ScrollTo(0)
}

func (c *agentCatalog) dirty() bool {
	if c.draft == nil {
		return false
	}
	for _, field := range c.draft.Fields() {
		if !field.Disabled && !field.Value.Equal(field.Initial) {
			return true
		}
	}
	return false
}

func (c *agentCatalog) back() ui.EventResult {
	if c.dirty() {
		c.confirm = "discard"
		c.notice = "Discard this agent's unapplied edits?"
		c.rebuildBar()
		c.owner.focusID(c.bar.ID())
	} else {
		c.closeDraft()
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
}

func (c *agentCatalog) closeDraft() {
	c.draft = nil
	c.confirm = ""
	c.notice = ""
	c.model.categories[catAgents].Form = formModel()
	c.refreshRows()
	c.owner.focusID(c.table.ID())
}

func (c *agentCatalog) apply() bool {
	c.draft.ValidateAll()
	for _, field := range c.draft.Fields() {
		if field.HasErrors() {
			c.setPage("model")
			c.form.FocusField(field.ID)
			c.owner.focusID(c.form.ID())
			return false
		}
	}
	cfg := c.model.agentCategoryFromForm(c.draft, c.base)
	cfg.Inherit = ""
	if c.draft.Field("agent-tool-mode").Value.String() == "Role defaults" {
		cfg.AllowedTools = nil
		cfg.DeniedTools = nil
	} else {
		cfg.AllowedTools = nil
		cfg.DeniedTools = nil
		for _, tool := range tools.AgentTools {
			if c.draft.Field("agent-tool/" + tool.Function.Name).Value.Bool() {
				cfg.AllowedTools = append(cfg.AllowedTools, tool.Function.Name)
			} else {
				cfg.DeniedTools = append(cfg.DeniedTools, tool.Function.Name)
			}
		}
	}
	if c.roleIndex >= 0 {
		if c.roleIndex == catMain {
			c.model.agents.Enabled = ui.BoolFieldValue(c.draft.Field(fieldMultiAgent))
		}
		if c.model.agents.Categories == nil {
			c.model.agents.Categories = map[string]types.CategorySettings{}
		}
		c.model.agents.Categories[c.id] = cfg
		c.model.categories[c.roleIndex].Form = c.model.buildAgentForm(cfg, c.roleIndex == catMain)
		index := c.roleIndex
		c.model.categories[index].Form.OnChanged(func(id string) { c.model.handleFieldChanged(index, id) })
	} else {
		p := c.profile
		p.Category = c.draft.Field("agent-role").Value.String()
		p.AccountID = cfg.AccountID
		p.Model = cfg.Model
		p.Description = cfg.Description
		p.Instructions = cfg.Instructions
		p.AllowedTools = cfg.AllowedTools
		p.DeniedTools = cfg.DeniedTools
		p.ContextCompactionPercent = cfg.ContextCompactionPercent
		if c.model.agents.AgentProfiles == nil {
			c.model.agents.AgentProfiles = map[string]types.AgentProfileSettings{}
		}
		c.model.agents.AgentProfiles[strings.TrimSpace(c.draft.Field("agent-name").Value.String())] = p
	}
	c.closeDraft()
	c.notice = "Agent updated. Save settings to persist these changes."
	return true
}

func (c *agentCatalog) Children() []ui.Component {
	children := []ui.Component{c.filter, c.table, c.tabs, c.form}
	if !c.Bounds().Empty() {
		children = append(children, c.bar)
		if c.draft != nil && c.formScroll.Needed() {
			children = append(children, c.formScroll)
		} else if c.draft == nil && c.listScroll.Needed() {
			children = append(children, c.listScroll)
		}
	}
	return children
}
func (c *agentCatalog) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (c *agentCatalog) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	w, h := r.W, r.H
	c.rebuildBar()
	if r.Empty() {
		for _, child := range c.Children() {
			child.Layout(ctx, ui.Rect{})
		}
		return
	}
	if c.draft != nil {
		c.filter.Layout(ctx, ui.Rect{})
		c.table.Layout(ctx, ui.Rect{})
		c.listScroll.Layout(ctx, ui.Rect{})
		c.tabs.Layout(ctx, ui.Rect{Y: 2, W: w, H: ui.StripHeight})
		c.form.SetHideDescriptions(w < 48)
		c.form.Layout(ctx, ui.Rect{Y: 5, W: max(1, w-2), H: max(1, h-8)})
		c.formScroll.Layout(ctx, ui.Rect{X: w - 2, Y: 5, W: 2, H: max(1, h-8)})
		if !c.formScroll.Needed() {
			c.formScroll.Layout(ctx, ui.Rect{})
		}
		c.bar.Layout(ctx, ui.Rect{Y: h - 1, W: w, H: 1})
	} else {
		c.tabs.Layout(ctx, ui.Rect{})
		c.form.Layout(ctx, ui.Rect{})
		c.formScroll.Layout(ctx, ui.Rect{})
		c.filter.Layout(ctx, ui.Rect{Y: 2, W: w, H: 1})
		c.table.SetColumns([]ui.TableColumn{{ID: "name", Label: "Agent", Track: ui.FractionGridTrack(2)}, {ID: "role", Label: "Role", Track: ui.FractionGridTrack(2)}, {ID: "model", Label: "Model", Track: ui.FractionGridTrack(3)}})
		c.table.Layout(ctx, ui.Rect{Y: 4, W: max(1, w-2), H: max(1, h-8)})
		c.listScroll.Layout(ctx, ui.Rect{X: w - 2, Y: 5, W: 2, H: max(1, h-9)})
		if !c.listScroll.Needed() {
			c.listScroll.Layout(ctx, ui.Rect{})
		}
		c.bar.Layout(ctx, ui.Rect{Y: h - 3, W: w, H: 1})
	}
}
func (c *agentCatalog) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := c.Bounds()
	if r.Empty() {
		return
	}
	title := c.owner.PaneTitle()
	buf.WriteString(ox+r.X, oy+r.Y, rendering.TruncateVisual(title, r.W, "…"), c.owner.tokens.Color(ui.ColorAccent), c.owner.tokens.Color(ui.ColorPopupBg), cell.AttrBold)
	if c.notice != "" {
		buf.WriteString(ox+r.X, oy+r.Y+r.H-4, rendering.TruncateVisual(c.notice, r.W, "…"), c.owner.tokens.Color(ui.ColorMuted), c.owner.tokens.Color(ui.ColorPopupBg), 0)
	}
}
func (c *agentCatalog) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && c.draft != nil {
		return c.back()
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		c.owner.focusID(c.bar.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	return ui.EventResult{}
}
func (c *agentCatalog) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	row, _ := c.table.Selected()
	switch action.ID {
	case "form.changed":
	case "agents.add":
		c.begin("", false)
	case "agents.edit":
		if row.ID != "" {
			c.begin(row.ID, false)
		}
	case "agents.duplicate":
		if row.ID != "" {
			c.begin(row.ID, true)
		}
	case "agents.back":
		return c.back()
	case "form.cancel":
		return c.back()
	case "agents.discard":
		c.closeDraft()
		if next := c.model.nextScope; next != "" {
			c.model.nextScope = ""
			result.Cmd = func() tea.Msg { return ScopeMsg{Scope: next} }
		}
	case "agents.stay":
		c.confirm = ""
		c.notice = ""
		c.model.nextScope = ""
		c.rebuildBar()
		if c.draft != nil {
			c.owner.focusID(c.form.ID())
		} else {
			c.owner.focusID(c.table.ID())
		}
	case "agents.apply":
		c.apply()
	case "agents.remove":
		if strings.HasPrefix(row.ID, "profile/") {
			c.id = strings.TrimPrefix(row.ID, "profile/")
			c.confirm = "remove"
			c.notice = fmt.Sprintf("Remove %s? Running agents are not stopped.", c.id)
			c.rebuildBar()
			c.owner.focusID(c.bar.ID())
		}
	case "agents.remove-confirm":
		if c.confirm == "remove" {
			delete(c.model.agents.AgentProfiles, c.id)
			c.confirm = ""
			c.notice = "Agent removed from this settings draft."
			c.refreshRows()
		}
	case "tabs.activate", "tabs.enter":
		if c.draft != nil {
			page, _ := action.Payload.(string)
			c.setPage(page)
			if action.ID == "tabs.enter" {
				c.owner.focusID(c.form.ID())
			}
		}
	case "form.select.open":
		id, _ := action.Payload.(string)
		result.Cmd = c.model.OpenSelect(id)
	case "form.exit.forward":
		c.owner.focusID(c.bar.ID())
	case "form.exit.backward":
		c.owner.focusID(c.tabs.ID())
	case "actionbar.exit.forward", "actionbar.exit.backward":
		if c.draft != nil {
			c.owner.focusID(c.tabs.ID())
		} else {
			c.owner.FocusActions()
		}
	default:
		return ui.EventResult{}
	}
	return result
}
func (c *agentCatalog) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Agents"}
}

func (c *agentCatalog) SetTokens(tokens *ui.TokenSet) {
	c.filter.SetTokens(tokens)
	c.table.SetTokens(tokens)
	c.tabs.SetTokens(tokens)
	c.form.SetTokens(tokens)
	c.bar.SetTokens(tokens)
	c.listScroll.SetTokens(tokens)
	c.formScroll.SetTokens(tokens)
}

type agentCatalogFilter struct {
	*ui.FilterField
	owner *agentCatalog
}

func (f *agentCatalogFilter) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && (evt.Key.Code == tea.KeyDown || evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeyTab) {
		f.owner.owner.focusID(f.owner.table.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	before := f.Value()
	result := f.FilterField.Handle(ctx, evt)
	if before != f.Value() {
		f.owner.refreshRows()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}
