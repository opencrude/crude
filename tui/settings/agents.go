package settings

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/provider"
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
}{{"default", "Main Agent", catMain}, {"researcher", "Researcher", catResearcher}, {"crafter", "Crafter", catCrafter}, {"aeron", "Aeron", catAeron}, {"memory", "Memory Agent", catMemory}, {"auto-review", "Auto Review Agent", catAutoReviewPolicy}}

type agentCatalog struct {
	helpRect ui.Rect
	ui.Base
	owner                     *WorkspaceSettingsSurface
	model                     *Model
	filter                    *agentCatalogFilter
	toolFilter                *ui.FilterField
	capabilitySeen            uint64
	table                     *ui.Table
	tabs                      *ui.Tabs
	form                      *ui.Form
	bar                       *ui.ActionBar
	more                      *ui.ActionBar
	stackActions              bool
	listScroll, formScroll    *ui.ScrollbarControl
	draft                     *ui.FormModel
	toolPage                  *ui.FormModel
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
	c.toolFilter = ui.NewFilterField("settings/agents/tool-search", t)
	c.toolFilter.SetPlaceholder("Search built-in tools, MCP and skills")
	c.toolFilter.SetPrompt(ui.SearchGlyphEnd(t))
	c.toolFilter.SetPromptPlacement(ui.PromptEnd)
	c.toolFilter.SetSubmitAction("agents.tool-search.enter")
	c.toolFilter.OnChanged(func(string) {
		if c.draft != nil && c.page == "tools" {
			c.setPage("tools")
		}
	})
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
	c.more = ui.NewActionBar("settings/agents/more-actions", t)
	c.more.SetAlignEnd(false)
	c.more.SetExitActions("agents.more.forward", "agents.more.backward")
	c.listScroll = ui.NewScrollbarControl("settings/agents/scroll", c.table.Scroll(), t)
	c.formScroll = ui.NewScrollbarControl("settings/agents/form-scroll", c.form.Scroll(), t)
	c.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.formScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.refreshRows()
	return c
}

func (c *agentCatalog) syncModel() {
	if c.model != c.owner.model {
		c.model = c.owner.model
		c.draft = nil
		c.toolPage = nil
		c.confirm = ""
		c.refreshRows()
	}
	if c.draft != nil {
		if c.capabilitySeen != c.model.capabilityRevision {
			c.syncCapabilityFields()
			c.refreshToolLimits()
			if c.page == "tools" {
				c.syncToolPage()
			}
			c.capabilitySeen = c.model.capabilityRevision
		}
		// Async model results update the draft in place. Refresh retained
		// controls without rebuilding the page or closing its open dropdown.
		for _, field := range c.draft.Fields() {
			if control := c.form.Field(field.ID); control != nil {
				control.Sync()
			}
		}
		c.form.Layout(ui.LayoutContext{}, c.form.Bounds())
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
	add := func(id, name, role, model, description string, enabled bool) {
		if query != "" && !strings.Contains(strings.ToLower(name+" "+role+" "+model+" "+description), query) {
			return
		}
		if model == "" {
			model = "Inherit role"
		}
		if !enabled {
			role += " (disabled)"
		}
		rows = append(rows, ui.TableRow{ID: id, Cells: []ui.TableCell{{Text: name}, {Text: role, Role: ui.ColorMuted}, {Text: model, Role: ui.ColorMuted}}})
	}
	settings := c.model.AppSettings().Agents
	for _, role := range agentRoles {
		cfg := settings.Categories[role.key]
		if role.key == "memory" {
			cfg = c.model.memoryAgent
		} else if role.key == "auto-review" {
			auto := c.model.autoReviewer
			add("role/auto-review", role.label, "Built-in", auto.Model, "Reviews eligible root and child permission requests", auto.Enabled == "on")
			continue
		}
		add("role/"+role.key, role.label, "Built-in", cfg.Model, cfg.Description, cfg.Enabled == nil || *cfg.Enabled)
	}
	keys := make([]string, 0, len(settings.AgentProfiles))
	for key := range settings.AgentProfiles {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		p := settings.AgentProfiles[key]
		add("profile/"+key, key, p.Category, p.Model, p.Description, p.Enabled == nil || *p.Enabled)
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
	case "reset":
		buttons = []ui.ActionBarButton{{Label: "Restore default", Action: "agents.reset-confirm", Variant: ui.VariantDanger, Enabled: true}, {Label: "Keep changes", Action: "agents.stay", Enabled: true}}
	default:
		if c.draft != nil {
			buttons = []ui.ActionBarButton{{Label: "Apply agent", Action: "agents.apply", Variant: ui.VariantPrimary, Enabled: true}, {Label: "Back", Action: "agents.back", Enabled: true}}
		} else {
			row, ok := c.table.Selected()
			buttons = []ui.ActionBarButton{{Label: "Add", Action: "agents.add", Variant: ui.VariantPrimary, Enabled: true}, {Label: "Edit", Action: "agents.edit", Enabled: ok}, {Label: "Clone", Action: "agents.duplicate", Enabled: ok}, {Label: "Reset", Action: "agents.reset", Enabled: ok}, {Label: "Remove", Action: "agents.remove", Variant: ui.VariantDanger, Enabled: ok && strings.HasPrefix(row.ID, "profile/")}}
		}
	}
	c.stackActions = c.draft == nil && c.confirm == "" && c.Bounds().W > 0 && c.barWidth(buttons) > c.Bounds().W
	if c.stackActions {
		c.bar.SetButtons(buttons[:3]...)
		c.more.SetButtons(buttons[3:]...)
	} else {
		c.bar.SetButtons(buttons...)
		c.more.SetButtons()
	}
}

func (c *agentCatalog) barWidth(buttons []ui.ActionBarButton) int {
	width := 0
	for i, button := range buttons {
		if i > 0 {
			width += c.owner.tokens.ButtonGap()
		}
		width += rendering.VisibleWidth(button.Label) + 4
	}
	return width
}

func (c *agentCatalog) begin(key string, duplicate bool) {
	c.toolPage = nil
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
			switch role.key {
			case "memory":
				c.base = c.model.memoryAgent
			case "auto-review":
				auto := c.model.autoReviewer
				enabled := auto.Enabled == "on"
				c.base = types.CategorySettings{Enabled: &enabled, AccountID: auto.AccountID, Provider: auto.Provider, Model: auto.Model,
					Description: "Reviews eligible root and child permission requests", Instructions: auto.Instructions}
			}
			c.profile.Category = role.key
		}
	}
	if strings.HasPrefix(key, "profile/") {
		c.id = strings.TrimPrefix(key, "profile/")
		c.profile = settings.AgentProfiles[c.id]
		p := c.profile
		c.base = types.CategorySettings{
			Enabled: p.Enabled, AccountID: p.AccountID, Model: p.Model, Description: p.Description, Instructions: p.Instructions,
			ThinkingEffort: p.ThinkingEffort, ModelContextWindow: p.ModelContextWindow, ModelMaxOutput: p.ModelMaxOutput,
			ModelModalities: p.ModelModalities, ModelTemperature: p.ModelTemperature, ModelTopP: p.ModelTopP,
			AllowedTools: p.AllowedTools, DeniedTools: p.DeniedTools, SkillModes: p.SkillModes, ContextCompactionPercent: p.ContextCompactionPercent,
		}
	}
	if duplicate {
		c.id = ""
		c.roleIndex = -1
		if c.profile.Category == "default" {
			c.profile.Category = "crafter"
		}
	}
	if c.roleIndex == catAutoReviewPolicy {
		c.model.autoReviewer = autoPermissionFromForm(c.model.autoReviewer, c.model.categories[catAutoReviewPolicy].Form)
		c.draft = c.model.buildAutoReviewerForm()
	} else {
		c.draft = c.model.buildAgentForm(c.base, c.roleIndex == catMain)
	}
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
		fields = append(fields, c.draft.Fields()...)
		fields = append(fields, ui.BoolFieldState("agent-inherit-modalities", "Inherit modalities", c.profile.ModelModalities == nil, "Use the role's media capabilities."))
		ownership := ui.TextAreaFieldState("agent-file-ownership", "File ownership", strings.Join(c.profile.FileOwnership, "\n"), "Exclusive paths, one per line.")
		ownership.Rows = 3
		fields = append(fields, ownership)
		c.draft = formModel(fields...)
	}
	enabled := c.base.Enabled == nil || *c.base.Enabled
	availability := ui.BoolFieldState("agent-enabled", "Enabled", enabled, "Allow new runs of this agent.")
	if c.roleIndex != catMain {
		c.draft.AddField(availability)
	}
	mode := "Role defaults"
	if len(c.base.AllowedTools) > 0 || len(c.base.DeniedTools) > 0 {
		mode = "Custom selection"
	}
	if c.roleIndex != catAutoReviewPolicy {
		c.draft.AddField(ui.SelectFieldState("agent-tool-mode", "Tool access", mode, []ui.FormOption{{ID: "Role defaults", Label: "Inherit role"}, {ID: "Custom selection", Label: "Custom"}}, "Choose the available tools."))
		c.syncCapabilityFields()
	}
	c.model.categories[catAgents].Form = c.draft
	c.draft.OnChanged(c.fieldChanged)
	if c.roleIndex != catAutoReviewPolicy {
		c.model.autofillCapabilities(catAgents)
	}
	c.confirm = ""
	c.notice = ""
	c.toolFilter.SetValue("")
	c.refreshToolLimits()
	c.setPage("model")
	c.rebuildBar()
	c.owner.focusID(c.form.ID())
	c.form.FocusFirst()
}

func (c *agentCatalog) fieldChanged(id string) {
	if strings.HasPrefix(id, fieldModalityPrefix) {
		c.draft.SetValue("agent-inherit-modalities", ui.StringValue("off"))
	}
	if id == "agent-inherit-modalities" {
		inherit := ui.BoolFieldValue(c.draft.Field(id))
		for _, modality := range provider.KnownModalities() {
			if field := c.draft.Field(fieldModalityPrefix + modality); field != nil {
				field.Inherited = inherit
			}
		}
		c.model.autofillCapabilities(catAgents)
	}
	c.model.handleFieldChanged(catAgents, id)
	if c.roleIndex == catAutoReviewPolicy {
		validateAutoReviewPolicyForm(c.draft)
	}
	if id == "agent-tool-mode" || id == "agent-role" {
		c.refreshToolLimits()
	}
}

func (c *agentCatalog) refreshToolLimits() {
	if c.roleIndex == catMemory || c.roleIndex == catAutoReviewPolicy {
		return // Neither background agent has a tool execution path.
	}
	c.refreshCapabilityLimits()
}

func (c *agentCatalog) setPage(page string) {
	c.page = page
	thirdLabel := "Tool Access"
	if c.roleIndex == catAutoReviewPolicy {
		thirdLabel = "Review"
	}
	c.tabs.SetItems([]ui.TabItem{{ID: "model", Label: "Model"}, {ID: "instructions", Label: "Instructions"}, {ID: "tools", Label: thirdLabel}}, page)
	if page == "tools" && c.roleIndex == catMemory {
		label, detail := "Background jobs", "No tools; memory completions use the model and instructions."
		c.form.SetForm(formModel(&ui.FieldState{ID: "agent-no-tools", Label: label, Type: ui.FieldText, Value: ui.StringValue(detail), ReadOnly: true}), "", "")
		c.form.Scroll().ScrollTo(0)
		return
	}
	if page == "tools" && c.roleIndex != catAutoReviewPolicy {
		c.syncToolPage()
		c.form.Scroll().ScrollTo(0)
		return
	}
	var fields []*ui.FieldState
	for _, field := range c.draft.Fields() {
		isPrompt := field.ID == "agent-instructions" || field.ID == "agent-description"
		isReview := c.roleIndex == catAutoReviewPolicy && strings.HasPrefix(field.ID, "auto-permission-")
		isTools := isReview || strings.HasPrefix(field.ID, "agent-tool") || strings.HasPrefix(field.ID, "agent-skill") || strings.HasPrefix(field.ID, "agent-access") || field.ID == "agent-file-ownership"
		if page == "instructions" && isPrompt || page == "tools" && isTools || page == "model" && !isPrompt && !isTools {
			fields = append(fields, field)
		}
	}
	form := formModel(fields...)
	form.OnChanged(c.fieldChanged)
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
	c.toolPage = nil
	c.confirm = ""
	c.notice = ""
	c.model.categories[catAgents].Form = formModel()
	c.refreshRows()
	c.owner.focusID(c.table.ID())
}

func (c *agentCatalog) apply() bool {
	c.draft.ValidateAll()
	if c.roleIndex == catAutoReviewPolicy {
		validateAutoReviewPolicyForm(c.draft)
	}
	for _, field := range c.draft.Fields() {
		if field.HasErrors() {
			page := "model"
			if field.ID == "agent-instructions" || field.ID == "agent-description" {
				page = "instructions"
			} else if c.roleIndex == catAutoReviewPolicy && strings.HasPrefix(field.ID, "auto-permission-") {
				page = "tools"
			}
			c.setPage(page)
			c.form.FocusField(field.ID)
			c.owner.focusID(c.form.ID())
			return false
		}
	}
	if c.roleIndex == catAutoReviewPolicy {
		enabled := ui.BoolFieldValue(c.draft.Field("agent-enabled"))
		accountID := c.draft.Field(fieldConnection).Value.String()
		model := c.draft.Field(fieldModel).Value.String()
		if enabled && (accountID == "" || model == "") {
			fieldID := fieldConnection
			if accountID != "" {
				fieldID = fieldModel
			}
			c.draft.Field(fieldID).Errors = []string{"Required while Auto Review is enabled."}
			c.setPage("model")
			c.form.FocusField(fieldID)
			c.owner.focusID(c.form.ID())
			return false
		}
		format := c.draft.Field(fieldAutoPermFormat).Value.String()
		if accountID != "" && (format == types.AutoPermissionFormatJSONSchema || format == types.AutoPermissionFormatJSONObject) {
			account, ok := c.model.accounts[accountID]
			standard := ""
			if ok {
				standard = account.Connection.APIStandard
			}
			if standard == "" && ok {
				standard = provider.DefaultStandardID(account.ProviderID)
			}
			if standard != "openai" {
				c.draft.Field(fieldAutoPermFormat).Errors = []string{"Explicit JSON response formats require an OpenAI Chat-compatible account; use Auto or Prompt only."}
				c.setPage("tools")
				c.form.FocusField(fieldAutoPermFormat)
				c.owner.focusID(c.form.ID())
				return false
			}
		}
		auto := c.model.autoReviewer
		auto.Enabled = onOff(enabled)
		auto.AccountID = accountID
		auto.Provider = c.model.accounts[accountID].ProviderID
		auto.Model = model
		auto.Instructions = strings.TrimSpace(c.draft.Field("agent-instructions").Value.String())
		auto = autoPermissionFromForm(auto, c.draft)
		c.model.autoReviewer = auto
		policyForm := c.model.buildAutoReviewPolicyForm(auto)
		policyForm.OnChanged(func(id string) { c.model.handleFieldChanged(catAutoReviewPolicy, id) })
		c.model.categories[catAutoReviewPolicy].Form = policyForm
		c.closeDraft()
		c.notice = "Auto Review Agent updated. Save settings to persist these changes."
		return true
	}
	cfg := c.model.agentCategoryFromForm(c.draft, c.base)
	cfg.Inherit = ""
	if c.roleIndex == catMain {
		cfg.Enabled = nil
	} else {
		enabled := ui.BoolFieldValue(c.draft.Field("agent-enabled"))
		cfg.Enabled = &enabled
	}
	if c.draft.Field("agent-tool-mode").Value.String() == "Role defaults" {
		cfg.AllowedTools = nil
		cfg.DeniedTools = nil
	} else {
		cfg.AllowedTools = nil
		cfg.DeniedTools = nil
		for _, field := range c.draft.Fields() {
			if !strings.HasPrefix(field.ID, "agent-tool/") {
				continue
			}
			name := strings.TrimPrefix(field.ID, "agent-tool/")
			if field.Value.Bool() {
				cfg.AllowedTools = append(cfg.AllowedTools, name)
			} else {
				cfg.DeniedTools = append(cfg.DeniedTools, name)
			}
		}
	}
	cfg.SkillModes = c.selectedSkillModes()
	if c.roleIndex >= 0 {
		if c.roleIndex == catMemory {
			c.model.memoryAgent = cfg
			c.closeDraft()
			c.notice = "Memory agent updated. Save settings to persist these changes."
			return true
		}
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
		p.Enabled = cfg.Enabled
		p.AccountID = cfg.AccountID
		p.Model = cfg.Model
		p.Description = cfg.Description
		p.Instructions = cfg.Instructions
		p.AllowedTools = cfg.AllowedTools
		p.DeniedTools = cfg.DeniedTools
		p.SkillModes = cfg.SkillModes
		p.ContextCompactionPercent = cfg.ContextCompactionPercent
		p.ThinkingEffort = cfg.ThinkingEffort
		p.ModelContextWindow = cfg.ModelContextWindow
		p.ModelMaxOutput = cfg.ModelMaxOutput
		if ui.BoolFieldValue(c.draft.Field("agent-inherit-modalities")) {
			p.ModelModalities = nil
		} else {
			p.ModelModalities = append(make([]string, 0, len(cfg.ModelModalities)), cfg.ModelModalities...)
		}
		p.ModelTemperature = cfg.ModelTemperature
		p.ModelTopP = cfg.ModelTopP
		p.FileOwnership = strings.Fields(c.draft.Field("agent-file-ownership").Value.String())
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
	children := []ui.Component{c.filter, c.toolFilter, c.table, c.tabs, c.form}
	if !c.Bounds().Empty() {
		children = append(children, c.bar)
		if c.stackActions {
			children = append(children, c.more)
		}
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
		c.more.Layout(ctx, ui.Rect{})
		return
	}
	if c.draft != nil {
		c.filter.Layout(ctx, ui.Rect{})
		c.table.Layout(ctx, ui.Rect{})
		c.listScroll.Layout(ctx, ui.Rect{})
		c.tabs.Layout(ctx, ui.Rect{Y: 0, W: w, H: ui.StripHeight})
		c.toolFilter.Layout(ctx, ui.Rect{})
		formY := ui.StripHeight + 1
		if c.page == "tools" && c.roleIndex != catMemory && c.roleIndex != catAutoReviewPolicy {
			c.toolFilter.Layout(ctx, ui.Rect{Y: ui.StripHeight, W: w, H: 1})
			formY = ui.StripHeight + 2
		}
		// Rows from the bottom: action bar, notice, help strip, breathing row.
		c.form.SetHideDescriptions(true)
		c.form.SetFieldGap(0)
		helpLines := helpStripLines(h - formY - 2)
		helpH := helpLines
		if helpLines > 1 {
			helpH++
		}
		formH := max(1, h-formY-2-helpH)
		c.helpRect = ui.Rect{Y: h - 2 - helpLines, W: w, H: helpLines}
		c.form.Layout(ctx, ui.Rect{Y: formY, W: max(1, w-2), H: formH})
		// Register only the painted stroke so the reserved gutter column
		// between the form and the bar stays inert for pointer input.
		c.formScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, Y: formY, W: ui.ScrollbarWidth, H: formH})
		if !c.formScroll.Needed() {
			c.formScroll.Layout(ctx, ui.Rect{})
		}
		c.bar.Layout(ctx, ui.Rect{Y: h - 1, W: w, H: 1})
		c.more.Layout(ctx, ui.Rect{})
	} else {
		c.helpRect = ui.Rect{}
		c.toolFilter.Layout(ctx, ui.Rect{})
		c.tabs.Layout(ctx, ui.Rect{})
		c.form.Layout(ctx, ui.Rect{})
		c.formScroll.Layout(ctx, ui.Rect{})
		c.filter.Layout(ctx, ui.Rect{Y: 0, W: w, H: 1})
		c.table.SetColumns([]ui.TableColumn{{ID: "name", Label: "Agent", Track: ui.FractionGridTrack(2)}, {ID: "role", Label: "Role", Track: ui.FractionGridTrack(2)}, {ID: "model", Label: "Model", Track: ui.FractionGridTrack(3)}})
		firstAction := h - 1
		if c.stackActions {
			firstAction--
		}
		c.table.Layout(ctx, ui.Rect{Y: 2, W: max(1, w-2), H: max(1, firstAction-4)})
		c.listScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, Y: 3, W: ui.ScrollbarWidth, H: max(1, firstAction-5)})
		if !c.listScroll.Needed() {
			c.listScroll.Layout(ctx, ui.Rect{})
		}
		c.bar.Layout(ctx, ui.Rect{Y: firstAction, W: w, H: 1})
		if c.stackActions {
			c.more.Layout(ctx, ui.Rect{Y: h - 1, W: w, H: 1})
		} else {
			c.more.Layout(ctx, ui.Rect{})
		}
	}
}
func (c *agentCatalog) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := c.Bounds()
	if r.Empty() {
		return
	}
	bg := c.owner.tokens.Color(ui.ColorPopupBg)
	if c.draft != nil && !c.helpRect.Empty() {
		paintFieldHelp(buf, ox+r.X+c.helpRect.X, oy+r.Y+c.helpRect.Y, c.helpRect.W, c.helpRect.H, c.form.HelpField(), c.owner.tokens, bg)
	}
	if c.notice != "" {
		row := r.H - 2
		if c.stackActions {
			row--
		}
		buf.WriteString(ox+r.X, oy+r.Y+row, rendering.TruncateVisual(c.notice, r.W, "…"), c.owner.tokens.Color(ui.ColorMuted), c.owner.tokens.Color(ui.ColorPopupBg), 0)
	}
}
func (c *agentCatalog) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if c.owner != nil && c.owner.model != nil && c.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
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
	if c.owner != nil && c.owner.model != nil && c.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	row, _ := c.table.Selected()
	switch action.ID {
	case "agents.tool-search.enter":
		c.owner.focusID(c.form.ID())
		c.form.FocusFirst()
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
		if next := c.owner.capCatalog.nextOpen; next != nil {
			c.owner.capCatalog.nextOpen = nil
			return c.owner.OpenCapabilities(next.kind, next.query, next.id)
		}
		if next := c.model.nextScope; next != "" {
			c.model.nextScope = ""
			result.Cmd = func() tea.Msg { return ScopeMsg{Scope: next} }
		}
	case "agents.stay":
		c.confirm = ""
		c.notice = ""
		c.model.nextScope = ""
		c.owner.capCatalog.nextOpen = nil
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
	case "agents.reset":
		if row.ID != "" {
			c.id = row.ID
			c.confirm = "reset"
			c.notice = "Restore shipped defaults for this agent? Save settings to persist."
			c.rebuildBar()
			c.owner.focusID(c.bar.ID())
		}
	case "agents.reset-confirm":
		if c.confirm == "reset" {
			if key := strings.TrimPrefix(c.id, "role/"); key != c.id {
				switch key {
				case "memory":
					c.model.memoryAgent = types.DefaultAgentRoleCategories()["memory"]
				case "auto-review":
					c.model.autoReviewer.Enabled = "off"
					c.model.autoReviewer.Provider = ""
					c.model.autoReviewer.AccountID = ""
					c.model.autoReviewer.Model = ""
					c.model.autoReviewer.Instructions = ""
				default:
					cfg := types.DefaultAgentRoleCategories()[key]
					if key == "default" {
						current := c.model.AppSettings().Agents.Categories[key]
						cfg.AccountID, cfg.Provider, cfg.Model, cfg.ThinkingEffort = current.AccountID, current.Provider, current.Model, current.ThinkingEffort
					}
					c.model.agents.Categories[key] = cfg
					for _, role := range agentRoles {
						if role.key == key {
							form := c.model.buildAgentForm(cfg, role.index == catMain)
							c.model.categories[role.index].Form = form
							index := role.index
							form.OnChanged(func(id string) { c.model.handleFieldChanged(index, id) })
						}
					}
					if key == "default" {
						c.model.agents.Enabled = true
					}
				}
			} else if key := strings.TrimPrefix(c.id, "profile/"); key != c.id {
				if original, exists := types.DefaultNamedAgents()[key]; exists {
					c.model.agents.AgentProfiles[key] = original
				} else if current, exists := c.model.agents.AgentProfiles[key]; exists {
					c.model.agents.AgentProfiles[key] = types.AgentProfileSettings{Category: current.Category, Description: current.Description}
				}
			}
			c.confirm = ""
			c.notice = "Shipped defaults restored in this draft. Save to persist."
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
		} else if c.stackActions && action.ID == "actionbar.exit.forward" {
			c.owner.focusID(c.more.ID())
		} else {
			c.owner.FocusActions()
		}
	case "agents.more.backward":
		c.owner.focusID(c.bar.ID())
	case "agents.more.forward":
		c.owner.FocusActions()
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
	c.toolFilter.SetTokens(tokens)
	c.table.SetTokens(tokens)
	c.tabs.SetTokens(tokens)
	c.form.SetTokens(tokens)
	c.bar.SetTokens(tokens)
	c.more.SetTokens(tokens)
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
