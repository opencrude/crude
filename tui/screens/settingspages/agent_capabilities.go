package settingspages

import (
	"maps"
	"slices"
	"strings"

	"crude/agent/safety"
	"crude/agent/tools"
	"crude/agent/turnloop"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func (c *agentCatalog) capabilityRole() string {
	if field := c.draft.Field("agent-role"); field != nil {
		return field.Value.String()
	}
	return c.profile.Category
}

func (c *agentCatalog) syncToolPage() {
	var fields []*ui.FieldState
	query := strings.ToLower(c.toolFilter.Value())
	for _, field := range c.draft.Fields() {
		isTool := strings.HasPrefix(field.ID, "agent-tool") || strings.HasPrefix(field.ID, "agent-skill") || field.ID == "agent-file-ownership"
		if !isTool {
			continue
		}
		if query != "" && (strings.HasPrefix(field.ID, "agent-tool/") || strings.HasPrefix(field.ID, "agent-skill/")) && !strings.Contains(strings.ToLower(field.ID+" "+field.Label+" "+field.Description), query) {
			continue
		}
		fields = append(fields, field)
	}
	same := c.toolPage != nil && c.toolPage.FieldCount() == len(fields)
	if same {
		for index, field := range fields {
			if c.toolPage.Fields()[index] != field {
				same = false
				break
			}
		}
	}
	if !same {
		c.toolPage = formModel(fields...)
		c.toolPage.OnChanged(c.fieldChanged)
	}
	c.form.SetForm(c.toolPage, "", "")
}

func (c *agentCatalog) syncCapabilityFields() {
	if c.draft == nil || c.roleIndex == catMemory || c.roleIndex == catAutoReviewPolicy {
		return
	}
	addGroup := func(id, label string) {
		if c.draft.Field(id) == nil {
			c.draft.AddField(ui.SeparatorFieldState(id, label))
		}
	}
	nativeAllowed := make(map[string]bool)
	for _, tool := range turnloop.FilterTools(tools.AgentTools, c.base.AllowedTools, c.base.DeniedTools, c.capabilityRole(), c.roleIndex != catMain) {
		nativeAllowed[tool.Function.Name] = true
	}
	add := func(name, label string, selected bool) {
		if c.draft.Field("agent-tool/"+name) != nil {
			return
		}
		if slices.Contains(c.base.AllowedTools, name) {
			selected = true
		}
		if slices.Contains(c.base.DeniedTools, name) {
			selected = false
		}
		c.draft.AddField(ui.CheckboxFieldState("agent-tool/"+name, label, selected, ""))
	}
	addGroup("agent-tool-group/builtin", "Built-in tools")
	for _, tool := range tools.AgentTools {
		add(tool.Function.Name, strings.ReplaceAll(tool.Function.Name, "_", " "), nativeAllowed[tool.Function.Name])
	}
	for _, connection := range c.connectionTools() {
		addGroup("agent-tool-group/mcp/"+connection.ID, "MCP · "+connection.ID)
		for _, tool := range connection.Tools {
			add(tool.Name, tool.OriginalName+" · "+connection.ID, len(c.base.AllowedTools) == 0 || slices.Contains(c.base.AllowedTools, tool.Name))
		}
	}
	for _, name := range append(slices.Clone(c.base.AllowedTools), c.base.DeniedTools...) {
		if c.draft.Field("agent-tool/"+name) == nil {
			addGroup("agent-tool-group/unavailable", "Retained unavailable selections")
			add(name, name, slices.Contains(c.base.AllowedTools, name))
		}
	}
	addGroup("agent-skill-group", "Skills · invocation preferences")
	skills := make(map[string]string)
	for _, entry := range c.model.capabilitySnapshot.Entries {
		if entry.Kind == captypes.KindSkill {
			skills[entry.ID] = entry.Name
		}
	}
	for id, skill := range c.model.capabilities.Skills {
		skills[id] = skill.Name
	}
	for id := range c.base.SkillModes {
		if skills[id] == "" {
			skills[id] = id
		}
	}
	identities := make([]string, 0, len(skills))
	for id := range skills {
		identities = append(identities, id)
	}
	slices.Sort(identities)
	for _, id := range identities {
		if c.draft.Field("agent-skill/"+id) != nil {
			continue
		}
		mode := mapDefault(c.base.SkillModes[id], "inherit")
		field := ui.SelectFieldState("agent-skill/"+id, skills[id]+" · "+id, mode, []ui.FormOption{{ID: "inherit", Label: "Inherit"}, {ID: "automatic", Label: "Automatic"}, {ID: "manual", Label: "Manual only"}, {ID: "disabled", Label: "Disabled"}}, "Invocation preference is separate from script execution permissions.")
		c.draft.AddField(field)
	}
}

func (c *agentCatalog) refreshCapabilityLimits() {
	role := c.capabilityRole()
	var roleAllowed, roleDenied []string
	if c.roleIndex < 0 {
		configured := c.model.AppSettings().Agents.Categories[role]
		roleAllowed, roleDenied = configured.AllowedTools, configured.DeniedTools
	}
	native := make(map[string]bool)
	definitions := append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.CollaborationTools...)
	for _, tool := range turnloop.FilterTools(definitions, roleAllowed, roleDenied, role, c.roleIndex != catMain) {
		native[tool.Function.Name] = true
	}
	policy := turnloop.ComposePolicy(safety.DefaultPolicy(), c.base.Policy, role)
	dynamic := make(map[string]captypes.Tool)
	connections := make(map[string]captypes.ConnectionState)
	for _, connection := range c.connectionTools() {
		connections[connection.ID] = connection
		for _, tool := range connection.Tools {
			dynamic[tool.Name] = tool
		}
	}
	custom := c.draft.Field("agent-tool-mode").Value.String() == "Custom selection"
	for _, field := range c.draft.Fields() {
		if strings.HasPrefix(field.ID, "agent-tool/") {
			name := strings.TrimPrefix(field.ID, "agent-tool/")
			reason := ""
			allowed := native[name]
			if tool, exists := dynamic[name]; exists {
				allowed = turnloop.PolicyAllowsMetadata(policy, tools.ToolMetadata{Name: tool.Name, Effect: tool.Effect, Scope: tool.Scope, Fingerprint: tool.Fingerprint, Dynamic: true})
				if !allowed {
					reason = "Denied by " + role + " role or unreviewed effect"
				}
				if slices.Contains(roleDenied, name) || len(roleAllowed) > 0 && !slices.Contains(roleAllowed, name) {
					allowed = false
					reason = "Excluded by inherited role selection"
				}
				if tool.Reason != "" {
					allowed = false
					reason = tool.Reason
				}
				if !tool.Selected {
					allowed = false
					if reason == "" {
						reason = "Needs an explicit effect, scope and fingerprint grant"
					}
				}
				if connections[tool.ConnectionID].State != "connected" {
					allowed = false
					reason = "Connection stopped; selection intent is retained"
				}
			} else if _, exists := tools.NativeMetadata(name); !exists {
				reason = "Unavailable source; selection intent is retained"
			}
			required := name == "publish_report" && c.roleIndex != catMain
			field.Disabled = !custom || !allowed || required
			if _, dynamicTool := dynamic[name]; !dynamicTool && !strings.HasPrefix(name, "mcp__") && !allowed && !slices.Contains(c.base.AllowedTools, name) {
				field.Value = ui.BoolValue(false)
			}
			if !allowed && reason == "" {
				reason = "Denied by " + role + " role"
			}
			if required {
				reason = "Required child reporting"
				field.Value = ui.BoolValue(true)
			}
			// Only an exceptional state (denied, required, unavailable) reads
			// under its switch; the routine one is the focused field's inline help.
			field.DescriptionImportant = reason != ""
			if !custom && reason == "" {
				reason = "Inherited role selection; choose Custom to override"
			}
			if custom && reason == "" {
				reason = "Selected intent; effective schema inclusion is request-bound"
			}
			field.Description = reason
		}
		if strings.HasPrefix(field.ID, "agent-skill/") {
			id := strings.TrimPrefix(field.ID, "agent-skill/")
			skill, exists := c.model.capabilities.Skills[id]
			field.Description = "Inherit " + mapDefault(skill.Mode, "automatic") + "; scripts still need execution permission"
			field.DescriptionImportant = false
			if !exists {
				field.Description = "Unavailable skill; saved invocation intent is retained"
				field.DescriptionImportant = true
			}
			for _, entry := range c.model.capabilitySnapshot.Entries {
				if entry.ID == id && entry.Kind == captypes.KindSkill && entry.Reason != "" {
					field.Description = entry.Reason + "; invocation intent retained"
					field.DescriptionImportant = true
				}
			}
		}
	}
}

func (c *agentCatalog) selectedSkillModes() map[string]string {
	modes := maps.Clone(c.base.SkillModes)
	if modes == nil {
		modes = make(map[string]string)
	}
	for _, field := range c.draft.Fields() {
		if !strings.HasPrefix(field.ID, "agent-skill/") {
			continue
		}
		id := strings.TrimPrefix(field.ID, "agent-skill/")
		if field.Value.String() == "inherit" {
			delete(modes, id)
		} else {
			modes[id] = field.Value.String()
		}
	}
	return modes
}

// connectionTools is the observed MCP catalog plus enabled built-in servers
// that have not started yet. Built-ins start on an agent's first request, and
// their tools must be selectable per agent before that.
func (c *agentCatalog) connectionTools() []captypes.ConnectionState {
	connections := slices.Clone(c.model.capabilitySnapshot.Connections)
	observed := make(map[string]bool, len(connections))
	for _, connection := range connections {
		observed[connection.ID] = len(connection.Tools) > 0
	}
	ids := make([]string, 0)
	for id, config := range c.model.capabilities.Connections {
		if config.IsBuiltin() && !config.Disabled && !observed[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		config := c.model.capabilities.Connections[id]
		descriptor, exists := captypes.Builtin(config.Command)
		if !exists {
			continue
		}
		state := captypes.ConnectionState{ID: id, State: "not started"}
		for _, name := range descriptor.Tools {
			tool := captypes.Tool{Name: name, OriginalName: name, ConnectionID: id, Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: "builtin", Selected: true}
			if config.Grants[name].Disabled {
				tool.Selected, tool.Reason = false, "Turned off for this connection"
			}
			state.Tools = append(state.Tools, tool)
		}
		connections = slices.DeleteFunc(connections, func(existing captypes.ConnectionState) bool { return existing.ID == id })
		connections = append(connections, state)
	}
	return connections
}
