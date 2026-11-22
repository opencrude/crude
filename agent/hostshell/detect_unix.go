//go:build unix

package hostshell

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"crude/types/setting"
)

var unixShellLabels = map[string]string{"zsh": "Zsh", "bash": "Bash", "fish": "Fish", "sh": "sh", "nu": "Nushell", "ksh": "Korn shell", "tcsh": "tcsh"}

// detect lists installed shells: the user's login shell ($SHELL) first, then
// the platform's conventional default (zsh on macOS, bash elsewhere) and the
// remaining common shells.
func detect(env SearchEnvironment) []setting.TerminalShell {
	order := []string{"bash", "zsh", "fish", "sh"}
	if runtime.GOOS == "darwin" {
		order = []string{"zsh", "bash", "fish", "sh"}
	}
	var shells []setting.TerminalShell
	seen := make(map[string]bool)
	add := func(id, path string) {
		if path == "" || seen[id] {
			return
		}
		seen[id] = true
		label := unixShellLabels[id]
		if label == "" {
			label = id
		}
		// POSIX shells, fish and nu all accept -l for a login shell.
		shells = append(shells, setting.TerminalShell{ID: id, Label: label, Path: path, LoginArgs: []string{"-l"}})
	}
	if login := strings.TrimSpace(env.values[searchShell]); filepath.IsAbs(login) {
		add(filepath.Base(login), env.executable(login))
	}
	for _, name := range order {
		add(name, env.LookPath(name))
	}
	return shells
}

func (SearchEnvironment) executable(path string) string {
	// For an absolute path, Go only checks the executable, not ambient PATH.
	path, _ = exec.LookPath(path)
	return path
}
