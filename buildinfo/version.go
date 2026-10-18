package buildinfo

import (
	"fmt"
	"runtime"
)

// Release builds set these with -ldflags. Development builds remain identifiable
// without consulting Git, opening a profile, or connecting to a core.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func VersionString() string {
	return fmt.Sprintf("crude %s (%s; %s; %s/%s)", Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
