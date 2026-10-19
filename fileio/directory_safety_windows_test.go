package fileio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsShortDirectory(t *testing.T) {
	long := filepath.Join(t.TempDir(), "ordinary directory with a long name")
	if err := os.Mkdir(long, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		t.Fatalf("short path: %d %v", n, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) {
		t.Skip("8.3 names disabled on this volume")
	}
	if err := CheckDirectoryNoSymlinks(short); err != nil {
		t.Fatalf("ordinary 8.3 directory rejected: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(short)
	if err != nil || strings.EqualFold(resolved, short) {
		t.Fatalf("fixture did not exercise short-name expansion: %q -> %q (%v)", short, resolved, err)
	}
}
