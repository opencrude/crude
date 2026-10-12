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
	fieldTerminalScrollback = "terminal-scrollback"
)

var terminalScrollbackLadder = []int{1000, 2000, 5000}

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
		ui.TextFieldState(fieldTerminalCommand, "Command", current.Command, "Executable for a Custom shell, found on PATH or given as an absolute path."),
		args,
		ui.BoolFieldState(fieldTerminalLogin, "Login shell", current.LoginShell == "on", "Start shells that support it (bash, zsh, fish, Git Bash) as login shells, so your profile loads."),
		env,
		ui.SeparatorFieldState("terminal-behavior", "Behavior"),
		ui.SegmentedFieldState(fieldTerminalPlacement, "Open new terminals", []ui.FormOption{{ID: types.TerminalPlacementDocked, Label: "Docked"}, {ID: types.TerminalPlacementFloating, Label: "Floating"}}, current.Placement, "Where a new terminal appears. Either can be moved later."),
		scrollback,
	)
	m.syncTerminalCommand(form)
	return form
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
	if path, _, err := types.ResolveTerminalLaunch(types.TerminalSettings{Shell: shell}, m.terminalShells); err == nil && shell != types.TerminalShellCustom {
		command.Placeholder = path
	}
}

func (m *Model) collectTerminal(cfg *types.TerminalSettings) {
	args, _ := splitShellWords(m.value(catTerminal, fieldTerminalArgs))
	*cfg = types.NormalizeTerminalSettings(types.TerminalSettings{
		Shell:           m.value(catTerminal, fieldTerminalShell),
		Command:         strings.TrimSpace(m.value(catTerminal, fieldTerminalCommand)),
		Args:            args,
		LoginShell:      onOff(m.value(catTerminal, fieldTerminalLogin) == "on"),
		Environment:     parseEnvironment(m.value(catTerminal, fieldTerminalEnv)),
		ScrollbackLines: parseOpt(m.value(catTerminal, fieldTerminalScrollback), types.DefaultTerminalScrollback),
		Placement:       m.value(catTerminal, fieldTerminalPlacement),
	})
}
