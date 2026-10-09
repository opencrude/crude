//go:build !windows

package corewire

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// LocalAddress is deterministic for the canonical existing data directory.
func LocalAddress(dataDir string) (string, error) {
	dir, err := canonicalDir(dataDir)
	if err != nil {
		return "", err
	}
	addr := filepath.Join(dir, "corewire", "core.sock")
	// sockaddr_un holds 104 bytes on Darwin/BSD and 108 on Linux, including NUL.
	if len(addr) >= 104 {
		return "", fmt.Errorf("corewire: socket path %q exceeds the %d-byte Unix socket limit; use a shorter data directory", addr, 103)
	}
	return addr, nil
}

func privateSocketDir(dataDir string) (string, error) {
	addr, err := LocalAddress(dataDir)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(addr)
	if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	u, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm() != 0700 || u.Uid != uint32(os.Geteuid()) {
		return "", errors.New("corewire directory is not private to current user")
	}
	return addr, nil
}

// ErrOwned reports that a live core already owns the data directory's endpoint.
var ErrOwned = errors.New("corewire: another core owns this data directory")

func openOwnerLock(addr string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(filepath.Dir(addr), "owner.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "owner.lock")
	st, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	u, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || u.Uid != uint32(os.Geteuid()) {
		_ = lock.Close()
		return nil, errors.New("invalid corewire owner lock")
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrOwned
		}
		return nil, err
	}
	return lock, nil
}

// ReclaimStale removes a crash-left socket node. It succeeds only while it holds
// the exclusive owner lock (so no live core can exist), the node is a socket owned
// by this user, and nothing answers on it. A live owner yields ErrOwned and its
// endpoint is never touched. It reports whether a stale node was removed.
func ReclaimStale(ctx context.Context, dataDir string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	addr, err := privateSocketDir(dataDir)
	if err != nil {
		return false, err
	}
	lock, err := openOwnerLock(addr)
	if err != nil {
		return false, err
	}
	defer func() { _ = lock.Close() }()
	st, err := os.Lstat(addr)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	u, ok := st.Sys().(*syscall.Stat_t)
	if !ok || st.Mode()&os.ModeSocket == 0 || u.Uid != uint32(os.Geteuid()) {
		return false, errors.New("corewire endpoint is not a socket owned by the current user; refusing removal")
	}
	if c, derr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", addr); derr == nil {
		_ = c.Close()
		return false, errors.New("corewire endpoint answers although no owner holds the lock; refusing removal")
	}
	if err = os.Remove(addr); err != nil {
		return false, err
	}
	return true, nil
}

// ListenLocal refuses existing sockets, including stale ones. It never guesses
// that another owner's endpoint is safe to delete. Stale crash cleanup is an
// explicit administrative operation while no listener owns the lock.
func ListenLocal(ctx context.Context, dataDir string) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr, err := privateSocketDir(dataDir)
	if err != nil {
		return nil, err
	}
	lock, err := openOwnerLock(addr)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(addr); !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, errors.New("corewire endpoint already exists; refusing replacement")
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	l.SetUnlinkOnClose(false)
	identity, err := os.Lstat(addr)
	if err == nil {
		err = os.Chmod(addr, 0600)
	}
	if err != nil {
		_ = l.Close()
		_ = lock.Close()
		return nil, err
	}
	return bindListenerContext(ctx, &ownedListener{Listener: l, cleanup: func() error {
		// Never unlink a replacement node. The lock file is deliberately persistent.
		var e error
		current, statErr := os.Lstat(addr)
		if statErr == nil && os.SameFile(identity, current) {
			e = os.Remove(addr)
		}
		closeErr := lock.Close()
		if e == nil {
			e = closeErr
		}
		return e
	}}), nil
}

func DialLocal(ctx context.Context, dataDir string) (net.Conn, error) {
	addr, err := privateSocketDir(dataDir)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(addr)
	if err != nil {
		return nil, err
	}
	u, ok := st.Sys().(*syscall.Stat_t)
	if !ok || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0600 || u.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("corewire endpoint is not private to current user")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", addr)
}
