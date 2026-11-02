package testsymlink

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// Create creates a real symlink or fails visibly when the host cannot create one.
func Create(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Fatalf("create symlink: %v; enable Windows Developer Mode or grant symlink privilege", err)
		}
		t.Fatalf("create symlink %q -> %q: %v", newname, oldname, err)
	}
}
