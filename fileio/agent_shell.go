package fileio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"crude/types/command"
)

// SetShell installs the resolver for the shell that runs this manager's agent
// command lines. It is called for every command, so a settings change applies
// to the next command. Commands of a manager without a resolver run in
// SystemShell.
func (m *AsyncCommandManager) SetShell(resolve func() command.Shell) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shell = resolve
}

// resolveShell must not be called with m.mu held: the resolver reads its
// owner's configuration under the owner's lock.
func resolveShell(resolve func() command.Shell) command.Shell {
	if resolve == nil {
		return SystemShell()
	}
	return resolve()
}

// The PowerShell script that wraps an agent command. The prologue keeps output
// plain UTF-8 text (no progress records, colors or CLIXML). The epilogue gives
// the command the exit status of its last statement, like a POSIX shell: the
// native exit code when a program failed, otherwise 1 for a failed cmdlet.
const (
	powerShellPrologue = "$ProgressPreference = 'SilentlyContinue'; if ($PSStyle) { $PSStyle.OutputRendering = 'PlainText' }; try { [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false) } catch {}; $OutputEncoding = [Text.UTF8Encoding]::new($false); $global:LASTEXITCODE = $null\n"
	powerShellEpilogue = "\n$crudeSucceeded = $?\nif (-not $crudeSucceeded) { if ($global:LASTEXITCODE) { exit $global:LASTEXITCODE }; exit 1 }\nexit 0\n"
)

// shellCommand prepares one agent command line for its shell. This is the only
// place that turns an agent command into a process, for blocking, background
// and interactive commands alike.
//
//   - Command Prompt runs a temporary .cmd file (see windowsBatchScript).
//   - PowerShell runs a temporary UTF-8 .ps1 file with -File, without the
//     user's profile. -Command would serialize error output as CLIXML when
//     output is redirected, and a profile adds startup time and output.
//   - POSIX shells (bash, Git Bash, zsh, sh) run the line with -c, or from a
//     temporary .sh file on Windows; -l is added when login is set.
func shellCommand(ctx context.Context, shell command.Shell, line, cwd, tempDir string, login bool) (*exec.Cmd, func(), error) {
	if !shell.Usable() {
		if shell.Unavailable != "" {
			return nil, nil, errors.New(shell.Unavailable)
		}
		return nil, nil, errors.New("agent commands cannot run because no command shell was found on this computer")
	}
	environment := commandEnvironmentWithTemp(nil, tempDir)
	var c *exec.Cmd
	cleanup := func() {}
	switch shell.Syntax {
	case command.SyntaxCmd:
		script, err := windowsBatchScript(line, environment)
		if err != nil {
			return nil, nil, err
		}
		path, err := writeCommandScript(tempDir, "crude_*.cmd", script)
		if err != nil {
			return nil, nil, err
		}
		c, cleanup = setProcessGroupCmd(ctx, shell.Path, "/C", path), func() { _ = os.Remove(path) }
	case command.SyntaxPowerShell:
		path, err := writeCommandScript(tempDir, "crude_*.ps1", "\ufeff"+powerShellPrologue+line+powerShellEpilogue)
		if err != nil {
			return nil, nil, err
		}
		c = setProcessGroupCmd(ctx, shell.Path, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
		cleanup = func() { _ = os.Remove(path) }
	default:
		args := []string{"-c", line}
		if runtime.GOOS == "windows" {
			// A Windows command line does not carry arguments exactly: the
			// MSYS runtime that starts Git Bash re-parses it, which changes
			// backslashes and can drop a line break. A script file keeps the
			// command byte for byte.
			path, err := writeCommandScript(tempDir, "crude_*.sh", line+"\n")
			if err != nil {
				return nil, nil, err
			}
			args, cleanup = []string{filepath.ToSlash(path)}, func() { _ = os.Remove(path) }
		}
		if login {
			args = append([]string{"-l"}, args...)
		}
		c = setProcessGroupCmd(ctx, shell.Path, args...)
	}
	if cwd != "" {
		c.Dir = cwd
	}
	c.Env = environment
	return c, cleanup, nil
}

func writeCommandScript(tempDir, pattern, content string) (string, error) {
	file, err := os.CreateTemp(tempDir, pattern)
	if err != nil {
		return "", fmt.Errorf("prepare command: %w", err)
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("prepare command: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("prepare command: %w", err)
	}
	return path, nil
}
