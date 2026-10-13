package mcpclient

import "os"

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
