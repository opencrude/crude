package settings

import (
	"reflect"
	"slices"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestTerminalSettingsRoundTripWithoutDirtyState(t *testing.T) {
	want := types.TerminalSettings{Shell: "git-bash", Args: []string{"-x", "a path"}, LoginShell: "off", Environment: map[string]string{"EDITOR": "vim"}, ScrollbackLines: 2000, Placement: types.TerminalPlacementFloating}
	model := New(theme.DefaultDark(), types.AppSettings{Terminal: want, TerminalShells: []types.TerminalShell{{ID: "git-bash", Label: "Git Bash", Path: `C:\Git\bin\bash.exe`}}}, nil)
	if got := model.AppSettings().Terminal; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal settings\n got=%#v\nwant=%#v", got, want)
	}
	if model.hasUnsavedPreferences() {
		t.Fatal("an untouched Terminal page reports unsaved changes")
	}
}

func TestTerminalShellChoicesNameAutoAndKeepUnknownSelection(t *testing.T) {
	shells := []types.TerminalShell{{ID: "pwsh", Label: "PowerShell 7", Path: `C:\pwsh.exe`}, {ID: "cmd", Label: "Command Prompt", Path: `C:\cmd.exe`}}
	model := New(theme.DefaultDark(), types.AppSettings{Terminal: types.TerminalSettings{Shell: "zsh"}, TerminalShells: shells}, nil)
	form := model.categories[catTerminal].Form
	var labels []string
	for _, option := range form.Field(fieldTerminalShell).Options {
		labels = append(labels, option.Label)
	}
	if !slices.Equal(labels, []string{"Auto (PowerShell 7)", "PowerShell 7", "Command Prompt", "zsh (not installed)", "Custom"}) {
		t.Fatalf("shell choices = %v", labels)
	}
	if model.AppSettings().Terminal.Shell != "zsh" {
		t.Fatal("a shell synced from another machine was silently replaced")
	}

	form.SetValue(fieldTerminalShell, ui.StringValue(types.TerminalShellAuto))
	model.handleFieldChanged(catTerminal, fieldTerminalShell)
	command := form.Field(fieldTerminalCommand)
	if command.Required || command.Placeholder != `C:\pwsh.exe` {
		t.Fatalf("Auto command preview = %q required=%t", command.Placeholder, command.Required)
	}
	form.SetValue(fieldTerminalShell, ui.StringValue(types.TerminalShellCustom))
	model.handleFieldChanged(catTerminal, fieldTerminalShell)
	if !command.Required || command.Placeholder != "" {
		t.Fatalf("Custom command required=%t placeholder=%q", command.Required, command.Placeholder)
	}
}

func TestTerminalPageFollowsInterfaceInIDEGroup(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	visible := model.visibleCategoryIndices()
	ui, terminal := slices.Index(visible, catUI), slices.Index(visible, catTerminal)
	if ui < 0 || terminal != ui+1 || model.categories[catTerminal].Group != "IDE" {
		t.Fatalf("Terminal page position %d after Interface %d in %v", terminal, ui, visible)
	}
}
