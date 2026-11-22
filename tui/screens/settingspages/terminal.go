package settingspages

import (
	"slices"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types/setting"
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
func (m *Model) buildTerminalForm(current setting.Terminal) *ui.FormModel {
	current = setting.NormalizeTerminal(current)
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
	scrollback.Options = dialFieldOptions(current.ScrollbackLines, setting.DefaultTerminalScrollback, terminalScrollbackLadder, " lines")
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
		ui.SegmentedFieldState(fieldTerminalPlacement, "Open new terminals", []ui.FormOption{{ID: setting.TerminalPlacementDocked, Label: "Docked"}, {ID: setting.TerminalPlacementFloating, Label: "Floating"}}, current.Placement, "Where a new terminal appears. Either can be moved later."),
		scrollback,
		ui.SegmentedFieldState(fieldTerminalRTL, "Right-to-left text", []ui.FormOption{
			{ID: setting.TerminalRightToLeftShell, Label: "Shell output"},
			{ID: setting.TerminalRightToLeftAll, Label: "All output"},
			{ID: setting.TerminalRightToLeftOff, Label: "As written"},
		}, current.RightToLeftOrDefault(), "Where Crude puts Arabic, Persian and Hebrew text in reading order. Shell output skips full-screen programs, which can arrange this text themselves."),
	)
	m.syncTerminalCommand(form)
	return form
}

// UpdateShellInventory updates derived choices and previews in the retained
// form. It does not rebase editable fields, selection, filters or save state.
func (s *WorkspaceSettingsSurface) UpdateShellInventory(terminalShells, agentShells []setting.TerminalShell) bool {
	if s == nil || s.model == nil {
		return false
	}
	m := s.model
	terminalChanged := !slices.EqualFunc(m.terminalShells, terminalShells, setting.TerminalShell.Equal)
	if terminalChanged {
		m.terminalShells = setting.CloneTerminalShells(terminalShells)
		form := m.categories[catTerminal].Form
		shell := form.Field(fieldTerminalShell)
		shell.Options = terminalShellOptions(shell.Value.String(), m.terminalShells)
		m.syncTerminalCommand(form)
		if m.selected == catTerminal {
			s.refreshForm()
		}
	}
	agentChanged := !slices.EqualFunc(m.agentShells, agentShells, setting.TerminalShell.Equal)
	if agentChanged {
		m.agentShells = setting.CloneTerminalShells(agentShells)
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

func terminalShellOptions(selected string, shells []setting.TerminalShell) []ui.FormOption {
	auto := "Auto"
	if len(shells) > 0 {
		auto = "Auto (" + shells[0].Label + ")"
	}
	options := []ui.FormOption{{ID: setting.TerminalShellAuto, Label: auto}}
	for _, shell := range shells {
		options = append(options, ui.FormOption{ID: shell.ID, Label: shell.Label})
	}
	known := selected == setting.TerminalShellAuto || selected == setting.TerminalShellCustom ||
		slices.ContainsFunc(shells, func(shell setting.TerminalShell) bool { return shell.ID == selected })
	if !known {
		// A choice synced from another machine stays visible instead of
		// silently becoming Auto.
		options = append(options, ui.FormOption{ID: selected, Label: selected + " (not installed)"})
	}
	return append(options, ui.FormOption{ID: setting.TerminalShellCustom, Label: "Custom"})
}

// syncTerminalCommand requires a command only for Custom and previews the
// executable any other choice resolves to.
func (m *Model) syncTerminalCommand(form *ui.FormModel) {
	command := form.Field(fieldTerminalCommand)
	if command == nil {
		return
	}
	shell := form.Field(fieldTerminalShell).Value.String()
	command.Required = shell == setting.TerminalShellCustom
	command.Placeholder = ""
	command.Description = terminalCustomCommandHelp
	if command.Required {
		return
	}
	command.Errors = nil // A missing Custom command cannot invalidate a different shell.
	if path, _, err := setting.ResolveTerminalLaunch(setting.Terminal{Shell: shell}, m.terminalShells); err == nil {
		command.Placeholder = path
		command.Description = "New terminals start " + path
	} else if shell == setting.TerminalShellAuto {
		command.Description = "No supported shell was found. Install one or choose Custom."
	} else {
		command.Description = "This shell is not installed. Install it or choose another shell."
	}
}

func (m *Model) collectTerminal(cfg *setting.Terminal) {
	args, _ := splitShellWords(m.value(catTerminal, fieldTerminalArgs))
	*cfg = setting.NormalizeTerminal(setting.Terminal{
		Shell:           m.value(catTerminal, fieldTerminalShell),
		Command:         strings.TrimSpace(m.value(catTerminal, fieldTerminalCommand)),
		Args:            args,
		LoginShell:      loginShellSetting(m.value(catTerminal, fieldTerminalLogin)),
		Environment:     parseEnvironment(m.value(catTerminal, fieldTerminalEnv)),
		ScrollbackLines: parseOpt(m.value(catTerminal, fieldTerminalScrollback), setting.DefaultTerminalScrollback),
		Placement:       m.value(catTerminal, fieldTerminalPlacement),
		RightToLeft:     explicitChoice(m.value(catTerminal, fieldTerminalRTL), setting.TerminalRightToLeftShell),
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
