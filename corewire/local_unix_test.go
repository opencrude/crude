//go:build !windows

package corewire

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalUnpublishedSocketDialsSeeAbsentThenPrivate(t *testing.T) {
	dir := t.TempDir()
	addr, err := privateSocketDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, temporary, identity, err := bindUnpublishedSocket(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	defer func() { _ = removeSocketIdentity(addr, identity); _ = removeSocketIdentity(temporary, identity) }()
	// Deterministic version of the old bind-before-chmod interval. No global
	// umask or mutable production test hook is needed.
	if err := os.Chmod(temporary, 0755); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if c, err := DialLocal(t.Context(), dir); !errors.Is(err, os.ErrNotExist) {
			if c != nil {
				c.Close()
			}
			t.Fatalf("unpublished socket visible: %v", err)
		}
	}
	stop := make(chan struct{})
	fail := make(chan error, 1)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, err := DialLocal(t.Context(), dir)
				if c != nil {
					c.Close()
				}
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					select {
					case fail <- err:
					default:
					}
					return
				}
			}
		}()
	}
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	if err := publishSocket(t.Context(), temporary, addr, identity); err != nil {
		close(stop)
		l.Close()
		wg.Wait()
		t.Fatal(err)
	}
	for range 20 {
		c, err := DialLocal(t.Context(), dir)
		if err != nil {
			t.Error(err)
			break
		}
		c.Close()
	}
	close(stop)
	wg.Wait()
	l.Close()
	<-accepted
	select {
	case err := <-fail:
		t.Fatal("dial observed publication permissions", err)
	default:
	}
	st, err := os.Lstat(addr)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("publication privacy %v %v", st, err)
	}
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary bind remains", err)
	}
}

func TestLocalLegacyBindBeforeChmodReproducesPrivacyRefusal(t *testing.T) {
	dir := t.TempDir()
	addr, err := privateSocketDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Independently reproduce the former publication sequence while paused
	// before chmod. This documents why accepting/retrying unsafe mode is wrong.
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(addr, 0755); err != nil {
		t.Fatal(err)
	}
	if c, err := DialLocal(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "not private") {
		if c != nil {
			c.Close()
		}
		t.Fatalf("legacy failure not reproduced: %v", err)
	}
}

func TestLocalSocketPublicationNoReplaceAndCancellation(t *testing.T) {
	for _, mode := range []string{"replacement", "cancelled", "crash-left"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			addr, err := privateSocketDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			l, tmp, id, err := bindUnpublishedSocket(addr)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			defer func() { _ = removeSocketIdentity(tmp, id); _ = removeSocketIdentity(addr, id) }()
			ctx := t.Context()
			if mode == "replacement" {
				if err := os.WriteFile(addr, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if mode == "crash-left" {
				old, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				old.SetUnlinkOnClose(false)
				old.Close()
			}
			if err := publishSocket(ctx, tmp, addr, id); err == nil {
				t.Fatal("unsafe publication succeeded")
			}
			if mode == "replacement" {
				raw, err := os.ReadFile(addr)
				if err != nil || string(raw) != "keep" {
					t.Fatal("replacement changed", err)
				}
			}
			if mode == "cancelled" {
				if _, err := os.Lstat(addr); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cancelled endpoint published", err)
				}
			}
			if mode == "crash-left" {
				if ok, err := ReclaimStale(t.Context(), dir); err != nil || !ok {
					t.Fatal("crash-left reclaim failed", ok, err)
				}
			}
		})
	}
}

func TestLocalPublicationCanonicalAddressAtPathLimit(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "cs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, strings.Repeat("x", 103-len(base)-len("/corewire/core.sock")-1))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	addr, err := LocalAddress(dir)
	if err != nil || len(addr) != 103 {
		t.Fatalf("boundary address %q %v", addr, err)
	}
	l, err := ListenLocal(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Addr().String() != addr {
		t.Fatal("temporary bind address leaked", l.Addr())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(addr))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "owner.lock" {
			t.Fatal("socket node leaked", entry.Name())
		}
	}
}

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
