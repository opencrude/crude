package prompt

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"

	"crude/types/command"
)

func stableEnvironment(workDir, tempDir string, shell command.Shell) string {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	home := "/unknown"
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			home = value
			break
		}
	}
	username, hostname := "unknown", "unknown"
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	if value, err := os.Hostname(); err == nil {
		hostname = value
	}
	return fmt.Sprintf(`<environment>
<operating_system>%s</operating_system>
<shell>%s</shell>
<user>%s</user>
<host>%s</host>
<home>%s</home>
<working_directory>%s</working_directory>
 <temporary_directory>%s</temporary_directory>
 <temporary_directory_variable>CRUDE_TEMP_DIR</temporary_directory_variable>
<platform_guidance>%s</platform_guidance>
</environment>`, XMLText(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)), XMLText(shellName(shell)), XMLText(username),
		XMLText(hostname), XMLText(home), XMLText(workDir), XMLText(tempDir), XMLText(shellGuidance(shell, runtime.GOOS == "windows")))
}

func XMLText(value string) string {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return ""
	}
	return escaped.String()
}

// shellName names the shell that runs commands, as the agent should see it.
func shellName(shell command.Shell) string {
	switch {
	case shell.Unavailable != "":
		return shell.Label + " (not available)"
	case shell.Label != "" && shell.Path != "":
		return shell.Label + " (" + shell.Path + ")"
	case shell.Path != "":
		return shell.Path
	}
	return "unknown"
}

// shellGuidance describes the command language of the shell that actually runs
// the agent's commands. It never names another shell's syntax as the rule.
func shellGuidance(shell command.Shell, windows bool) string {
	if shell.Unavailable != "" {
		return "Commands cannot run: " + shell.Unavailable + " Tell the user; do not retry commands until the shell is fixed."
	}
	paths := ""
	if windows {
		paths = " Paths are case-insensitive."
	}
	switch shell.Syntax {
	case command.SyntaxCmd:
		return "Commands are cmd.exe command lines: %VAR% expands only defined variables, any other % is literal, and for loops use %i. A quoted argument cannot contain a line break: write a multi-line script to a file in the temporary directory and run that file. Use PowerShell only when its behavior is required. Paths are case-insensitive; do not assume Unix-only commands exist."
	case command.SyntaxPowerShell:
		text := "Commands run in " + shell.Label + " as a script file, without the user's profile: write PowerShell, not cmd.exe or bash syntax. Multi-line scripts work. Use $env:NAME for environment variables and ; or a new line between statements. The exit code is the last statement's: a failed native program gives its exit code and a failed cmdlet gives 1. Commands that contain ( ), { }, <# or a lone & need the user's approval, because Crude cannot check the code inside them."
		if strings.EqualFold(shell.ID, "powershell") {
			text += " Windows PowerShell 5.1 has no && or ||; use ; or if ($?) { }. It drops embedded double quotes in arguments to native programs: write them as \\\" or put the value in a file."
		} else {
			text += " Chain with && and || as in bash."
		}
		return text + paths
	}
	if windows {
		return "Commands run in " + shell.Label + ": write bash as on Linux. The command reaches bash byte for byte, so quotes, pipes, $(...), here-documents and multi-line scripts work. GNU tools (grep, sed, awk, find, head, xargs) and Windows programs on PATH both run. Write paths as C:/Users/name, which bash and the file tools both accept. Git Bash converts arguments that look like POSIX paths, such as /d, when it starts a Windows program; put MSYS_NO_PATHCONV=1 before that command when the conversion is wrong." + paths
	}
	return ""
}
