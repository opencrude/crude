package agent

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

type envContext struct {
	Shell    string
	OSInfo   string
	CWD      string
	Username string
	Hostname string
	HomeDir  string
}

func gatherStableEnvContextInDir(dir string) envContext {
	if dir == "" {
		dir = getCWD()
	}
	context := envContext{
		Shell: detectShell(), OSInfo: fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		CWD: dir, HomeDir: detectHomeDir(), Username: "unknown", Hostname: "unknown",
	}
	if current, err := user.Current(); err == nil {
		context.Username = current.Username
	}
	if hostname, err := os.Hostname(); err == nil {
		context.Hostname = hostname
	}
	return context
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

func platformGuidance() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return "Commands run through cmd.exe batch files. Use PowerShell only when its behavior is required. Paths are case-insensitive; do not assume Unix-only commands exist."
}

func (e envContext) buildStableSection() string {
	return fmt.Sprintf(`<environment>
<operating_system>%s</operating_system>
<shell>%s</shell>
<user>%s</user>
<host>%s</host>
<home>%s</home>
<working_directory>%s</working_directory>
<platform_guidance>%s</platform_guidance>
</environment>`, xmlText(e.OSInfo), xmlText(e.Shell), xmlText(e.Username),
		xmlText(e.Hostname), xmlText(e.HomeDir), xmlText(e.CWD), xmlText(platformGuidance()))
}

func xmlText(value string) string {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return ""
	}
	return escaped.String()
}

func getCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
