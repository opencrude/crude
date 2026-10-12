package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStageCoreImageIsContentAddressedAndPrunesOtherBuilds(t *testing.T) {
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
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("image of the previous build was not pruned: %v", err)
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
	// The stdio bridge starts a core and exits once its (empty) stdin closes,
	// exactly like a shell process that started the core and quit.
	cmd := exec.CommandContext(ctx, starter, "--core-stdio")
	cmd.Stdin = bytes.NewReader(nil)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starter: %v: %s", err, output)
	}
	status, err := CoreStatus(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(status.Executable) != CoreImageDir(home) {
		t.Fatalf("core runs from %s, not a staged image under %s", status.Executable, CoreImageDir(home))
	}
	if err := os.Remove(starter); err != nil {
		t.Fatalf("the running core still pins the executable that started it: %v", err)
	}
	if _, err := CoreStatus(ctx, home); err != nil {
		t.Fatalf("core did not survive removal of the starting executable: %v", err)
	}
}
