package settings

import (
	"maps"
	"slices"
	"strings"

	"crude/agent"
	"crude/safety"
	"crude/tools"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func (catalog *agentCatalog) capabilityRole() string {
	if field := catalog.draft.Field("agent-role"); field != nil {
		return field.Value.String()
	}
	return catalog.profile.Category
}

func (catalog *agentCatalog) syncToolPage() {
	var fields []*ui.FieldState
	query := strings.ToLower(catalog.toolFilter.Value())
	for _, field := range catalog.draft.Fields() {
		isTool := strings.HasPrefix(field.ID, "agent-tool") || strings.HasPrefix(field.ID, "agent-skill") || field.ID == "agent-file-ownership"
		if !isTool {
			continue
		}
		if query != "" && (strings.HasPrefix(field.ID, "agent-tool/") || strings.HasPrefix(field.ID, "agent-skill/")) && !strings.Contains(strings.ToLower(field.ID+" "+field.Label+" "+field.Description), query) {
			continue
		}
		fields = append(fields, field)
	}
	same := catalog.toolPage != nil && catalog.toolPage.FieldCount() == len(fields)
	if same {
		for index, field := range fields {
			if catalog.toolPage.Fields()[index] != field {
				same = false
				break
			}
		}
	}
	if !same {
		catalog.toolPage = formModel(fields...)
		catalog.toolPage.OnChanged(catalog.fieldChanged)
	}
	catalog.form.SetForm(catalog.toolPage, "", "")
}

func (catalog *agentCatalog) syncCapabilityFields() {
	if catalog.draft == nil || catalog.roleIndex == catMemory || catalog.roleIndex == catAutoReviewPolicy {
		return
	}
	addGroup := func(id, label string) {
		if catalog.draft.Field(id) == nil {
			catalog.draft.AddField(ui.SeparatorFieldState(id, label))
		}
	}
	nativeAllowed := make(map[string]bool)
	for _, tool := range agent.FilterTools(tools.AgentTools, catalog.base.AllowedTools, catalog.base.DeniedTools, catalog.capabilityRole(), catalog.roleIndex != catMain) {
		nativeAllowed[tool.Function.Name] = true
	}
	add := func(name, label string, selected bool) {
		if catalog.draft.Field("agent-tool/"+name) != nil {
			return
		}
		if slices.Contains(catalog.base.AllowedTools, name) {
			selected = true
		}
		if slices.Contains(catalog.base.DeniedTools, name) {
			selected = false
		}
		catalog.draft.AddField(ui.CheckboxFieldState("agent-tool/"+name, label, selected, ""))
	}
	addGroup("agent-tool-group/builtin", "Built-in tools")
	for _, tool := range tools.AgentTools {
		add(tool.Function.Name, strings.ReplaceAll(tool.Function.Name, "_", " "), nativeAllowed[tool.Function.Name])
	}
	for _, connection := range catalog.connectionTools() {
		addGroup("agent-tool-group/mcp/"+connection.ID, "MCP · "+connection.ID)
		for _, tool := range connection.Tools {
			add(tool.Name, tool.OriginalName+" · "+connection.ID, len(catalog.base.AllowedTools) == 0 || slices.Contains(catalog.base.AllowedTools, tool.Name))
		}
	}
	for _, name := range append(slices.Clone(catalog.base.AllowedTools), catalog.base.DeniedTools...) {
		if catalog.draft.Field("agent-tool/"+name) == nil {
			addGroup("agent-tool-group/unavailable", "Retained unavailable selections")
			add(name, name, slices.Contains(catalog.base.AllowedTools, name))
		}
	}
	addGroup("agent-skill-group", "Skills · invocation preferences")
	skills := make(map[string]string)
	for _, entry := range catalog.model.capabilitySnapshot.Entries {
		if entry.Kind == captypes.KindSkill {
			skills[entry.ID] = entry.Name
		}
	}
	for id, skill := range catalog.model.capabilities.Skills {
		skills[id] = skill.Name
	}
	for id := range catalog.base.SkillModes {
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
		if catalog.draft.Field("agent-skill/"+id) != nil {
			continue
		}
		mode := mapDefault(catalog.base.SkillModes[id], "inherit")
		field := ui.SelectFieldState("agent-skill/"+id, skills[id]+" · "+id, mode, []ui.FormOption{{ID: "inherit", Label: "Inherit"}, {ID: "automatic", Label: "Automatic"}, {ID: "manual", Label: "Manual only"}, {ID: "disabled", Label: "Disabled"}}, "Invocation preference is separate from script execution permissions.")
		catalog.draft.AddField(field)
	}
}

func (catalog *agentCatalog) refreshCapabilityLimits() {
	role := catalog.capabilityRole()
	var roleAllowed, roleDenied []string
	if catalog.roleIndex < 0 {
		configured := catalog.model.AppSettings().Agents.Categories[role]
		roleAllowed, roleDenied = configured.AllowedTools, configured.DeniedTools
	}
	native := make(map[string]bool)
	definitions := append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.CollaborationTools...)
	for _, tool := range agent.FilterTools(definitions, roleAllowed, roleDenied, role, catalog.roleIndex != catMain) {
		native[tool.Function.Name] = true
	}
	policy := agent.ComposePolicy(safety.DefaultPolicy(), catalog.base.Policy, role)
	dynamic := make(map[string]captypes.Tool)
	connections := make(map[string]captypes.ConnectionState)
	for _, connection := range catalog.connectionTools() {
		connections[connection.ID] = connection
		for _, tool := range connection.Tools {
			dynamic[tool.Name] = tool
		}
	}
	custom := catalog.draft.Field("agent-tool-mode").Value.String() == "Custom selection"
	for _, field := range catalog.draft.Fields() {
		if strings.HasPrefix(field.ID, "agent-tool/") {
			name := strings.TrimPrefix(field.ID, "agent-tool/")
			reason := ""
			allowed := native[name]
			if tool, exists := dynamic[name]; exists {
				allowed = agent.PolicyAllowsMetadata(policy, tools.ToolMetadata{Name: tool.Name, Effect: tool.Effect, Scope: tool.Scope, Fingerprint: tool.Fingerprint, Dynamic: true})
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
			required := name == "publish_report" && catalog.roleIndex != catMain
			field.Disabled = !custom || !allowed || required
			if _, dynamicTool := dynamic[name]; !dynamicTool && !strings.HasPrefix(name, "mcp__") && !allowed && !slices.Contains(catalog.base.AllowedTools, name) {
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
			skill, exists := catalog.model.capabilities.Skills[id]
			field.Description = "Inherit " + mapDefault(skill.Mode, "automatic") + "; scripts still need execution permission"
			field.DescriptionImportant = false
			if !exists {
				field.Description = "Unavailable skill; saved invocation intent is retained"
				field.DescriptionImportant = true
			}
			for _, entry := range catalog.model.capabilitySnapshot.Entries {
				if entry.ID == id && entry.Kind == captypes.KindSkill && entry.Reason != "" {
					field.Description = entry.Reason + "; invocation intent retained"
					field.DescriptionImportant = true
				}
			}
		}
	}
}

func (catalog *agentCatalog) selectedSkillModes() map[string]string {
	modes := maps.Clone(catalog.base.SkillModes)
	if modes == nil {
		modes = make(map[string]string)
	}
	for _, field := range catalog.draft.Fields() {
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
func (catalog *agentCatalog) connectionTools() []captypes.ConnectionState {
	connections := slices.Clone(catalog.model.capabilitySnapshot.Connections)
	observed := make(map[string]bool, len(connections))
	for _, connection := range connections {
		observed[connection.ID] = len(connection.Tools) > 0
	}
	ids := make([]string, 0)
	for id, config := range catalog.model.capabilities.Connections {
		if config.IsBuiltin() && !config.Disabled && !observed[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		config := catalog.model.capabilities.Connections[id]
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
