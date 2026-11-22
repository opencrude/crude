// Package hostshell finds shells on the core's machine for an explicit search
// environment. Integrated terminals and agent commands share the discovery
// rules but can inherit different environments and use different Auto orders.
package hostshell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"crude/types/command"
	"crude/types/setting"
)

// detectionTTL bounds how long a detected inventory is reused. Detection
// searches PATH for several programs (7.6 ms per call measured on Windows),
// and command start, command review and prompt building each need the
// resolved shell. A shell installed while Crude runs appears within this
// time; a changed environment detects again at once.
const detectionTTL = 10 * time.Second

// Inventory retains the most recent observation for one launch owner. It
// refreshes immediately when the search environment changes.
type Inventory struct {
	sync.Mutex
	env    SearchEnvironment
	at     time.Time
	shells []setting.TerminalShell
}

var processInventory Inventory

// Detect lists the shells installed on this machine. Terminals and agent
// commands use the same observation when they inherit the core process's
// environment.
func Detect() []setting.TerminalShell {
	return processInventory.Detect(currentSearchEnvironment())
}

// Detect returns the shells found on this machine and refreshes the result after a short time.
func (i *Inventory) Detect(env SearchEnvironment) []setting.TerminalShell {
	i.Lock()
	defer i.Unlock()
	if i.at.IsZero() || i.env != env || time.Since(i.at) >= detectionTTL {
		i.env, i.shells = env, detect(env)
		i.at = time.Now()
	}
	return setting.CloneTerminalShells(i.shells)
}

// ResolveAgent resolves the configured agent command shell on this machine.
func ResolveAgent(choice string) command.Shell {
	return setting.ResolveAgentShell(choice, Detect())
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
