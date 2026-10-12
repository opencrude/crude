//go:build windows

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"

	ttypes "crude/types"
)

// DetectTerminalShells lists installed shells in Auto preference order:
// PowerShell 7 before Windows PowerShell 5.1, which ships with every Windows
// but is no longer developed, then Command Prompt. Git Bash and WSL are offered
// but never chosen automatically.
func DetectTerminalShells() []ttypes.TerminalShell {
	var shells []ttypes.TerminalShell
	add := func(id, label string, path string, loginArgs, args []string) {
		if path != "" {
			shells = append(shells, ttypes.TerminalShell{ID: id, Label: label, Path: path, LoginArgs: loginArgs, Args: args})
		}
	}
	programFiles := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")}
	add("pwsh", "PowerShell 7", firstExecutable([]string{"pwsh.exe"}, installedPaths(programFiles, `PowerShell\7\pwsh.exe`, `PowerShell\7-preview\pwsh.exe`)), nil, nil)
	add("powershell", "Windows PowerShell", firstExecutable([]string{"powershell.exe"}, systemPaths(`WindowsPowerShell\v1.0\powershell.exe`)), nil, nil)
	cmd := os.Getenv("ComSpec")
	if !isFile(cmd) {
		cmd = firstExecutable([]string{"cmd.exe"}, systemPaths("cmd.exe"))
	}
	add("cmd", "Command Prompt", cmd, nil, nil)
	gitBash := installedPaths(append(programFiles, filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")), `Git\bin\bash.exe`)
	// System32\bash.exe is the WSL launcher, so Git Bash is located from its
	// installation or from git.exe (Git\cmd\git.exe), never from PATH bash.
	if git, err := exec.LookPath("git.exe"); err == nil {
		gitBash = append(gitBash, filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe"))
	}
	add("git-bash", "Git Bash", firstExecutable(nil, gitBash), []string{"--login"}, []string{"-i"})
	add("wsl", "WSL", firstExecutable(nil, systemPaths("wsl.exe")), nil, nil)
	return shells
}

func installedPaths(roots []string, relatives ...string) []string {
	var out []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, relative := range relatives {
			out = append(out, filepath.Join(root, relative))
		}
	}
	return out
}

func systemPaths(relative string) []string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return nil
	}
	return []string{filepath.Join(root, "System32", relative)}
}

// firstExecutable prefers PATH (the user's chosen installation, including the
// Microsoft Store alias for PowerShell) and then well-known install paths.
func firstExecutable(names, paths []string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	for _, path := range paths {
		if isFile(path) {
			return path
		}
	}
	return ""
}

func isFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
