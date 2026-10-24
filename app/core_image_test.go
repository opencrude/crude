package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Two callers can both observe a missing image before either finishes copying.
// The later copy must reuse the first published inode, not rename over it.
func TestCopyCoreImageDoesNotReplacePublishedImage(t *testing.T) {
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target, err := stageCoreImage(home, exe, "immutable")
	if err != nil {
		t.Fatal(err)
	}
	winner, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	if err := copyCoreImage(source, CoreImageDir(home), target); err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(winner, current) {
		t.Fatal("a losing copy replaced the published executable inode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, "-test.run=^$")
	cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute the published winner: %v: %s", err, output)
	}
	entries, err := os.ReadDir(CoreImageDir(home))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Fatalf("copy left staging files: %v", entries)
	}
}

// Separate shells share no Go mutex. Force each process down the copy path as
// though it observed a cache miss before another caller published the winner.
func TestCopyCoreImageAcrossProcesses(t *testing.T) {
	if home := os.Getenv("CRUDE_TEST_COPY_CORE_IMAGE"); home != "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = source.Close() }()
		target := filepath.Join(CoreImageDir(home), coreImagePrefix+"processes"+filepath.Ext(exe))
		if err := copyCoreImage(source, CoreImageDir(home), target); err != nil {
			t.Fatal(err)
		}
		return
	}
	home := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target, err := stageCoreImage(home, exe, "processes")
	if err != nil {
		t.Fatal(err)
	}
	winner, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const shells = 6
	outputs := make([][]byte, shells)
	errs := make([]error, shells)
	var wg sync.WaitGroup
	for i := range shells {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCopyCoreImageAcrossProcesses$")
			cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=0", "CRUDE_TEST_COPY_CORE_IMAGE="+home)
			outputs[i], errs[i] = cmd.CombinedOutput()
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("shell %d: %v: %s", i, err, outputs[i])
		}
	}
	current, err := os.Stat(target)
	if err != nil || !os.SameFile(winner, current) {
		t.Fatalf("concurrent processes replaced the published winner: %v", err)
	}
	// Execute after all losing copies; retained core startup must stay usable.
	cmd := exec.CommandContext(ctx, target, "-test.run=^$")
	cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute after concurrent copies: %v: %s", err, output)
	}
}

func TestCoreImageRejectsInvalidPublishedWinner(t *testing.T) {
	for _, kind := range []string{"truncated", "directory", "symlink", "not executable"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "not executable" && runtime.GOOS == "windows" {
				t.Skip("Windows executable access does not use Unix mode bits")
			}
			home := t.TempDir()
			source := filepath.Join(t.TempDir(), "crude.exe")
			if err := os.WriteFile(source, []byte("complete build"), 0o700); err != nil {
				t.Fatal(err)
			}
			dir := CoreImageDir(home)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "crude-core-invalid.exe")
			var err error
			switch kind {
			case "truncated":
				err = os.WriteFile(target, []byte("partial"), 0o700)
			case "directory":
				err = os.Mkdir(target, 0o700)
			case "symlink":
				err = os.Symlink(source, target)
				if err != nil && runtime.GOOS == "windows" {
					t.Skipf("symlink privilege unavailable: %v", err)
				}
			case "not executable":
				err = os.WriteFile(target, []byte("complete build"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stageCoreImage(home, source, "invalid"); err == nil {
				t.Fatal("staging accepted or replaced an invalid published image")
			}
			in, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = in.Close() }()
			if err := copyCoreImage(in, dir, target); err == nil {
				t.Fatal("a losing copy accepted or replaced an invalid winner")
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() {
				t.Fatalf("invalid image was mutated: before=%v after=%v err=%v", before, after, err)
			}
		})
	}
}

func TestStageCoreImageRejectsSameSizeChangedBytes(t *testing.T) {
	home, source := t.TempDir(), filepath.Join(t.TempDir(), "crude.exe")
	if err := os.WriteFile(source, []byte("first build"), 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := stageCoreImage(home, source, "immutable")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("other build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := stageCoreImage(home, source, "immutable"); err == nil {
		t.Fatal("staging trusted size instead of exact executable bytes")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "other build" {
		t.Fatalf("invalid winner was overwritten: %q %v", raw, err)
	}
}

func TestStageCoreImageIsContentAddressedAndRetainsOtherBuilds(t *testing.T) {
	home, source := t.TempDir(), filepath.Join(t.TempDir(), "crude.exe")
	if err := os.WriteFile(source, []byte("first build"), 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := stageCoreImage(home, source, "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != CoreImageDir(home) || filepath.Base(first) != "crude-core-aaaa.exe" {
		t.Fatalf("staged image %s is not content-addressed under %s", first, CoreImageDir(home))
	}
	if raw, _ := os.ReadFile(first); !bytes.Equal(raw, []byte("first build")) {
		t.Fatalf("staged bytes = %q", raw)
	}
	again, err := stageCoreImage(home, source, "aaaa")
	if err != nil || again != first {
		t.Fatalf("restaging the same build = %s, %v", again, err)
	}
	if err := os.WriteFile(source, []byte("second build"), 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := stageCoreImage(home, source, "bbbb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("image of a possibly running owner was removed: %v", err)
	}
	if raw, _ := os.ReadFile(second); !bytes.Equal(raw, []byte("second build")) {
		t.Fatalf("second staged bytes = %q", raw)
	}
	if _, err := stageCoreImage(home, source, "unknown"); err == nil {
		t.Fatal("an unidentified executable was staged")
	}
}

// `go run .` deletes its temporary executable when the shell exits, and a
// rebuild overwrites crude.exe, while the detached core keeps running. The core
// must therefore never run from (and pin) the image that started it.
func TestDetachedCoreDoesNotPinTheStartingExecutable(t *testing.T) {
	home := coreTestHome(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	starter := filepath.Join(t.TempDir(), "crude"+filepath.Ext(self))
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(starter, os.O_CREATE|os.O_WRONLY, 0o700)
	if err == nil {
		_, err = io.Copy(out, in)
		err = errors.Join(err, out.Close())
	}
	_ = in.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A genuine SSH session starts the core and closes cleanly, exactly like a
	// shell process that started the core and quit. Empty input admits no session.
	_, closeRelay := startCoreStdioClient(t, ctx, starter)
	closeRelay()
	status, err := CoreStatus(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(status.Executable) != CoreImageDir(home) {
		t.Fatalf("core runs from %s, not a staged image under %s", status.Executable, CoreImageDir(home))
	}
	owner, err := readCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Stat(status.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCoreImage(status.Executable, owner.Digest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(starter); err != nil {
		t.Fatalf("the running core still pins the executable that started it: %v", err)
	}
	if _, err := CoreStatus(ctx, home); err != nil {
		t.Fatalf("core did not survive removal of the starting executable: %v", err)
	}
	current, err := os.Stat(status.Executable)
	if err != nil || !os.SameFile(image, current) {
		t.Fatalf("retained image identity changed: %v", err)
	}
	if err := verifyCoreImage(status.Executable, owner.Digest); err != nil {
		t.Fatalf("retained image bytes changed: %v", err)
	}
}
