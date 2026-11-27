// Package gitplatform holds the Git options that every Git process Crude
// starts needs on this operating system, so no call site can forget one.
package gitplatform

import "runtime"

// Args returns the "-c" options to put before the Git subcommand.
//
// On Windows, Git opens a path of more than 260 characters only with
// core.longpaths, and deep dependency and build trees have such paths. Go's
// own file operations already support them, so without this option Git would
// fail on files that the rest of Crude reads and writes normally.
func Args() []string {
	if runtime.GOOS == "windows" {
		return []string{"-c", "core.longpaths=true"}
	}
	return nil
}
