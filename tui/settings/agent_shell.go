package settings

import (
	"slices"

	"crude/tui/ui"
	"crude/types"
)

const (
	fieldAgentShell = "agent-shell"
	fieldMaxWait    = "agent-max-wait"
)

// agentShellField chooses the shell that runs every agent's commands, from the
// shells detected on the core's machine. Auto names the shell it resolves to.
// Terminals keep their own shell setting.
func (m *Model) agentShellField() *ui.FieldState {
	current := types.AgentShellAuto
	if m.agents != nil && m.agents.Shell != "" {
		current = m.agents.Shell
	}
	return ui.SelectFieldState(fieldAgentShell, "Command shell", current, agentShellOptions(current, m.terminalShells),
		"Runs every agent command. Auto prefers Git Bash, then PowerShell 7.")
}

func agentShellOptions(selected string, shells []types.TerminalShell) []ui.FormOption {
	auto := "Auto"
	if shell, ok := types.AgentShellAutoChoice(shells); ok {
		auto = "Auto (" + shell.Label + ")"
	}
	options := []ui.FormOption{{ID: types.AgentShellAuto, Label: auto}}
	choices := types.AgentShellChoices(shells)
	for _, shell := range choices {
		options = append(options, ui.FormOption{ID: shell.ID, Label: shell.Label})
	}
	if selected != types.AgentShellAuto && !slices.ContainsFunc(choices, func(shell types.TerminalShell) bool { return shell.ID == selected }) {
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
	if value := field.Value.String(); value != types.AgentShellAuto {
		return value, true
	}
	return "", true
}
