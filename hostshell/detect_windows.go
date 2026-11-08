//go:build windows

package hostshell

import (
	"os"
	"os/exec"
	"path/filepath"

	ttypes "crude/types"
)

// detect lists the shells installed on this machine: PowerShell 7 before
// Windows PowerShell 5.1, which ships with every Windows but is no longer
// developed, then Command Prompt, Git Bash and WSL.
func detect() []ttypes.TerminalShell {
	var shells []ttypes.TerminalShell
	add := func(id, label string, path string, loginArgs, args []string) {
		if path != "" {
			shells = append(shells, ttypes.TerminalShell{ID: id, Label: label, Path: path, LoginArgs: loginArgs, Args: args})
		}
	}
	programFiles := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")}
	add("pwsh", "PowerShell 7", firstExecutable([]string{"pwsh.exe"}, installedPaths(programFiles, `PowerShell\7\pwsh.exe`, `PowerShell\7-preview\pwsh.exe`)), nil, nil)
	add("powershell", "Windows PowerShell", firstExecutable([]string{"powershell.exe"}, systemPaths(`WindowsPowerShell\v1.0\powershell.exe`)), nil, nil)
	add("cmd", "Command Prompt", commandPromptPath(), nil, nil)
	gitBash := installedPaths(append(programFiles, filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")), `Git\bin\bash.exe`)
	// System32\bash.exe is the WSL launcher, so Git Bash is located from its
	// installation or from git.exe (Git\cmd\git.exe), never from PATH bash.
	if git, err := exec.LookPath("git.exe"); err == nil {
		gitBash = append(gitBash, filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe"))
	}
	// The Windows installer puts a Git Bash in the user's own profile when the
	// machine has none. It is private to Crude and needs no PATH entry.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		gitBash = append(gitBash, filepath.Join(local, "Crude", "git", "bin", "bash.exe"))
	}
	add("git-bash", "Git Bash", firstExecutable(nil, gitBash), []string{"--login"}, []string{"-i"})
	add("wsl", "WSL", firstExecutable(nil, systemPaths("wsl.exe")), nil, nil)
	return shells
}

func commandPromptPath() string {
	if path := os.Getenv("ComSpec"); isFile(path) {
		return path
	}
	return firstExecutable([]string{"cmd.exe"}, systemPaths("cmd.exe"))
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
