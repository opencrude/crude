package fileio

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func identityTestDirectory(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
	default:
		t.Skip("native stable directory identity unsupported on this OS")
	}
	// macOS's temporary directory commonly has /var -> /private/var ancestry.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireDirectoryIdentity(t *testing.T, dir string) string {
	t.Helper()
	id, err := DirectoryIdentity(dir)
	if err != nil {
		t.Fatalf("directory identity: %v", err)
	}
	if id == "" || len(id) > 512 || !utf8.ValidString(id) {
		t.Fatalf("invalid identity: %q", id)
	}
	if strings.Contains(id, dir) {
		t.Fatalf("identity contains the pathname: %q", id)
	}
	return id
}

func TestDirectoryIdentityStableAfterMutationAndReopen(t *testing.T) {
	dir := identityTestDirectory(t)
	before := requireDirectoryIdentity(t, dir)
	for i := 0; i < 4; i++ {
		child := filepath.Join(dir, "child")
		if err := os.Mkdir(child, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(child, "file")
		if err := os.WriteFile(file, []byte("ordinary child mutation"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := requireDirectoryIdentity(t, dir); got != before {
			t.Fatalf("child creation changed identity: %q -> %q", before, got)
		}
		if err := os.RemoveAll(child); err != nil {
			t.Fatal(err)
		}
		if got := requireDirectoryIdentity(t, dir); got != before {
			t.Fatalf("child deletion changed identity: %q -> %q", before, got)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("identity observation left project markers: %v, %v", entries, err)
	}
}

func TestDirectoryIdentityProcessRestart(t *testing.T) {
	dir := identityTestDirectory(t)
	want := requireDirectoryIdentity(t, dir)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A private working directory is the fixture input; no production env switch
	// or process-local identity state is involved.
	command := exec.Command(executable, "-test.run=^TestDirectoryIdentityChildProcess$", "-test.count=1", "-test.v")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "DIRECTORY_IDENTITY="+want+"\n") {
		t.Fatalf("fresh process did not return the same identity: %s", output)
	}
}

func TestDirectoryIdentityChildProcess(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "directory" {
		t.Skip("only runs inside the restart fixture")
	}
	t.Log("DIRECTORY_IDENTITY=" + requireDirectoryIdentity(t, dir))
}

func TestDirectoryIdentitySamePathReplacement(t *testing.T) {
	dir := identityTestDirectory(t)
	before := requireDirectoryIdentity(t, dir)
	moved := dir + "-original"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if got := requireDirectoryIdentity(t, moved); got != before {
		t.Fatalf("rename changed object identity: %q -> %q", before, got)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	replacement := requireDirectoryIdentity(t, dir)
	if replacement == before {
		t.Fatalf("same-path replacement retained identity %q", before)
	}
	seen := map[string]bool{before: true, replacement: true}
	for i := 0; i < 8; i++ {
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		id := requireDirectoryIdentity(t, dir)
		if seen[id] {
			t.Fatalf("delete/recreate reused identity %q", id)
		}
		seen[id] = true
	}
}

func TestDirectoryIdentityRejectsUnsafePaths(t *testing.T) {
	dir := identityTestDirectory(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	separator := string(os.PathSeparator)
	paths := map[string]string{
		"empty": "", "relative": "directory", "file": file,
		"missing":   filepath.Join(dir, "missing"),
		"traversal": dir + separator + ".." + separator + filepath.Base(dir),
		"dot":       dir + separator + ".",
		"nul":       dir + "\x00",
		"too long":  separator + strings.Repeat("x", maxDirectoryIdentityPathBytes),
		"too deep":  separator + strings.Repeat("x"+separator, maxDirectoryIdentityDepth+1),
	}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			id, err := DirectoryIdentity(path)
			if err == nil || id != "" {
				t.Fatalf("unsafe path %q returned identity %q, error %v", path, id, err)
			}
		})
	}
}

func TestDirectoryIdentityRejectsSymlinksAndAncestors(t *testing.T) {
	dir := identityTestDirectory(t)
	link := filepath.Join(filepath.Dir(dir), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(link, "child")} {
		if id, err := DirectoryIdentity(path); err == nil || id != "" {
			t.Fatalf("symlink path %q returned %q, %v", path, id, err)
		}
	}
}

type identityTestHandle struct {
	directoryIdentityHandle
	onBirth func() error
}

func (h identityTestHandle) openChild(name string) (directoryIdentityHandle, error) {
	child, err := h.directoryIdentityHandle.openChild(name)
	if err != nil {
		return nil, err
	}
	return identityTestHandle{child, h.onBirth}, nil
}

func (h identityTestHandle) identity(withBirth bool) (string, error) {
	if withBirth {
		if err := h.onBirth(); err != nil {
			return "", err
		}
	}
	return h.directoryIdentityHandle.identity(withBirth)
}

func identityTestOpener(onBirth func() error) directoryIdentityOpener {
	return func(root string) (directoryIdentityHandle, error) {
		handle, err := openDirectoryIdentityRoot(root)
		if err != nil {
			return nil, err
		}
		return identityTestHandle{handle, onBirth}, nil
	}
}

func TestDirectoryIdentityRejectsReplacementDuringObservation(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		name := "directory"
		if ancestor {
			name = "ancestor"
		}
		t.Run(name, func(t *testing.T) {
			dir := identityTestDirectory(t)
			requireDirectoryIdentity(t, dir)
			target := dir
			if ancestor {
				target = filepath.Dir(dir)
			}
			changed := false
			opener := identityTestOpener(func() error {
				if changed {
					return nil
				}
				changed = true
				moved := target + "-moved"
				if err := os.Rename(target, moved); err != nil {
					return err
				}
				t.Cleanup(func() {
					if err := os.RemoveAll(moved); err != nil {
						t.Error(err)
					}
				})
				if err := os.Mkdir(target, 0700); err != nil {
					return err
				}
				if ancestor {
					// Keep the leaf's native identity unchanged while replacing its
					// parent. Only checking the final directory would miss this.
					return os.Rename(filepath.Join(moved, filepath.Base(dir)), dir)
				}
				return nil
			})
			id, err := directoryIdentity(dir, opener)
			if !changed || err == nil || id != "" || !strings.Contains(err.Error(), "replaced") {
				t.Fatalf("replacement not detected: changed=%v id=%q err=%v", changed, id, err)
			}
		})
	}
}

func TestDirectoryIdentityUnavailableFailsClosed(t *testing.T) {
	dir := identityTestDirectory(t)
	unavailable := errors.New("native creation identity unavailable")
	id, err := directoryIdentity(dir, identityTestOpener(func() error { return unavailable }))
	if id != "" || !errors.Is(err, unavailable) {
		t.Fatalf("unavailable creation identity returned %q, %v", id, err)
	}
	if runtime.GOOS == "linux" {
		// procfs supplies directory inodes but not birth identity. Do not accept
		// a weaker inode-only identity when statx omits STATX_BTIME.
		if _, err := os.Stat("/proc/self"); err != nil {
			t.Skipf("procfs unavailable: %v", err)
		}
		if id, err := DirectoryIdentity("/proc"); id != "" || err == nil || !strings.Contains(err.Error(), "birth time") {
			t.Fatalf("unsupported procfs identity returned %q, %v", id, err)
		}
	}
}

func TestDirectoryIdentityUnsupportedPlatformFailsClosed(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		t.Skip("native stable directory identity supported on this OS")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if id, err := DirectoryIdentity(dir); id != "" || err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported platform returned %q, %v", id, err)
	}
}
