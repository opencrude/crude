package mcpclient

import (
	"os"
	"sort"
	"strings"

	captypes "crude/types/capability"
)

// MissingConfiguration checks the inputs actually used by the transport, not
// just explicit overrides. Shell-host environment is checked on that shell.
// An empty header value records a required header awaiting configuration.
func MissingConfiguration(config captypes.ConnectionConfig) []string {
	var missing []string
	if config.ExecutionHost() != "shell" {
		for _, name := range config.RequiredEnv {
			value, explicit := config.Environment[name]
			if !explicit && config.Transport == "stdio" {
				value = os.Getenv(name)
			}
			if strings.TrimSpace(value) == "" && strings.TrimSpace(config.SecretRefs[name]) == "" {
				missing = append(missing, "environment "+name)
			}
		}
	}
	if config.Transport == "http" {
		for name, value := range config.Headers {
			if strings.TrimSpace(value) == "" && strings.TrimSpace(config.SecretRefs[name]) == "" {
				missing = append(missing, "header "+name)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// stdioBaseEnvironment is the complete environment of the host process. A
// stdio server is an authorized, unsandboxed process running with the user's
// privileges — the same authority as an agent shell command, which also
// inherits the full environment. Filtering it through a list of names is not a
// security boundary (the process can read the user's files and sockets
// directly) and cannot be complete: display, session-bus, locale, proxy,
// toolchain and vendor variables are open-ended, and every omission makes some
// server fail at startup. The connection's own environment and resolved secret
// references are appended afterwards and override inherited values.
func stdioBaseEnvironment() []string {
	return os.Environ()
}
