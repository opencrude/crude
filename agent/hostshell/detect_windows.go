//go:build windows

package hostshell

import (
	"crude/types/setting"
	"os"
	"path/filepath"
	"strings"
)

// detect lists the shells installed on this machine: PowerShell 7 before
// Windows PowerShell 5.1, which ships with every Windows but is no longer
// developed, then Command Prompt, Git Bash and WSL.
func detect(env SearchEnvironment) []setting.TerminalShell {
	var shells []setting.TerminalShell
	add := func(id, label string, path string, loginArgs, args []string) {
		if path != "" {
			shells = append(shells, setting.TerminalShell{ID: id, Label: label, Path: path, LoginArgs: loginArgs, Args: args})
		}
	}
	programFiles := []string{env.values[searchProgramFiles], env.values[searchProgramW6432], env.values[searchProgramFilesX86]}
	add("pwsh", "PowerShell 7", env.firstExecutable("pwsh.exe", installedPaths(programFiles, `PowerShell\7\pwsh.exe`, `PowerShell\7-preview\pwsh.exe`)), nil, nil)
	add("powershell", "Windows PowerShell", env.firstExecutable("powershell.exe", env.systemPaths(`WindowsPowerShell\v1.0\powershell.exe`)), nil, nil)
	add("cmd", "Command Prompt", env.commandPromptPath(), nil, nil)
	local := env.values[searchLocalAppData]
	gitBash := installedPaths(append(programFiles, filepath.Join(local, "Programs")), `Git\bin\bash.exe`)
	// System32\bash.exe is the WSL launcher, so Git Bash is located from its
	// installation or git.exe, never from PATH bash. Git for Windows places
	// its real git.exe in mingw32\bin or mingw64\bin, while the shell wrapper
	// remains in the installation's bin directory.
	// https://gitforwindows.org/git-wrapper.html
	if git := env.LookPath("git.exe"); git != "" {
		bin := filepath.Dir(git)
		root := filepath.Dir(bin)
		if strings.EqualFold(filepath.Base(bin), "bin") &&
			(strings.EqualFold(filepath.Base(root), "mingw32") || strings.EqualFold(filepath.Base(root), "mingw64")) {
			root = filepath.Dir(root)
		}
		gitBash = append(gitBash, filepath.Join(root, "bin", "bash.exe"))
	}
	// The Windows installer puts a Git Bash in the user's own profile when the
	// machine has none. It is private to Crude and needs no PATH entry.
	if local != "" {
		gitBash = append(gitBash, filepath.Join(local, "Crude", "git", "bin", "bash.exe"))
	}
	add("git-bash", "Git Bash", env.firstExecutable("", gitBash), []string{"--login"}, []string{"-i"})
	add("wsl", "WSL", env.firstExecutable("", env.systemPaths("wsl.exe")), nil, nil)
	return shells
}

func (e SearchEnvironment) commandPromptPath() string {
	if path := e.values[searchComSpec]; filepath.IsAbs(path) && isFile(path) {
		return path
	}
	return e.firstExecutable("cmd.exe", e.systemPaths("cmd.exe"))
}

func installedPaths(roots []string, relatives ...string) []string {
	var out []string
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		for _, relative := range relatives {
			out = append(out, filepath.Join(root, relative))
		}
	}
	return out
}

func (e SearchEnvironment) systemPaths(relative string) []string {
	root := e.values[searchSystemRoot]
	if !filepath.IsAbs(root) {
		return nil
	}
	return []string{filepath.Join(root, "System32", relative)}
}

// firstExecutable prefers PATH (the user's chosen installation, including the
// Microsoft Store alias for PowerShell) and then well-known install paths.
func (e SearchEnvironment) firstExecutable(name string, paths []string) string {
	if path := e.LookPath(name); path != "" {
		return path
	}
	for _, path := range paths {
		if filepath.IsAbs(path) && isFile(path) {
			return path
		}
	}
	return ""
}

func (e SearchEnvironment) executable(path string) string {
	if filepath.Ext(path) != "" {
		if isFile(path) {
			return path
		}
		return ""
	}
	exts := e.values[searchPathExt]
	if exts == "" {
		exts = ".COM;.EXE"
	}
	for ext := range strings.SplitSeq(exts, ";") {
		switch strings.ToUpper(strings.TrimPrefix(ext, ".")) {
		case "COM", "EXE":
			// A direct PTY launch uses CreateProcess, not file associations
			// or a command interpreter for script extensions.
			if candidate := path + "." + strings.TrimPrefix(ext, "."); isFile(candidate) {
				return candidate
			}
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
