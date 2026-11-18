package prompt

import (
	"strings"
	"testing"

	"crude/types/command"
)

func platformGuidance(t *testing.T, text string) string {
	t.Helper()
	start, end := strings.Index(text, "<platform_guidance>"), strings.Index(text, "</platform_guidance>")
	if start < 0 || end < start {
		t.Fatalf("no platform guidance in %q", text)
	}
	return text[start:end]
}

// The prompt described cmd.exe while exec_command ran PowerShell. It must
// describe the shell that actually runs the agent's commands, and only it.
func TestEnvironmentDescribesTheShellThatRunsCommands(t *testing.T) {
	pwsh := command.Shell{ID: "pwsh", Label: "PowerShell 7", Path: `C:\Program Files\PowerShell\7\pwsh.exe`, Syntax: command.SyntaxPowerShell}
	root := BuildRoot(RootOptions{Shell: pwsh})
	child, _ := BuildChild(ChildOptions{Category: "crafter", Shell: pwsh})
	for _, text := range []string{root, child} {
		guidance := platformGuidance(t, text)
		if !strings.Contains(text, "<shell>PowerShell 7 ("+XMLText(pwsh.Path)+")</shell>") || !strings.Contains(guidance, "PowerShell 7") ||
			strings.Contains(guidance, "%VAR%") || strings.Contains(guidance, "Commands are cmd.exe") {
			t.Fatalf("PowerShell environment: %q", guidance)
		}
	}
	for _, tc := range []struct {
		shell        command.Shell
		want, absent string
	}{
		{command.Shell{ID: "git-bash", Label: "Git Bash", Path: "bash.exe", Syntax: command.SyntaxPOSIX}, "write bash as on Linux", "cmd.exe"},
		{command.Shell{ID: "powershell", Label: "Windows PowerShell", Path: "powershell.exe", Syntax: command.SyntaxPowerShell}, "has no && or ||", "%VAR%"},
		{command.Shell{ID: "cmd", Label: "Command Prompt", Path: "cmd.exe", Syntax: command.SyntaxCmd}, "Commands are cmd.exe command lines", "Git Bash"},
		{command.Shell{ID: "zsh", Label: "zsh", Syntax: command.SyntaxPOSIX, Unavailable: "The command shell \"zsh\" is not installed."}, "Commands cannot run", "write bash"},
	} {
		if guidance := shellGuidance(tc.shell, true); !strings.Contains(guidance, tc.want) || strings.Contains(guidance, tc.absent) {
			t.Errorf("%s guidance %q; want %q without %q", tc.shell.ID, guidance, tc.want, tc.absent)
		}
	}
}
