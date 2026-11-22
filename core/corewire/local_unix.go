//go:build !windows

package corewire

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	l, temporary, identity, err := bindUnpublishedSocket(addr)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	cleanup := func() error {
		return errors.Join(removeSocketIdentity(addr, identity), removeSocketIdentity(temporary, identity), lock.Close())
	}
	if err := publishSocket(ctx, temporary, addr, identity); err != nil {
		_ = l.Close()
		return nil, errors.Join(err, cleanup())
	}
	return bindListenerContext(ctx, &ownedListener{Listener: &publishedUnixListener{UnixListener: l, address: addr}, cleanup: cleanup}), nil
}

// Binding first publishes only a random, noncanonical name inside the private
// owner directory. Eight hex characters are shorter than "core.sock", so the
// canonical 103-byte address limit also bounds the temporary bind address.
func bindUnpublishedSocket(addr string) (*net.UnixListener, string, os.FileInfo, error) {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, "", nil, err
	}
	temporary := filepath.Join(filepath.Dir(addr), hex.EncodeToString(nonce[:]))
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: temporary, Net: "unix"})
	if err != nil {
		return nil, "", nil, err
	}
	l.SetUnlinkOnClose(false)
	identity, err := os.Lstat(temporary)
	if err != nil {
		_ = l.Close()
		return nil, "", nil, err
	}
	return l, temporary, identity, nil
}

// Hard-link publication is atomic and no-replace. A platform/filesystem which
// cannot link its Unix socket fails explicitly; never fall back to replacing a
// path or publishing a socket before its private mode is established.
func publishSocket(ctx context.Context, temporary, addr string, identity os.FileInfo) error {
	current, err := os.Lstat(temporary)
	if err != nil {
		return err
	}
	u, ok := current.Sys().(*syscall.Stat_t)
	if !ok || !os.SameFile(identity, current) || current.Mode()&os.ModeSocket == 0 || u.Uid != uint32(os.Geteuid()) {
		return errors.New("unpublished corewire socket identity changed")
	}
	if err := os.Chmod(temporary, 0600); err != nil {
		return err
	}
	current, err = os.Lstat(temporary)
	if err != nil {
		return err
	}
	if !os.SameFile(identity, current) || current.Mode().Perm() != 0600 {
		return errors.New("unpublished corewire socket privacy changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(temporary, addr); err != nil {
		return fmt.Errorf("publish private corewire socket without replacement: %w", err)
	}
	return removeSocketIdentity(temporary, identity)
}

func removeSocketIdentity(path string, identity os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if identity != nil && os.SameFile(identity, current) {
		return os.Remove(path)
	}
	return nil // a replacement node belongs to someone else
}

type publishedUnixListener struct {
	*net.UnixListener
	address string
}

func (l *publishedUnixListener) Addr() net.Addr { return &net.UnixAddr{Name: l.address, Net: "unix"} }

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
