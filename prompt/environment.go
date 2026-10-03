package prompt

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

func stableEnvironment(workDir string) string {
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	shell := "unknown"
	for _, key := range []string{"SHELL", "COMSPEC"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			shell = value
			break
		}
	}
	if shell == "unknown" && runtime.GOOS == "windows" {
		shell = "cmd.exe"
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
	platform := ""
	if runtime.GOOS == "windows" {
		platform = "Commands run through cmd.exe batch files. Use PowerShell only when its behavior is required. Paths are case-insensitive; do not assume Unix-only commands exist."
	}
	return fmt.Sprintf(`<environment>
<operating_system>%s</operating_system>
<shell>%s</shell>
<user>%s</user>
<host>%s</host>
<home>%s</home>
<working_directory>%s</working_directory>
<platform_guidance>%s</platform_guidance>
</environment>`, XMLText(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)), XMLText(shell), XMLText(username),
		XMLText(hostname), XMLText(home), XMLText(workDir), XMLText(platform))
}

func XMLText(value string) string {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return ""
	}
	return escaped.String()
}
