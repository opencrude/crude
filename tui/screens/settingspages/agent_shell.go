package settingspages

import (
	"slices"

	"crude/tui/ui"
	"crude/types/setting"
)

const (
	fieldAgentShell = "agent-shell"
	fieldMaxWait    = "agent-max-wait"
)

// agentShellField chooses the shell that runs every agent's commands, from the
// shells detected on the core's machine. Auto names the shell it resolves to.
// Terminals keep their own shell setting.
func (m *Model) agentShellField() *ui.FieldState {
	current := setting.AgentShellAuto
	if m.agents != nil && m.agents.Shell != "" {
		current = m.agents.Shell
	}
	return ui.SelectFieldState(fieldAgentShell, "Command shell", current, agentShellOptions(current, m.agentShells),
		"Runs every agent command. Auto prefers Git Bash, then PowerShell 7.")
}

func (m *Model) syncAgentShellOptions(form *ui.FormModel) {
	if form != nil {
		if field := form.Field(fieldAgentShell); field != nil {
			field.Options = agentShellOptions(field.Value.String(), m.agentShells)
		}
	}
}

func agentShellOptions(selected string, shells []setting.TerminalShell) []ui.FormOption {
	auto := "Auto"
	if shell, ok := setting.AgentShellAutoChoice(shells); ok {
		auto = "Auto (" + shell.Label + ")"
	}
	options := []ui.FormOption{{ID: setting.AgentShellAuto, Label: auto}}
	choices := setting.AgentShellChoices(shells)
	for _, shell := range choices {
		options = append(options, ui.FormOption{ID: shell.ID, Label: shell.Label})
	}
	if selected != setting.AgentShellAuto && !slices.ContainsFunc(choices, func(shell setting.TerminalShell) bool { return shell.ID == selected }) {
		// A choice synced from another machine stays visible; it is never
		// silently replaced by Auto.
		options = append(options, ui.FormOption{ID: selected, Label: selected + " (not installed)"})
	}
	return options
}

// agentShellChoice stores Auto as the absent value, so it inherits live.
func agentShellChoice(form *ui.FormModel) (string, bool) {
	field := form.Field(fieldAgentShell)
	if field == nil {
		return "", false
	}
	if value := field.Value.String(); value != setting.AgentShellAuto {
		return value, true
	}
	return "", true
}
