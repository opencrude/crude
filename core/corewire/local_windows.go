package corewire

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func addressHash(dir string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(dir)))[:24] }

func currentSID() (string, error) {
	token := windows.GetCurrentProcessToken()
	u, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func LocalAddress(dataDir string) (string, error) {
	dir, err := canonicalDir(dataDir)
	if err != nil {
		return "", err
	}
	sid, err := currentSID()
	if err != nil {
		return "", err
	}
	return `\\.\pipe\crude-core-` + sid + "-" + addressHash(strings.ToLower(dir)), nil
}

// ErrOwned reports that a live core already owns the endpoint.
var ErrOwned = errors.New("corewire: another core owns this data directory")

// ReclaimStale is a no-op on Windows: named pipes vanish with their owner.
func ReclaimStale(ctx context.Context, dataDir string) (bool, error) { return false, ctx.Err() }

func ListenLocal(ctx context.Context, dataDir string) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr, err := LocalAddress(dataDir)
	if err != nil {
		return nil, err
	}
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	// Protected DACL: deny network logons; only the exact user may connect.
	l, err := winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: "D:P(D;;GA;;;NU)(A;;GA;;;" + sid + ")", InputBufferSize: 65536, OutputBufferSize: 65536})
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, ErrOwned
		}
		return nil, err
	}
	return bindListenerContext(ctx, &ownedListener{Listener: l, cleanup: func() error { return nil }}), nil
}

func DialLocal(ctx context.Context, dataDir string) (net.Conn, error) {
	addr, err := LocalAddress(dataDir)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, addr)
}
