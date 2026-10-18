package buildinfo

import (
	"runtime"
	"testing"
)

func TestVersionString(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })
	Version, Commit, Date = "v0.1.0-rc.1", "0123456789abcdef", "2026-10-18T00:00:00Z"
	want := "crude v0.1.0-rc.1 (0123456789abcdef; 2026-10-18T00:00:00Z; " + runtime.GOOS + "/" + runtime.GOARCH + ")"
	if got := VersionString(); got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}
