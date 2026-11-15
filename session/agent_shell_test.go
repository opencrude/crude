package session

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crude/hostshell"
	"crude/tools"
	"crude/types"
)

func commandOutput(t *testing.T, tool, content string) string {
	t.Helper()
	result, ok := tools.ParseCommandResult(tool, content)
	if !ok || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("command failed: %q", content)
	}
	return strings.ReplaceAll(result.Output, "\r\n", "\n")
}

// On Windows with Git for Windows installed, Auto runs agent commands in Git
// Bash: the inline bash an agent writes on Linux runs unchanged through both
// command tools, and the prompt describes bash, never cmd.exe. Changing the
// setting applies to the next command and prompt without a restart.
func TestAgentCommandsRunInTheConfiguredShellEndToEnd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shells")
	}
	if hostshell.ResolveAgent("").ID != "git-bash" {
		t.Skip("Git Bash is not installed")
	}
	cfg := mediaAttributionConfig("http://127.0.0.1:1", "shell-model", "openai")
	sess, err := NewWithSessionDir("agent-shell", cfg, t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(t.TempDir())

	bash := `x='a\b\\c'; printf '%s\n' "$x" | sed 's/\\/\//g'; for f in 1 2; do echo "n$f"; done | tr '\n' ' '`
	want := "a/b//c\nn1 n2 "
	if got := commandOutput(t, "execute_command", mustTool(t, sess, "execute_command", map[string]any{"command": bash})); got != want {
		t.Fatalf("execute_command output %q; want %q", got, want)
	}
	if got := commandOutput(t, "exec_command", mustTool(t, sess, "exec_command", map[string]any{"cmd": bash, "yield_time_ms": 5000})); got != want {
		t.Fatalf("exec_command output %q; want %q", got, want)
	}
	// Exercise the real agent tool path, using the same shell inventory as
	// Settings, rather than duplicating Git Bash discovery in a test fixture.
	for _, tc := range []struct{ line, want string }{
		{`x='a\b\\c'; echo "$x"`, `a\b\\c` + "\n"},
		{`printf '%s\n' "C:\Users\Lenovo" | sed 's/\\/\//g'`, "C:/Users/Lenovo\n"},
		{`printf '%s|' "a b" 'c"d' e\\f; echo`, `a b|c"d|e\f|` + "\n"},
		{"echo one\necho two", "one\ntwo\n"},
		{"cat <<'EOF'\n$HOME \"q\" \\b\nEOF", "$HOME \"q\" \\b\n"},
		{`for f in a b; do echo "[$f]"; done | tr '\n' ' '; echo`, "[a] [b] \n"},
		{`printf 'héllo ✓\n' | grep -c ✓`, "1\n"},
	} {
		content := mustTool(t, sess, "execute_command", map[string]any{"command": tc.line})
		if got := commandOutput(t, "execute_command", content); got != tc.want {
			t.Errorf("%q: output %q; want %q", tc.line, got, tc.want)
		}
	}
	failed, err := sess.ExecuteToolForTest(t.Context(), "execute_command", map[string]any{
		"command": `test -n "$CRUDE_TEMP_DIR" && test -d "$CRUDE_TEMP_DIR" && echo temp_ok; exit 7`,
	})
	result, ok := tools.ParseCommandResult("execute_command", failed.Content)
	if err == nil || !ok || result.ExitCode == nil || *result.ExitCode != 7 || strings.ReplaceAll(result.Output, "\r\n", "\n") != "temp_ok\n" {
		t.Fatalf("temp dir and exit status: result=%q error=%v", failed.Content, err)
	}
	prompt := sess.systemPrompt(false)
	if !strings.Contains(prompt, "<shell>Git Bash (") || !strings.Contains(prompt, "write bash as on Linux") || strings.Contains(prompt, "Commands are cmd.exe") {
		t.Fatal("prompt does not describe Git Bash as the command shell")
	}

	t.Run("PowerShell and back", func(t *testing.T) {
		powerShell := hostshell.ResolveAgent("pwsh")
		if !powerShell.Usable() {
			powerShell = hostshell.ResolveAgent("powershell")
		}
		if !powerShell.Usable() {
			t.Skip("PowerShell is not installed")
		}
		sess.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Agents: types.AgentsSettings{Categories: ConfigToSettings(cfg).Agents.Categories, Shell: powerShell.ID}})
		if got := commandOutput(t, "execute_command", mustTool(t, sess, "execute_command", map[string]any{"command": "Write-Output 'powershell_ok'"})); got != "powershell_ok\n" {
			t.Fatalf("PowerShell output %q", got)
		}
		if got := commandOutput(t, "exec_command", mustTool(t, sess, "exec_command", map[string]any{"cmd": "Write-Output 'powershell_native'", "yield_time_ms": 5000})); got != "powershell_native\n" {
			t.Fatalf("PowerShell exec_command output %q", got)
		}
		if prompt := sess.systemPrompt(false); !strings.Contains(prompt, "write PowerShell") || strings.Contains(prompt, "write bash as on Linux") {
			t.Fatal("prompt did not follow the PowerShell setting")
		}
		sess.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Agents: types.AgentsSettings{Categories: ConfigToSettings(cfg).Agents.Categories, Shell: "git-bash"}})
		if got := commandOutput(t, "execute_command", mustTool(t, sess, "execute_command", map[string]any{"command": `printf 'bash_again\n'`})); got != "bash_again\n" {
			t.Fatalf("Git Bash output after switching back %q", got)
		}
		if prompt := sess.systemPrompt(false); !strings.Contains(prompt, "write bash as on Linux") || strings.Contains(prompt, "write PowerShell") {
			t.Fatal("prompt did not follow the Git Bash setting")
		}
	})

	sess.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Agents: types.AgentsSettings{Categories: ConfigToSettings(cfg).Agents.Categories, Shell: "cmd"}})
	if got := commandOutput(t, "execute_command", mustTool(t, sess, "execute_command", map[string]any{"command": "echo %OS%"})); strings.TrimSpace(got) != "Windows_NT" {
		t.Fatalf("after switching to Command Prompt, output %q", got)
	}
	if prompt := sess.systemPrompt(false); !strings.Contains(prompt, "Commands are cmd.exe") || strings.Contains(prompt, "write bash") {
		t.Fatal("prompt did not follow the shell setting")
	}
}

func mustTool(t *testing.T, sess *Session, tool string, params map[string]any) string {
	t.Helper()
	result, err := sess.ExecuteToolForTest(t.Context(), tool, params)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return result.Content
}
