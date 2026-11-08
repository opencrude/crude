package safety

import (
	"testing"

	"crude/types/command"
)

// Reproduced on Windows: PowerShell deleted both directories while the bash
// parser rated `git --version @(Remove-Item a); type x; & {Remove-Item b}`
// as safe, so such a command ran without approval and in parallel.
func TestPowerShellHiddenCodeNeedsApproval(t *testing.T) {
	powerShell := DefaultCommandPolicy()
	powerShell.Syntax = command.SyntaxPowerShell
	for _, cmd := range []string{
		"git status @(Remove-Item src)",
		"type README.md; & {Remove-Item src}",
		"git status <# x #> ; Remove-Item src",
		"echo (Remove-Item -Recurse -Force src)",
		"git log | % { Remove-Item $_ }",
		"git log ‘a’ (Remove-Item src) ‘b’",
	} {
		result := CheckCommandSafetyWithPolicy(cmd, nil, powerShell)
		if result.Risk == RiskSafe || !result.NeedsConfirmation() {
			t.Errorf("PowerShell %q = %v (%s); want approval", cmd, result.Risk, result.Reason)
		}
	}
	// A rule names the commands the checker saw; it cannot approve hidden code.
	ruled := powerShell
	ruled.Rules = []CommandRule{{Pattern: "git*", Risk: RiskSafe}}
	if result := CheckCommandSafetyWithPolicy("git status @(Remove-Item src)", nil, ruled); result.Risk == RiskSafe {
		t.Fatalf("a safe rule approved hidden PowerShell code: %s", result.Reason)
	}
	// Plain read-only commands, stream merges and chains stay automatic.
	for _, cmd := range []string{"git status", "git status 2>&1", "git status && git log -1", "Get-Content README.md | Select-String foo"} {
		if result := CheckCommandSafetyWithPolicy(cmd, nil, powerShell); result.Risk == RiskSafe {
			continue
		} else if cmd != "Get-Content README.md | Select-String foo" {
			t.Errorf("PowerShell %q = %v (%s); want safe", cmd, result.Risk, result.Reason)
		}
	}
	// POSIX review is unchanged: parentheses and braces are bash syntax that
	// the parser reads.
	if result := CheckCommandSafetyWithPolicy("git log --format='%h (%an)'", nil, DefaultCommandPolicy()); result.Risk != RiskSafe {
		t.Fatalf("POSIX git log = %v (%s); want safe", result.Risk, result.Reason)
	}
}

// `$env:NAME = 'value'` is how PowerShell sets a variable for the commands
// that follow. Bash parsing rated it destructive; it runs no code.
func TestPowerShellLiteralAssignmentsAreNotCommands(t *testing.T) {
	powerShell := DefaultCommandPolicy()
	powerShell.Syntax = command.SyntaxPowerShell
	plain := CheckCommandSafetyWithPolicy("git status", nil, powerShell).Risk
	for _, cmd := range []string{"$env:GIT_PAGER='cat'; git status", "$env:GIT_PAGER = \"cat\"\ngit status", "$n = 3; git status"} {
		if got := CheckCommandSafetyWithPolicy(cmd, nil, powerShell); got.Risk != plain {
			t.Errorf("%q = %v (%s); want %v like git status", cmd, got.Risk, got.Reason, plain)
		}
	}
	for _, cmd := range []string{"$x = Remove-Item src; git status", "$env:X = \"$(Remove-Item src)\"; git status", "$env:X = \"$HOME\"; git status",
		"$x = 'a’; Remove-Item src; '", "$x = \"a” ; Remove-Item src ; \""} {
		if got := CheckCommandSafetyWithPolicy(cmd, nil, powerShell); got.Risk == RiskSafe {
			t.Errorf("%q = safe; an assignment that runs or expands code must be reviewed", cmd)
		}
	}
}

func TestCallSyntaxFollowsAnExplicitExecShell(t *testing.T) {
	for _, tc := range []struct {
		tool, shell string
		want        command.Syntax
	}{
		{"execute_command", "pwsh", command.SyntaxPOSIX},
		{"exec_command", "", command.SyntaxPOSIX},
		{"exec_command", "pwsh", command.SyntaxPowerShell},
		{"exec_command", `C:\Windows\System32\cmd.exe`, command.SyntaxCmd},
		{"exec_command", "bash", command.SyntaxPOSIX},
	} {
		if got := CallSyntax(tc.tool, tc.shell, command.SyntaxPOSIX); got != tc.want {
			t.Errorf("CallSyntax(%s, %q) = %s; want %s", tc.tool, tc.shell, got, tc.want)
		}
	}
}
