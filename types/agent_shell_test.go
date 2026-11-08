package types

import (
	"strings"
	"testing"

	"crude/types/command"
)

func TestAgentShellAutoPrefersBashThenPowerShell(t *testing.T) {
	shell := func(id string) TerminalShell { return TerminalShell{ID: id, Label: id, Path: "/x/" + id} }
	for _, tc := range []struct {
		detected []string
		want     string
		syntax   command.Syntax
	}{
		{[]string{"pwsh", "powershell", "cmd", "git-bash", "wsl"}, "git-bash", command.SyntaxPOSIX},
		{[]string{"pwsh", "powershell", "cmd", "wsl"}, "pwsh", command.SyntaxPowerShell},
		{[]string{"powershell", "cmd"}, "powershell", command.SyntaxPowerShell},
		{[]string{"cmd"}, "cmd", command.SyntaxCmd},
		{[]string{"zsh", "bash", "sh"}, "bash", command.SyntaxPOSIX},
		{[]string{"wsl", "fish"}, "fish", command.SyntaxPOSIX},
	} {
		var shells []TerminalShell
		for _, id := range tc.detected {
			shells = append(shells, shell(id))
		}
		got := ResolveAgentShell(AgentShellAuto, shells)
		if got.ID != tc.want || got.Syntax != tc.syntax || !got.Usable() {
			t.Errorf("Auto over %v = %+v; want %s (%s)", tc.detected, got, tc.want, tc.syntax)
		}
		if empty := ResolveAgentShell("", shells); empty != got {
			t.Errorf("empty choice %+v differs from Auto %+v", empty, got)
		}
	}
}

// A chosen shell is never silently replaced: a missing one stays chosen and
// says how to fix it, and WSL is refused because it is a separate file system.
func TestExplicitAgentShellNeverFallsBack(t *testing.T) {
	shells := []TerminalShell{{ID: "cmd", Label: "Command Prompt", Path: `C:\Windows\System32\cmd.exe`}, {ID: "wsl", Label: "WSL", Path: `C:\Windows\System32\wsl.exe`}}
	missing := ResolveAgentShell("pwsh", shells)
	if missing.Usable() || missing.Syntax != command.SyntaxPowerShell || !strings.Contains(missing.Unavailable, "Settings › Agents") {
		t.Fatalf("missing pwsh = %+v", missing)
	}
	if wsl := ResolveAgentShell("wsl", shells); wsl.Usable() || !strings.Contains(wsl.Unavailable, "WSL") {
		t.Fatalf("wsl = %+v", wsl)
	}
	if choices := AgentShellChoices(shells); len(choices) != 1 || choices[0].ID != "cmd" {
		t.Fatalf("choices = %+v; want WSL excluded", choices)
	}
}
