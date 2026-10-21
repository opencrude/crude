package projectpath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestInspectAndBrowseAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require host privilege on Windows")
	}
	base := t.TempDir()
	project := mkdir(t, filepath.Join(base, "Project"))
	link := filepath.Join(base, "Alias")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}
	got := assertKind(t, link, "")
	want, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("alias resolves to %q, want %q", got.Path, want)
	}
	write(t, filepath.Join(base, "file"), "")
	for name, target := range map[string]string{"fileLink": "file", "broken": "missing", "loop": "loop"} {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Browse(context.Background(), base, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated || result.NextOffset != -1 || len(result.Entries) != 2 {
		t.Fatalf("unexpected directory page: %+v", result)
	}
	for _, entry := range result.Entries {
		if !entry.IsDir || entry.Path != filepath.Join(result.Path, entry.Name) || entry.IsSymlink != (entry.Name == "Alias") {
			t.Errorf("invalid entry: %+v", entry)
		}
	}
}

func TestBrowsePages(t *testing.T) {
	base := t.TempDir()
	const count = 1037
	for i := 0; i < count; i++ {
		mkdir(t, filepath.Join(base, fmt.Sprintf("d%04d", i)))
	}
	seen := map[string]bool{}
	offset := 0
	for page := 0; ; page++ {
		if page > 4 {
			t.Fatal("pagination did not finish")
		}
		result, err := Browse(context.Background(), base, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) > 512 {
			t.Fatalf("page exceeds 512 entries: %d", len(result.Entries))
		}
		for _, entry := range result.Entries {
			if seen[entry.Name] {
				t.Fatalf("duplicate entry %q", entry.Name)
			}
			seen[entry.Name] = true
		}
		if result.NextOffset == -1 {
			if result.Truncated {
				t.Fatal("small directory hit scan ceiling")
			}
			break
		}
		if !result.Truncated || result.NextOffset <= offset {
			t.Fatalf("invalid continuation: %+v", result)
		}
		offset = result.NextOffset
	}
	if len(seen) != count {
		t.Fatalf("saw %d directories, want %d", len(seen), count)
	}
}

func TestBrowseMixedOffsetAndInvalidPaths(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < 400; i++ {
		path := filepath.Join(base, fmt.Sprintf("entry-%04d", i))
		if i%3 == 0 {
			mkdir(t, path)
		} else {
			write(t, path, "")
		}
	}
	// Obtain the filesystem's raw order independently of Browse. An offset must
	// skip files as well as directories, including across internal read batches.
	f, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.ReadDir(400)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("fixture read: %v, close: %v", err, closeErr)
	}
	const offset = 133
	var want []string
	for _, entry := range raw[offset:] {
		if entry.IsDir() {
			want = append(want, entry.Name())
		}
	}
	result, err := Browse(context.Background(), base, offset)
	if err != nil || result.Truncated || result.NextOffset != -1 || len(result.Entries) != len(want) {
		t.Fatalf("mixed offset page: %+v, %v", result, err)
	}
	for i, name := range want {
		if result.Entries[i].Name != name {
			t.Fatalf("entry %d = %q, want %q", i, result.Entries[i].Name, name)
		}
	}
	empty := mkdir(t, filepath.Join(base, "empty"))
	for _, offset := range []int{0, 10} {
		result, err := Browse(context.Background(), empty, offset)
		if err != nil || len(result.Entries) != 0 || result.Truncated || result.NextOffset != -1 {
			t.Fatalf("empty directory page: %+v, %v", result, err)
		}
	}
	for _, path := range []string{"", filepath.Join(base, "missing"), filepath.Join(base, "entry-0001")} {
		if _, err := Browse(context.Background(), path, 0); err == nil {
			t.Errorf("Browse accepted invalid path %q", path)
		}
	}
}

func TestBrowseHugeFlatDirectoryAndOffset(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < 100003; i++ {
		write(t, filepath.Join(base, fmt.Sprintf("f%06d", i)), "")
	}
	for _, offset := range []int{0, 99999, 100000} {
		result, err := Browse(context.Background(), base, offset)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 0 || !result.Truncated || result.NextOffset != -1 {
			t.Fatalf("flat directory offset %d: %+v", offset, result)
		}
	}
	for _, offset := range []int{-1, 100001, int(^uint(0) >> 1)} {
		if _, err := Browse(context.Background(), base, offset); err == nil {
			t.Errorf("accepted invalid offset %d", offset)
		}
	}
}

func TestBrowseCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Browse(ctx, "missing", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Browse: %v", err)
	}
	base := t.TempDir()
	for i := 0; i < 300; i++ {
		write(t, filepath.Join(base, fmt.Sprint(i)), "")
	}
	// A deterministic context cancels during iteration rather than racing a fast
	// directory read with a goroutine or relying on a timing assertion.
	if _, err := Browse(&countingContext{Context: context.Background()}, base, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-scan cancellation: %v", err)
	}
}

type countingContext struct {
	context.Context
	calls int
}

func (c *countingContext) Err() error {
	c.calls++
	if c.calls > 150 {
		return context.Canceled
	}
	return nil
}

func TestReadOnlyAndNoWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	base := t.TempDir()
	project := mkdir(t, filepath.Join(base, "ReadOnly"))
	mkdir(t, filepath.Join(project, "child"))
	if err := os.Chmod(project, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(project, 0700) })
	assertKind(t, project, "")
	result, err := Browse(context.Background(), project, 0)
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Name != "child" {
		t.Fatalf("read-only Browse: %+v, %v", result, err)
	}
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) != 1 {
		t.Fatalf("inspection wrote project files: %v, %v", entries, err)
	}
}
