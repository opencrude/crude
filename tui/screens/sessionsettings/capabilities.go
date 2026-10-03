package sessionsettings

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"crude/tui/ui"
	captypes "crude/types/capability"
)

func (screen *Screen) SetCapabilities(snapshot captypes.Snapshot) {
	screen.capabilities = snapshot
}

func (screen *Screen) capabilityOverrideKey() string {
	if screen.role == "default" && screen.effective.SessionMainAgent != "" {
		return "root/" + screen.effective.SessionMainAgent
	}
	return screen.role
}

func (screen *Screen) openCapabilities() {
	screen.page = capabilityPage
	model := ui.NewFormModel()
	configuration := categoryFor(screen.effective, screen.base, screen.role)
	override := screen.overrides[screen.capabilityOverrideKey()]
	model.AddField(&ui.FieldState{ID: "capability-boundary", Label: "Effective access", Type: ui.FieldTextArea, ReadOnly: true, Value: ui.StringValue("Selection changes apply at the next request. Selecting a tool does not approve access, launch a server, or remove skill instructions already loaded. Role ceilings and current workspace grants always apply.")})
	model.AddField(ui.SeparatorFieldState("skills", "Skills"))
	for _, entry := range screen.capabilities.Entries {
		if entry.Kind != captypes.KindSkill {
			continue
		}
		mode := configuration.SkillModes[entry.ID]
		if mode == "" {
			mode = entry.Mode
		}
		if mode == "" {
			mode = "automatic"
		}
		field := ui.CycleFieldState("skill/"+entry.ID, entry.Name, []string{"automatic", "manual", "disabled"}, mode, entry.ID+" · "+entry.State+" · "+entry.Version)
		for _, binding := range screen.capabilities.Bindings {
			if binding.ID == entry.ID {
				field.Description += fmt.Sprintf(" · loaded by %s at %s (%s)", binding.AgentID, binding.Version, binding.Digest)
			}
		}
		_, explicit := override.SkillModes[entry.ID]
		field.Inherited = !explicit
		field.InheritedFrom = "profile/workspace"
		model.AddField(field)
	}
	for _, connection := range screen.capabilities.Connections {
		model.AddField(ui.SeparatorFieldState("mcp/"+connection.ID, "MCP · "+connection.ID+" · "+connection.State))
		for _, tool := range connection.Tools {
			selected := !slices.Contains(configuration.DeniedTools, tool.Name) && (len(configuration.AllowedTools) == 0 || slices.Contains(configuration.AllowedTools, tool.Name))
			description := tool.Scope + " · " + string(tool.Effect)
			if tool.Reason != "" {
				description = tool.Reason
			}
			field := ui.BoolFieldState("tool/"+tool.Name, tool.OriginalName, selected, description)
			field.Inherited = override.AllowedTools == nil && override.DeniedTools == nil
			field.InheritedFrom = "profile/workspace"
			model.AddField(field)
		}
	}
	model.AddField(&ui.FieldState{ID: "capability-manager", Label: "Inspect Skills / MCP", Type: ui.FieldNav, Target: "skills.manage", Description: "Open full Settings without starting a connection"})
	screen.formModel = model
	screen.form.SetForm(model, screen.title(screen.roleName(), "Capabilities"), "")
	screen.actions = ui.NewDockedActionBar(ActionsID, screen.tokens,
		ui.ActionBarButton{Label: "Apply", Action: "session-settings.capabilities.apply", Enabled: true, Variant: ui.VariantPrimary},
		ui.ActionBarButton{Label: "Back", Action: "session-settings.role.back", Enabled: true},
		ui.ActionBarButton{Label: "Inherit selection", Action: "session-settings.capabilities.reset", Enabled: true})
}

func (screen *Screen) applyCapabilities() {
	configuration := categoryFor(screen.effective, screen.base, screen.role)
	configuration.SkillModes = maps.Clone(configuration.SkillModes)
	if configuration.SkillModes == nil {
		configuration.SkillModes = make(map[string]string)
	}
	configuration.DeniedTools = slices.Clone(configuration.DeniedTools)
	for _, field := range screen.formModel.Fields() {
		if strings.HasPrefix(field.ID, "skill/") {
			configuration.SkillModes[strings.TrimPrefix(field.ID, "skill/")] = field.Value.String()
		}
		if strings.HasPrefix(field.ID, "tool/") {
			name := strings.TrimPrefix(field.ID, "tool/")
			configuration.DeniedTools = slices.DeleteFunc(configuration.DeniedTools, func(value string) bool { return value == name })
			if !ui.BoolFieldValue(field) {
				configuration.DeniedTools = append(configuration.DeniedTools, name)
			}
		}
	}
	setCategory(&screen.effective, screen.role, configuration)
	override := screen.overrides[screen.capabilityOverrideKey()]
	override.SkillModes, override.DeniedTools = maps.Clone(configuration.SkillModes), slices.Clone(configuration.DeniedTools)
	screen.overrides[screen.capabilityOverrideKey()] = override
}

func (screen *Screen) clearCapabilityOverrides() {
	key := screen.capabilityOverrideKey()
	override := screen.overrides[key]
	override.AllowedTools, override.DeniedTools, override.SkillModes = nil, nil, nil
	screen.overrides[key] = override
	configuration := categoryFor(screen.effective, screen.base, screen.role)
	base := categoryFor(screen.base, screen.base, screen.role)
	configuration.AllowedTools, configuration.DeniedTools, configuration.SkillModes = slices.Clone(base.AllowedTools), slices.Clone(base.DeniedTools), maps.Clone(base.SkillModes)
	setCategory(&screen.effective, screen.role, configuration)
}
