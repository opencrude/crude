//go:build unix

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	ttypes "crude/types"
)

var unixShellLabels = map[string]string{"zsh": "Zsh", "bash": "Bash", "fish": "Fish", "sh": "sh", "nu": "Nushell", "ksh": "Korn shell", "tcsh": "tcsh"}

// DetectTerminalShells lists installed shells in Auto preference order: the
// user's login shell ($SHELL) first, then the platform's conventional default
// (zsh on macOS, bash elsewhere) and the remaining common shells.
func DetectTerminalShells() []ttypes.TerminalShell {
	order := []string{"bash", "zsh", "fish", "sh"}
	if runtime.GOOS == "darwin" {
		order = []string{"zsh", "bash", "fish", "sh"}
	}
	var shells []ttypes.TerminalShell
	seen := make(map[string]bool)
	add := func(id, path string) {
		if path == "" || seen[id] {
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return
		}
		seen[id] = true
		label := unixShellLabels[id]
		if label == "" {
			label = id
		}
		// POSIX shells, fish and nu all accept -l for a login shell.
		shells = append(shells, ttypes.TerminalShell{ID: id, Label: label, Path: path, LoginArgs: []string{"-l"}})
	}
	if login := strings.TrimSpace(os.Getenv("SHELL")); login != "" {
		add(filepath.Base(login), login)
	}
	for _, name := range order {
		if path, err := exec.LookPath(name); err == nil {
			add(name, path)
		}
	}
	return shells
}
