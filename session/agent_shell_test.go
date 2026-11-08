package session

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crude/hostshell"
	"crude/types"
)

func commandOutput(t *testing.T, content string) string {
	t.Helper()
	var result struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatalf("decode command result: %v: %q", err, content)
	}
	if result.ExitCode != 0 {
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
	if got := commandOutput(t, mustTool(t, sess, "execute_command", map[string]any{"command": bash})); got != want {
		t.Fatalf("execute_command output %q; want %q", got, want)
	}
	if got := commandOutput(t, mustTool(t, sess, "exec_command", map[string]any{"cmd": bash, "yield_time_ms": 5000})); got != want {
		t.Fatalf("exec_command output %q; want %q", got, want)
	}
	prompt := sess.systemPrompt(false)
	if !strings.Contains(prompt, "<shell>Git Bash (") || !strings.Contains(prompt, "write bash as on Linux") || strings.Contains(prompt, "Commands are cmd.exe") {
		t.Fatal("prompt does not describe Git Bash as the command shell")
	}

	sess.UpdateWorkspaceDefaults(types.AppSettings{Accounts: cfg.Accounts, Agents: types.AgentsSettings{Categories: ConfigToSettings(cfg).Agents.Categories, Shell: "cmd"}})
	if got := commandOutput(t, mustTool(t, sess, "execute_command", map[string]any{"command": "echo %OS%"})); strings.TrimSpace(got) != "Windows_NT" {
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
