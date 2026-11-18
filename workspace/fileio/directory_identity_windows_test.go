package fileio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDirectoryIdentityWindowsShortName(t *testing.T) {
	dir := identityTestDirectory(t)
	long := filepath.Join(dir, "ordinary directory with a long name")
	if err := os.Mkdir(long, 0700); err != nil {
		t.Fatal(err)
	}
	want := requireDirectoryIdentity(t, long)
	pointer, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || int(n) >= len(buffer) {
		t.Fatalf("short name: %d, %v", n, err)
	}
	short := windows.UTF16ToString(buffer[:n])
	if strings.EqualFold(short, long) {
		t.Skip("8.3 names disabled on this volume")
	}
	if got := requireDirectoryIdentity(t, short); got != want {
		t.Fatalf("short-name alias changed identity: %q -> %q", want, got)
	}
}

func TestDirectoryIdentityWindowsJunction(t *testing.T) {
	dir := identityTestDirectory(t)
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(filepath.Dir(dir), "junction")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{junction, filepath.Join(junction, "child")} {
		if id, err := DirectoryIdentity(path); err == nil || id != "" {
			t.Fatalf("junction path %q returned %q, %v", path, id, err)
		}
	}
}

func TestDirectoryIdentityWindowsRejectsAmbiguousPaths(t *testing.T) {
	dir := identityTestDirectory(t)
	for _, path := range []string{
		dir + ".", dir + " ", dir + ":stream",
		`\\?\` + dir, `\\.\` + dir, `//?/` + filepath.ToSlash(dir),
		`C:directory`, `\directory`,
	} {
		if id, err := DirectoryIdentity(path); err == nil || id != "" {
			t.Fatalf("ambiguous path %q returned %q, %v", path, id, err)
		}
	}
}
