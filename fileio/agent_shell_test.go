package fileio

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/types/command"
)

var shTestShell = command.Shell{ID: "sh", Label: "sh", Path: "/bin/sh", Syntax: command.SyntaxPOSIX}

// runIn runs one command line to completion in shell, through the same
// manager path as agent commands, and returns its output and exit code.
func runIn(t *testing.T, shell command.Shell, line, cwd string) (string, int) {
	t.Helper()
	manager := NewAsyncCommandManager()
	if err := manager.SetTempDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer manager.CloseAndWait()
	started, err := manager.StartUntrackedIn(shell, line, cwd, 60)
	if err != nil {
		t.Fatalf("start %q: %v", line, err)
	}
	<-started.Done()
	out, _, _ := started.ReadOutputLimit(0, 1<<20)
	return strings.ReplaceAll(out, "\r\n", "\n"), started.Info().ExitCode
}

func gitBashForTest(t *testing.T) command.Shell {
	t.Helper()
	git, err := exec.LookPath("git.exe")
	if err != nil {
		t.Skip("Git for Windows is not installed")
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
	if _, err := os.Stat(path); err != nil {
		t.Skip("Git Bash is not installed")
	}
	return command.Shell{ID: "git-bash", Label: "Git Bash", Path: path, Syntax: command.SyntaxPOSIX}
}

// Agent command lines are written for bash (`source`/`.` with BASH_SOURCE,
// `[[ ]]`, arrays); a POSIX sh such as dash fails on syntax the user's shell
// accepts.
func TestPOSIXShellRunsBashSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX host shell")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed on this host")
	}
	out, code := runIn(t, SystemShell(), `[[ -n "x" ]] && echo bracket_ok && echo "bash_version=${BASH_VERSION:+set}"`, "")
	if code != 0 || !strings.Contains(out, "bracket_ok") || !strings.Contains(out, "bash_version=set") {
		t.Fatalf("exit %d output %q; want bash semantics", code, out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "env.sh"), []byte("ROOT=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")/..\" && pwd)\"\necho \"root=$ROOT\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := runIn(t, SystemShell(), ". ./env.sh", dir); !strings.Contains(out, "root="+filepath.Dir(dir)) {
		t.Fatalf("sourced helper output %q; want root=%s", out, filepath.Dir(dir))
	}
}

// Passing a command to Git Bash with -c goes through the Windows command line,
// which the MSYS runtime re-parses: it changed `a\b\\c` to `a\b\c`, broke a
// sed backslash expression and dropped `\\` from an argument. Agents must be
// able to run the same inline commands they run on Linux.
func TestGitBashRunsInlineCommandsByteForByte(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Git Bash")
	}
	bash := gitBashForTest(t)
	for _, tc := range []struct{ line, want string }{
		{`x='a\b\\c'; echo "$x"`, `a\b\\c` + "\n"},
		{`printf '%s\n' "C:\Users\Lenovo" | sed 's/\\/\//g'`, "C:/Users/Lenovo\n"},
		{`printf '%s|' "a b" 'c"d' e\\f; echo`, `a b|c"d|e\f|` + "\n"},
		{"echo one\necho two", "one\ntwo\n"},
		{"cat <<'EOF'\n$HOME \"q\" \\b\nEOF", "$HOME \"q\" \\b\n"},
		{`for f in a b; do echo "[$f]"; done | tr '\n' ' '; echo`, "[a] [b] \n"},
		{`printf 'héllo ✓\n' | grep -c ✓`, "1\n"},
	} {
		if out, code := runIn(t, bash, tc.line, t.TempDir()); code != 0 || out != tc.want {
			t.Errorf("%q: exit %d output %q; want %q", tc.line, code, out, tc.want)
		}
	}
	if out, code := runIn(t, bash, `test -n "$CRUDE_TEMP_DIR" && test -d "$CRUDE_TEMP_DIR" && echo temp_ok; exit 7`, ""); code != 7 || out != "temp_ok\n" {
		t.Fatalf("temp dir and exit status: exit %d output %q", code, out)
	}
}

// PowerShell runs agent commands as script files: multi-line text works, the
// exit status follows the last statement, and errors stay plain text instead
// of the CLIXML that -Command writes to a redirected stream.
func TestPowerShellRunsAgentCommandsAsScripts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell on Windows")
	}
	found := false
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		found = true
		shell := command.Shell{ID: strings.TrimSuffix(name, ".exe"), Label: name, Path: path, Syntax: command.SyntaxPowerShell}
		for _, tc := range []struct {
			line     string
			code     int
			contains string
		}{
			{"$a = 'x'\n\"v=$a\"", 0, "v=x"},
			{"Write-Output 'héllo ✓'", 0, "héllo ✓"},
			{"cmd /c exit 3", 3, ""},
			{"cmd /c exit 3; Write-Output after", 0, "after"},
			{"Get-Item definitely-missing-crude-path", 1, "Get-Item"},
			{"if ($env:CRUDE_TEMP_DIR) { 'temp_ok' }", 0, "temp_ok"},
		} {
			out, code := runIn(t, shell, tc.line, t.TempDir())
			if code != tc.code || !strings.Contains(out, tc.contains) || strings.Contains(out, "CLIXML") || strings.Contains(out, "\x1b[") {
				t.Errorf("%s %q: exit %d output %q; want exit %d containing %q as plain text", name, tc.line, code, out, tc.code, tc.contains)
			}
		}
	}
	if !found {
		t.Skip("PowerShell is not installed")
	}
}

func TestUnavailableShellRefusesWithoutStartingAProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	missing := command.Shell{ID: "zsh", Label: "zsh", Syntax: command.SyntaxPOSIX, Unavailable: "The command shell \"zsh\" is not installed."}
	manager.SetShell(func() command.Shell { return missing })
	if _, err := manager.StartUntracked("echo hi", t.TempDir(), 5); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("unavailable shell error = %v", err)
	}
	if _, err := manager.Start("job", "echo hi", t.TempDir(), 5); err == nil {
		t.Fatal("background command started without a usable shell")
	}
	if manager.Running() || len(manager.List()) != 0 {
		t.Fatal("a refused command left a process or handle behind")
	}
}

// Every agent command path asks the owner for the current shell, so a
// settings change applies to the next command of each kind.
func TestEveryCommandPathResolvesTheAgentShell(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	var resolved atomic.Int32
	manager.SetShell(func() command.Shell { resolved.Add(1); return SystemShell() })
	if _, err := manager.Start("background", "exit 0", t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	blocking, err := manager.StartUntracked("exit 0", t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	<-blocking.Done()
	if _, err := manager.StartInteractive(t.Context(), InteractiveOptions{Command: "exit 0", CWD: t.TempDir()}, 2*time.Second, 100); err != nil {
		t.Fatal(err)
	}
	if got := resolved.Load(); got != 3 {
		t.Fatalf("agent shell resolved %d times for three commands", got)
	}
	explicit, err := manager.StartUntrackedIn(SystemShell(), "exit 0", t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	<-explicit.Done()
	if got := resolved.Load(); got != 3 {
		t.Fatal("a command with its own shell consulted the agent shell")
	}
}

// Profiles may cd away from the execution checkout. This real shell wrapper
// makes that startup effect deterministic instead of depending on host dotfiles.
func TestLoginCommandRestoresRequestedWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	root := t.TempDir()
	shellPath := filepath.Join(root, "login-shell")
	startup := "#!/bin/sh\nif [ \"$1\" = -l ]; then shift; cd / || exit; fi\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(shellPath, []byte(startup), 0700); err != nil {
		t.Fatal(err)
	}
	shell := command.Shell{Path: shellPath, Syntax: command.SyntaxPOSIX}
	for _, name := range []string{"checkout", "quoted ' $() space", "line\nbreak"} {
		t.Run(name, func(t *testing.T) {
			cwd := filepath.Join(root, name)
			if err := os.Mkdir(cwd, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cwd, "owned.txt"), []byte("execution-owned"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, login := range []bool{false, true} {
				cmd, cleanup, err := shellCommand(t.Context(), shell, "cat owned.txt", cwd, t.TempDir(), login)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				out, err := cmd.CombinedOutput()
				if err != nil || string(out) != "execution-owned" {
					t.Fatalf("login=%v cwd=%q output=%q err=%v", login, cwd, out, err)
				}
			}
		})
	}
}
