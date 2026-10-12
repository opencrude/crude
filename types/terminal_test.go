package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var testShells = []TerminalShell{
	{ID: "pwsh", Label: "PowerShell 7", Path: `C:\pwsh.exe`},
	{ID: "powershell", Label: "Windows PowerShell", Path: `C:\powershell.exe`},
	{ID: "git-bash", Label: "Git Bash", Path: `C:\Git\bin\bash.exe`, LoginArgs: []string{"--login"}, Args: []string{"-i"}},
}

func TestResolveTerminalLaunch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings TerminalSettings
		shells   []TerminalShell
		command  string
		args     []string
		err      string
	}{
		{name: "auto takes the first detected shell", settings: TerminalSettings{}, shells: testShells, command: `C:\pwsh.exe`},
		{name: "explicit shell and user arguments", settings: TerminalSettings{Shell: "powershell", Args: []string{"-NoLogo"}}, shells: testShells, command: `C:\powershell.exe`, args: []string{"-NoLogo"}},
		{name: "login shell arguments precede shell and user arguments", settings: TerminalSettings{Shell: "git-bash", Args: []string{"-x"}}, shells: testShells, command: `C:\Git\bin\bash.exe`, args: []string{"--login", "-i", "-x"}},
		{name: "login off keeps the shell's own arguments", settings: TerminalSettings{Shell: "git-bash", LoginShell: "off"}, shells: testShells, command: `C:\Git\bin\bash.exe`, args: []string{"-i"}},
		{name: "custom command is used verbatim", settings: TerminalSettings{Shell: TerminalShellCustom, Command: "nu", Args: []string{"--login"}}, shells: testShells, command: "nu", args: []string{"--login"}},
		{name: "custom without a command fails with guidance", settings: TerminalSettings{Shell: TerminalShellCustom}, shells: testShells, err: "Custom without a command"},
		{name: "a missing shell is never silently replaced", settings: TerminalSettings{Shell: "zsh"}, shells: testShells, err: `"zsh" is not installed`},
		{name: "auto with no shells fails with guidance", settings: TerminalSettings{}, err: "no supported shell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, args, err := ResolveTerminalLaunch(tc.settings, tc.shells)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || command != tc.command || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("launch = %q %q %v; want %q %q", command, args, err, tc.command, tc.args)
			}
		})
	}
}

func TestNormalizeTerminalSettingsDefaultsAndBounds(t *testing.T) {
	got := NormalizeTerminalSettings(TerminalSettings{ScrollbackLines: 999999, Placement: "sideways", LoginShell: "maybe"})
	want := TerminalSettings{Shell: TerminalShellAuto, LoginShell: "on", ScrollbackLines: DefaultTerminalScrollback, Placement: TerminalPlacementDocked}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %+v; want %+v", got, want)
	}
	if NormalizeTerminalSettings(TerminalSettings{ScrollbackLines: 5}).ScrollbackLines != 100 {
		t.Fatal("scrollback below the floor was not clamped")
	}
}

// A workspace that changes only its shell keeps inheriting every other
// terminal field from the application layer, including later changes.
func TestTerminalWorkspaceOverrideIsFieldLevel(t *testing.T) {
	app := AppSettings{Terminal: TerminalSettings{Environment: map[string]string{"EDITOR": "vim"}, ScrollbackLines: 2000}}
	working := MergeAppWorkspace(app, WorkspaceOverrides{})
	working.Terminal.Shell = "git-bash"
	overrides := OverridesFromDiff(MergeAppWorkspace(app, WorkspaceOverrides{}), working)
	var stored map[string]any
	if err := json.Unmarshal(overrides.Terminal, &stored); err != nil || len(stored) != 1 || stored["shell"] != "git-bash" {
		t.Fatalf("terminal override = %s, %v; want only the shell", overrides.Terminal, err)
	}
	if !overrides.CategoryOverridden("terminal") {
		t.Fatal("terminal page is not reported as overridden")
	}
	app.Terminal.ScrollbackLines = 5000
	merged := MergeAppWorkspace(app, overrides)
	if merged.Terminal.Shell != "git-bash" || merged.Terminal.ScrollbackLines != 5000 || merged.Terminal.Environment["EDITOR"] != "vim" {
		t.Fatalf("merged terminal = %+v", merged.Terminal)
	}
	overrides.ClearCategory("terminal")
	if !overrides.IsEmpty() {
		t.Fatal("clearing the terminal page left overrides behind")
	}
}
