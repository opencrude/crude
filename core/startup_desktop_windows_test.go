//go:build windows

package core

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func isolatedStartupNavigation(t *testing.T) bool {
	t.Helper()
	if os.Getenv("CRUDE_STARTUP_NAV_CHILD") == "1" {
		return false
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStartupNavigationNeverConstructsWorkspace$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CRUDE_STARTUP_NAV_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated startup navigation: %v\n%s", err, output)
	}
	return true
}

func testDesktop(t *testing.T, _ string) string {
	t.Helper()
	path, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
