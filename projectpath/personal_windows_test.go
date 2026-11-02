//go:build windows

package projectpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsKnownPersonalFolders(t *testing.T) {
	if os.Getenv("CRUDE_KNOWN_FOLDER_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsKnownPersonalFolders$", "-test.count=1")
		cmd.Env = append(os.Environ(), "CRUDE_KNOWN_FOLDER_CHILD=1", "USERPROFILE="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated known-folder test: %v\n%s", err, output)
		}
		return
	}
	for _, folder := range []struct {
		name string
		id   *windows.KNOWNFOLDERID
	}{
		{"Profile", windows.FOLDERID_Profile},
		{"Desktop", windows.FOLDERID_Desktop},
		{"Documents", windows.FOLDERID_Documents},
		{"Downloads", windows.FOLDERID_Downloads},
	} {
		t.Run(folder.name, func(t *testing.T) {
			path, err := windows.KnownFolderPath(folder.id, windows.KF_FLAG_DONT_VERIFY)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(os.Getenv("USERPROFILE"), path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			location, err := Inspect(path)
			if err != nil {
				t.Fatal(err)
			}
			if location.Kind != KindPersonal && (folder.name != "Profile" || location.Kind != KindHome) {
				t.Fatalf("known personal folder %q classified as %q", path, location.Kind)
			}
		})
	}
	location, err := Inspect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if location.Kind != "" {
		t.Fatalf("temporary project classified as %q", location.Kind)
	}
}
