package agent

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

type envContext struct {
	Shell            string
	OSInfo           string
	CWD              string
	Now              string
	Username         string
	Hostname         string
	HomeDir          string
	GitBranch        string
	GitRoot          string
	GitRemote        string
	GitDirty         string
	ToolsStr         string
	PlatformGuidance string
	DirListing       string
}

func gatherEnvContext() envContext {
	e := gatherStableEnvContext()
	e.Now = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	if branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		e.GitBranch = strings.TrimSpace(string(branch))
	}
	if root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		e.GitRoot = strings.TrimSpace(string(root))
	}
	if remote, err := exec.Command("git", "remote", "get-url", "origin").Output(); err == nil {
		e.GitRemote = strings.TrimSpace(string(remote))
	}
	if status, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		trimmed := strings.TrimSpace(string(status))
		if trimmed == "" {
			e.GitDirty = "clean"
		} else {
			e.GitDirty = fmt.Sprintf("%d changes", len(strings.Split(trimmed, "\n")))
		}
	}

	e.ToolsStr = detectTools()
	e.PlatformGuidance = platformGuidance()
	e.DirListing = buildDirListing(e.CWD)
	return e
}

func gatherStableEnvContext() envContext {
	e := envContext{
		Shell:   detectShell(),
		OSInfo:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		CWD:     getCWD(),
		HomeDir: detectHomeDir(),
	}

	e.Username = "unknown"
	if u, err := user.Current(); err == nil {
		e.Username = u.Username
	}
	e.Hostname = "unknown"
	if h, err := os.Hostname(); err == nil {
		e.Hostname = h
	}

	return e
}

func detectShell() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "unknown"
}

func detectHomeDir() string {
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "/unknown"
}

func detectTools() string {
	toolChecks := []string{"go", "python3", "python", "node", "npm", "pnpm", "bun", "make", "docker", "cargo", "rustc", "java", "javac", "dotnet", "gcc", "g++", "clang", "rg", "fd", "jq", "gh"}
	foundTools := make([]string, 0, len(toolChecks))
	for _, tool := range toolChecks {
		if _, err := exec.LookPath(tool); err == nil {
			foundTools = append(foundTools, tool)
		}
	}
	if len(foundTools) == 0 {
		return "none detected"
	}
	return strings.Join(foundTools, ", ")
}

func platformGuidance() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return `

# Windows Command Execution
- Commands run through cmd.exe batch files.
- cmd.exe syntax works directly. Use powershell -Command "..." only when PowerShell behavior is needed.
- Use && echo SUCCESS or || echo FAILED when a visible completion marker helps.
- Paths are case-insensitive. Forward slashes and backslashes are both usually accepted.
- Avoid Unix-only command names unless you have confirmed they exist in this environment.`
}

func (e envContext) buildSection() string {
	envLines := []string{
		"# Environment",
		fmt.Sprintf("- Date: %s", e.Now),
		fmt.Sprintf("- OS: %s", e.OSInfo),
		fmt.Sprintf("- Shell: %s", e.Shell),
		fmt.Sprintf("- User: %s@%s", e.Username, e.Hostname),
		fmt.Sprintf("- Home: %s", e.HomeDir),
		fmt.Sprintf("- Working Directory: %s", e.CWD),
	}
	if e.GitBranch != "" {
		gitLine := fmt.Sprintf("- Git: branch=%s, status=%s", e.GitBranch, e.GitDirty)
		if e.GitRemote != "" {
			gitLine += fmt.Sprintf(", remote=%s", e.GitRemote)
		}
		if e.GitRoot != "" && e.GitRoot != e.CWD {
			gitLine += fmt.Sprintf(", root=%s", e.GitRoot)
		}
		envLines = append(envLines, gitLine)
	}
	envLines = append(envLines, fmt.Sprintf("- Tools on PATH: %s", e.ToolsStr))

	if e.DirListing != "" {
		envLines = append(envLines, "")
		envLines = append(envLines, e.DirListing)
	}

	return strings.Join(envLines, "\n") + e.PlatformGuidance
}

// buildStableSection contains only values expected to remain constant for a
// workspace. Volatile timestamps, Git dirtiness, and directory listings do not
// belong in the cacheable system prefix; agents can inspect current state with
// tools when it matters.
func (e envContext) buildStableSection() string {
	envLines := []string{
		"# Environment",
		fmt.Sprintf("- OS: %s", e.OSInfo),
		fmt.Sprintf("- Shell: %s", e.Shell),
		fmt.Sprintf("- User: %s@%s", e.Username, e.Hostname),
		fmt.Sprintf("- Home: %s", e.HomeDir),
		fmt.Sprintf("- Working Directory: %s", e.CWD),
	}
	if guidance := platformGuidance(); guidance != "" {
		envLines = append(envLines, "", guidance)
	}
	return strings.Join(envLines, "\n")
}

func getCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func buildDirListing(cwd string) string {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return ""
	}

	const maxEntries = 100
	lines := []string{"# Directory Listing"}

	if len(entries) <= maxEntries {
		for _, entry := range entries {
			lines = append(lines, formatDirEntryName(entry))
		}
		return strings.Join(lines, "\n")
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			lines = append(lines, formatDirEntryName(entry))
			count++
			if count >= maxEntries {
				break
			}
		}
	}
	if count < maxEntries {
		for _, entry := range entries {
			if !entry.IsDir() {
				lines = append(lines, formatDirEntryName(entry))
				count++
				if count >= maxEntries {
					break
				}
			}
		}
	}
	lines = append(lines, fmt.Sprintf("... (%d items total; use list_directory for the full listing)", len(entries)))
	return strings.Join(lines, "\n")
}

func formatDirEntryName(entry os.DirEntry) string {
	name := entry.Name()
	if entry.IsDir() {
		return name + "/"
	}
	return name
}
