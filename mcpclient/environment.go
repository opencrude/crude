package mcpclient

import (
	"os"
	"runtime"
)

// stdioInheritedVariables is the non-secret process baseline a stdio server
// needs to start, matching the MCP reference SDK's default inheritance. Without
// SYSTEMROOT/TEMP/USERPROFILE, Windows servers cannot open sockets or create
// temporary files. Everything else comes only from the explicit connection
// environment and secret references.
func stdioInheritedVariables() []string {
	if runtime.GOOS == "windows" {
		return []string{"APPDATA", "HOMEDRIVE", "HOMEPATH", "LOCALAPPDATA", "PATH", "PATHEXT", "PROCESSOR_ARCHITECTURE",
			"PROGRAMFILES", "SYSTEMDRIVE", "SYSTEMROOT", "TEMP", "TMP", "USERNAME", "USERPROFILE", "WINDIR"}
	}
	return []string{"HOME", "LOGNAME", "PATH", "SHELL", "TERM", "TMPDIR", "USER"}
}

func stdioBaseEnvironment() []string {
	var environment []string
	for _, name := range stdioInheritedVariables() {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
