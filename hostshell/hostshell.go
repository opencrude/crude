// Package hostshell finds the shells installed on the core's machine and
// resolves the shell that runs agent commands. Integrated terminals and agent
// commands share this one inventory; each applies its own Auto order.
package hostshell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	ttypes "crude/types"
	"crude/types/command"
)

// detectionTTL bounds how long a detected inventory is reused. Detection
// searches PATH for several programs (7.6 ms per call measured on Windows),
// and command start, command review and prompt building each need the
// resolved shell. A shell installed while Crude runs appears within this
// time; a changed environment detects again at once.
const detectionTTL = 10 * time.Second

var inventory struct {
	sync.Mutex
	key    string
	at     time.Time
	shells []ttypes.TerminalShell
}

// Detect lists the shells installed on this machine. Terminals and agent
// commands apply their own Auto order to this one inventory.
func Detect() []ttypes.TerminalShell {
	key := environmentKey()
	inventory.Lock()
	defer inventory.Unlock()
	if inventory.at.IsZero() || inventory.key != key || time.Since(inventory.at) >= detectionTTL {
		inventory.key, inventory.at, inventory.shells = key, time.Now(), detect()
	}
	return slices.Clone(inventory.shells)
}

// environmentKey holds every variable that detection reads.
func environmentKey() string {
	var key strings.Builder
	for _, name := range []string{"PATH", "PATHEXT", "SHELL", "ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec"} {
		key.WriteString(os.Getenv(name))
		key.WriteByte(0)
	}
	return key.String()
}

// ResolveAgent resolves the configured agent command shell on this machine.
func ResolveAgent(choice string) command.Shell {
	return ttypes.ResolveAgentShell(choice, Detect())
}

// Requested resolves an explicit shell name or path from exec_command. Names
// match the detected shells, so "bash" on Windows is Git Bash: PATH bash.exe
// there is the WSL launcher, which runs in a separate file system.
func Requested(name string) (command.Shell, error) {
	name = strings.TrimSpace(name)
	key := strings.ToLower(strings.TrimSuffix(filepath.Base(name), ".exe"))
	explicitPath := strings.ContainsAny(name, `/\`)
	for _, shell := range Detect() {
		if shell.ID == "wsl" {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(shell.Path), ".exe"))
		matches := shell.ID == key || base == key
		if explicitPath {
			matches = sameFile(name, shell.Path)
		}
		if matches {
			return command.Shell{ID: shell.ID, Label: shell.Label, Path: shell.Path, Syntax: command.SyntaxFor(shell.ID)}, nil
		}
	}
	if runtime.GOOS != "windows" || key != "bash" && key != "sh" && key != "wsl" {
		if path, err := exec.LookPath(name); err == nil {
			return command.Shell{ID: key, Label: key, Path: path, Syntax: command.SyntaxFor(key)}, nil
		}
	}
	return command.Shell{}, fmt.Errorf("the shell %q is not installed on this computer. Leave shell empty to use the agent command shell", name)
}

func sameFile(left, right string) bool {
	a, errA := os.Stat(left)
	b, errB := os.Stat(right)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
