package prompt

import (
	"encoding/xml"
	"runtime"
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

func TestGitBashPromptGuidesOnDemandToolCompatibility(t *testing.T) {
	gitBash := command.Shell{ID: "git-bash", Label: "Git Bash", Path: "bash.exe", Syntax: command.SyntaxPOSIX}
	child, _ := BuildChild(ChildOptions{Category: "crafter", Shell: gitBash})
	cases := []struct {
		name     string
		guidance string
		want     bool
	}{
		{"Git Bash", shellGuidance(gitBash, true), true},
		{"Windows POSIX shell", shellGuidance(command.Shell{ID: "sh", Syntax: command.SyntaxPOSIX}, true), false},
		{"PowerShell", shellGuidance(command.Shell{ID: "pwsh", Syntax: command.SyntaxPowerShell}, true), false},
		{"Linux Bash", shellGuidance(command.Shell{ID: "bash", Syntax: command.SyntaxPOSIX}, false), false},
		{"root agent", platformGuidance(t, BuildRoot(RootOptions{Shell: gitBash})), runtime.GOOS == "windows"},
		{"child agent", platformGuidance(t, child), runtime.GOOS == "windows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, phrase := range []string{"type -a", "MSYS and Windows-native", "official source", "tool approval rules", "task-local", "before retrying"} {
				if got := strings.Contains(tc.guidance, phrase); got != tc.want {
					t.Errorf("guidance %q: contains %q = %t, want %t", tc.guidance, phrase, got, tc.want)
				}
			}
		})
	}
}

func TestXMLTextKeepsLinesAndEscapesMarkup(t *testing.T) {
	got := XMLText("# Plan\n\t- a < b && \"c\"\n</plan>\r")
	want := "# Plan\n\t- a &lt; b &amp;&amp; &#34;c&#34;\n&lt;/plan&gt;&#xD;"
	if got != want {
		t.Fatalf("XMLText = %q, want %q", got, want)
	}
	var decoded struct {
		Text string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte("<x>"+got+"</x>"), &decoded); err != nil || decoded.Text != "# Plan\n\t- a < b && \"c\"\n</plan>\r" {
		t.Fatalf("decoded %q, %v", decoded.Text, err)
	}
}
