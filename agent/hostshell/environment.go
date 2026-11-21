package hostshell

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	searchPath = iota
	searchPathExt
	searchShell
	searchProgramFiles
	searchProgramW6432
	searchProgramFilesX86
	searchLocalAppData
	searchSystemRoot
	searchComSpec
	searchEnvCount
)

var searchEnvNames = [searchEnvCount]string{
	"PATH", "PATHEXT", "SHELL", "ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec",
}

// SearchEnvironment contains only the inputs for shell discovery. It does not
// retain or merge other variables from the attaching shell.
type SearchEnvironment struct {
	values [searchEnvCount]string
}

func currentSearchEnvironment() (out SearchEnvironment) {
	for i, name := range searchEnvNames {
		out.values[i] = os.Getenv(name)
	}
	return out
}

// SearchEnvironmentFrom reads the same detached environment that the terminal
// owner applies to the child. An absent key remains absent; it is not filled
// from another process. Windows variable names are case-insensitive.
func SearchEnvironmentFrom(env []string) (out SearchEnvironment) {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		for i, name := range searchEnvNames {
			if key == name || runtime.GOOS == "windows" && strings.EqualFold(key, name) {
				out.values[i] = value
				break
			}
		}
	}
	return out
}

// LookPath finds an executable at an absolute path or by name on this PATH.
// A bare name never searches the core's working directory or a relative entry.
func (e SearchEnvironment) LookPath(name string) string {
	if filepath.IsAbs(name) {
		return e.executable(name)
	}
	if name == "" || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return ""
	}
	for _, dir := range filepath.SplitList(e.values[searchPath]) {
		if filepath.IsAbs(dir) {
			if path := e.executable(filepath.Join(dir, name)); path != "" {
				return path
			}
		}
	}
	return ""
}
