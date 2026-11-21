package settings

import (
	"slices"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

const (
	fieldTerminalShell      = "terminal-shell"
	fieldTerminalCommand    = "terminal-command"
	fieldTerminalArgs       = "terminal-args"
	fieldTerminalLogin      = "terminal-login"
	fieldTerminalEnv        = "terminal-environment"
	fieldTerminalPlacement  = "terminal-placement"
	fieldTerminalRTL        = "terminal-right-to-left"
	fieldTerminalScrollback = "terminal-scrollback"
)

var terminalScrollbackLadder = []int{1000, 2000, 5000}

const terminalCustomCommandHelp = "Enter an executable name on PATH, or enter its full path."

// buildTerminalForm offers the shells detected on the core's machine. Auto
// names the shell it resolves to, so the choice is never a guess.
func (m *Model) buildTerminalForm(current types.TerminalSettings) *ui.FormModel {
	current = types.NormalizeTerminalSettings(current)
	args := ui.TextFieldState(fieldTerminalArgs, "Arguments", joinShellWords(current.Args), `Added after the shell's own startup arguments. Quote values with spaces: -NoLogo "a path".`)
	args.Validator = func(value ui.FieldValue) []string {
		if _, err := splitShellWords(value.String()); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	env := ui.KeyValuesFieldState(fieldTerminalEnv, "Variables", formatEnvironment(current.Environment), "Extra environment variables for new terminals.")
	env.Placeholder = "Add variable"
	env.Columns = []string{"Variable", "Value"}
	scrollback := ui.CycleFieldState(fieldTerminalScrollback, "Scrollback", nil, strconv.Itoa(current.ScrollbackLines), "Lines of history each terminal keeps for scrolling back.")
	scrollback.Options = dialFieldOptions(current.ScrollbackLines, types.DefaultTerminalScrollback, terminalScrollbackLadder, " lines")
	form := formModel(
		ui.SeparatorFieldState("terminal-shell-section", "Shell"),
		ui.SelectFieldState(fieldTerminalShell, "Shell", current.Shell, terminalShellOptions(current.Shell, m.terminalShells), "The shell new terminals start. Auto prefers PowerShell 7 on Windows and your login shell elsewhere."),
		ui.TextFieldState(fieldTerminalCommand, "Command", current.Command, terminalCustomCommandHelp),
		args,
		ui.SegmentedFieldState(fieldTerminalLogin, "Login shell", []ui.FormOption{
			{ID: "auto", Label: "Auto"}, {ID: "on", Label: "On"}, {ID: "off", Label: "Off"},
		}, loginShellChoice(current.LoginShell), "Auto loads your profile only when Crude does not have the environment of the shell that started it."),
		env,
		ui.SeparatorFieldState("terminal-behavior", "Behavior"),
		ui.SegmentedFieldState(fieldTerminalPlacement, "Open new terminals", []ui.FormOption{{ID: types.TerminalPlacementDocked, Label: "Docked"}, {ID: types.TerminalPlacementFloating, Label: "Floating"}}, current.Placement, "Where a new terminal appears. Either can be moved later."),
		scrollback,
		ui.SegmentedFieldState(fieldTerminalRTL, "Right-to-left text", []ui.FormOption{
			{ID: types.TerminalRightToLeftShell, Label: "Shell output"},
			{ID: types.TerminalRightToLeftAll, Label: "All output"},
			{ID: types.TerminalRightToLeftOff, Label: "As written"},
		}, current.RightToLeftOrDefault(), "Where Crude puts Arabic, Persian and Hebrew text in reading order. Shell output skips full-screen programs, which can arrange this text themselves."),
	)
	m.syncTerminalCommand(form)
	return form
}

// UpdateShellInventory updates derived choices and previews in the retained
// form. It does not rebase editable fields, selection, filters or save state.
func (s *WorkspaceSettingsSurface) UpdateShellInventory(terminalShells, agentShells []types.TerminalShell) bool {
	if s == nil || s.model == nil {
		return false
	}
	m := s.model
	terminalChanged := !slices.EqualFunc(m.terminalShells, terminalShells, types.TerminalShell.Equal)
	if terminalChanged {
		m.terminalShells = types.CloneTerminalShells(terminalShells)
		form := m.categories[catTerminal].Form
		shell := form.Field(fieldTerminalShell)
		shell.Options = terminalShellOptions(shell.Value.String(), m.terminalShells)
		m.syncTerminalCommand(form)
		if m.selected == catTerminal {
			s.refreshForm()
		}
	}
	agentChanged := !slices.EqualFunc(m.agentShells, agentShells, types.TerminalShell.Equal)
	if agentChanged {
		m.agentShells = types.CloneTerminalShells(agentShells)
		m.syncAgentShellOptions(m.categories[catMain].Form)
		m.syncAgentShellOptions(s.agents.draft)
		switch m.selected {
		case catAgents:
			s.agents.syncModel()
		case catMain:
			s.refreshForm()
		}
	}
	return terminalChanged || agentChanged
}

func terminalShellOptions(selected string, shells []types.TerminalShell) []ui.FormOption {
	auto := "Auto"
	if len(shells) > 0 {
		auto = "Auto (" + shells[0].Label + ")"
	}
	options := []ui.FormOption{{ID: types.TerminalShellAuto, Label: auto}}
	for _, shell := range shells {
		options = append(options, ui.FormOption{ID: shell.ID, Label: shell.Label})
	}
	known := selected == types.TerminalShellAuto || selected == types.TerminalShellCustom ||
		slices.ContainsFunc(shells, func(shell types.TerminalShell) bool { return shell.ID == selected })
	if !known {
		// A choice synced from another machine stays visible instead of
		// silently becoming Auto.
		options = append(options, ui.FormOption{ID: selected, Label: selected + " (not installed)"})
	}
	return append(options, ui.FormOption{ID: types.TerminalShellCustom, Label: "Custom"})
}

// syncTerminalCommand requires a command only for Custom and previews the
// executable any other choice resolves to.
func (m *Model) syncTerminalCommand(form *ui.FormModel) {
	command := form.Field(fieldTerminalCommand)
	if command == nil {
		return
	}
	shell := form.Field(fieldTerminalShell).Value.String()
	command.Required = shell == types.TerminalShellCustom
	command.Placeholder = ""
	command.Description = terminalCustomCommandHelp
	if command.Required {
		return
	}
	command.Errors = nil // A missing Custom command cannot invalidate a different shell.
	if path, _, err := types.ResolveTerminalLaunch(types.TerminalSettings{Shell: shell}, m.terminalShells); err == nil {
		command.Placeholder = path
		command.Description = "New terminals start " + path
	} else if shell == types.TerminalShellAuto {
		command.Description = "No supported shell was found. Install one or choose Custom."
	} else {
		command.Description = "This shell is not installed. Install it or choose another shell."
	}
}

func (m *Model) collectTerminal(cfg *types.TerminalSettings) {
	args, _ := splitShellWords(m.value(catTerminal, fieldTerminalArgs))
	*cfg = types.NormalizeTerminalSettings(types.TerminalSettings{
		Shell:           m.value(catTerminal, fieldTerminalShell),
		Command:         strings.TrimSpace(m.value(catTerminal, fieldTerminalCommand)),
		Args:            args,
		LoginShell:      loginShellSetting(m.value(catTerminal, fieldTerminalLogin)),
		Environment:     parseEnvironment(m.value(catTerminal, fieldTerminalEnv)),
		ScrollbackLines: parseOpt(m.value(catTerminal, fieldTerminalScrollback), types.DefaultTerminalScrollback),
		Placement:       m.value(catTerminal, fieldTerminalPlacement),
		RightToLeft:     explicitChoice(m.value(catTerminal, fieldTerminalRTL), types.TerminalRightToLeftShell),
	})
}

// loginShellChoice shows the stored login_shell value; empty is Auto.
func loginShellChoice(value string) string {
	if value == "on" || value == "off" {
		return value
	}
	return "auto"
}

// loginShellSetting stores Auto as the absent value, so it stays inherited.
func loginShellSetting(choice string) string {
	if choice == "on" || choice == "off" {
		return choice
	}
	return ""
}
