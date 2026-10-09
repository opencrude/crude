//go:build !windows

package corewire

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalLifecyclePrivacyAndOwnership(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, err := ListenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	addr, _ := LocalAddress(dir)
	st, err := os.Stat(addr)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("socket privacy: %v %v", st, err)
	}
	st, err = os.Stat(filepath.Dir(addr))
	if err != nil || st.Mode().Perm() != 0700 {
		t.Fatalf("directory privacy: %v %v", st, err)
	}
	if other, err := ListenLocal(ctx, dir); err == nil {
		_ = other.Close()
		t.Fatal("duplicate listener")
	}
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			_ = c.Close()
		}
		done <- e
	}()
	c, err := DialLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	go func() { _, e := l.Accept(); done <- e }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("accept succeeded after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("accept blocked")
	}
	_ = l.Close()
	if _, err = os.Lstat(addr); !os.IsNotExist(err) {
		t.Fatal("owned socket remains")
	}
	l2, err := ListenLocal(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Close()
}

func TestLocalNeverDeletesStaleOrReplacement(t *testing.T) {
	dir := t.TempDir()
	l, err := ListenLocal(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := LocalAddress(dir)
	if err = os.Rename(addr, addr+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(addr, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if b, e := os.ReadFile(addr); e != nil || string(b) != "replacement" {
		t.Fatal("replacement deleted")
	}
	if l, err = ListenLocal(context.Background(), dir); err == nil {
		_ = l.Close()
		t.Fatal("replaced stale endpoint")
	}
	if _, err = DialLocal(context.Background(), dir); err == nil {
		t.Fatal("dialed non-socket")
	}
}

func TestLocalRejectsPublicOrSymlinkDirectory(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		p := filepath.Join(dir, "corewire")
		if symlink {
			if err := os.Symlink(t.TempDir(), p); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Mkdir(p, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if l, err := ListenLocal(context.Background(), dir); err == nil {
			_ = l.Close()
			t.Fatal("accepted unsafe directory")
		}
	}
}

func TestStaleSocketRefused(t *testing.T) {
	dir := t.TempDir()
	addr, err := privateSocketDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
	if l2, err := ListenLocal(context.Background(), dir); err == nil {
		_ = l2.Close()
		t.Fatal("stale socket replaced")
	}
}
