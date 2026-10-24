package corewire

import (
	"context"
	"testing"
	"time"
)

func TestQUICResourceCloseNotifiesEstablishedPeer(t *testing.T) {
	id, err := LoadIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ListenQUIC("127.0.0.1:0", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	peer, err := DialQUIC(ctx, listener.Addr().String(), id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	server, err := listener.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// No application Serve loop closes this connection for the listener. A
	// resource owner still owes the peer a wire close before releasing UDP.
	if err := listener.StopAccept(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.Done():
	default:
		t.Fatal("resource close left its established carrier running")
	}
	select {
	case <-peer.Done():
	case <-time.After(time.Second):
		t.Fatal("resource close silently discarded UDP before notifying its peer; fallback now waits for the liveness timeout")
	}
}
