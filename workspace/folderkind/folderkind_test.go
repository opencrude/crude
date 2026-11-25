package folderkind

import (
	"os"
	"path/filepath"
	"testing"

	"crude/internal/testhome"
)

func TestMain(m *testing.M) {
	// testhome sets HOME, but an inherited explicit XDG_CONFIG_HOME must not
	// make these tests read the developer's actual user-dirs configuration.
	if err := os.Unsetenv("XDG_CONFIG_HOME"); err != nil {
		panic(err)
	}
	os.Exit(testhome.Run(m))
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertKind(t *testing.T, path, kind string) Location {
	t.Helper()
	got, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != kind {
		t.Fatalf("Inspect(%q).Kind = %q, want %q", path, got.Kind, kind)
	}
	return got
}

func TestInspectExistingDirectoriesOnly(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	write(t, file, "content")
	for _, path := range []string{"", filepath.Join(base, "missing"), file} {
		if _, err := Inspect(path); err == nil {
			t.Errorf("Inspect(%q) accepted invalid directory", path)
		}
	}
	project := mkdir(t, filepath.Join(base, "MixedCaseProject"))
	got := assertKind(t, project, "")
	want, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("canonical path = %q, want %q", got.Path, want)
	}
	assertKind(t, mkdir(t, filepath.Join(project, "Documents")), "")
	root := filepath.VolumeName(project) + string(filepath.Separator)
	assertKind(t, root, KindRoot)
}
