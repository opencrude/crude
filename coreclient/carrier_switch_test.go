package coreclient

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
)

func TestCarrierSwitchRetainsOwnerAndDoesNotReplayCommands(t *testing.T) {
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) {
		reply(s, &coreprotocol.BoolResult{OK: true})
	}}
	c := dial(t, f)
	owner := c.Owner()
	old, err := c.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SwitchCarrier(t.Context(), f.dialer()); err != nil {
		t.Fatal(err)
	}
	if c.Owner() != owner {
		t.Fatal("carrier upgrade replaced session owner")
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("replaced carrier remains open")
	}
	f.mu.Lock()
	hellos := append([]coreprotocol.Hello(nil), f.hellos...)
	f.mu.Unlock()
	if len(hellos) != 2 || hellos[1].Resume == nil || hellos[1].Resume.SessionID != "session" || hellos[1].Build != buildinfo.ProtocolBuild {
		t.Fatalf("upgrade was not an exact-cohort session resume: %+v", hellos)
	}
	var out coreprotocol.BoolResult
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := c.Call(ctx, "workspace", "still-usable", &coreprotocol.Empty{}, &out); err != nil || !out.OK {
		t.Fatal(err, out)
	}
}

func TestCarrierSwitchFailureAndCancelledOwnershipPreserveUsableCarrier(t *testing.T) {
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { reply(s, &coreprotocol.BoolResult{OK: true}) }}
	c := dial(t, f)
	old, err := c.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dialFailure := errors.New("reviewed endpoint unavailable")
	if err := c.SwitchCarrier(t.Context(), func(context.Context) (corewire.Conn, error) { return nil, dialFailure }); !errors.Is(err, dialFailure) {
		t.Fatal(err)
	}
	if err := c.acquireCarrierChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err = c.SwitchCarrier(ctx, func(context.Context) (corewire.Conn, error) { called = true; return nil, dialFailure })
	c.releaseCarrierChange()
	if !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled ownership wait dialed a new endpoint", err)
	}
	current, err := c.current(t.Context())
	if err != nil || current != old {
		t.Fatal("failed upgrade displaced current carrier", err)
	}
	var out coreprotocol.BoolResult
	if err := c.Call(t.Context(), "workspace", "still-usable", &coreprotocol.Empty{}, &out); err != nil || !out.OK {
		t.Fatal(err, out)
	}
}

func TestCloseCancelsInFlightCarrierHandshake(t *testing.T) {
	f := &fakeCore{t: t, handle: func(coreprotocol.Open, []byte, corewire.Stream) {}}
	c := dial(t, f)
	started := make(chan struct{})
	var server *corewire.Mux
	result := make(chan error, 1)
	go func() {
		result <- c.SwitchCarrier(context.Background(), func(context.Context) (corewire.Conn, error) {
			a, b := net.Pipe()
			server = corewire.NewMux(b, corewire.CarrierLocal, false)
			go func() { _, _ = server.AcceptStream(t.Context()); close(started) }()
			return corewire.NewMux(a, corewire.CarrierLocal, true), nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upgrade did not begin handshake")
	}
	defer server.Close()
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for upgrade handshake timeout")
	}
	if err := <-result; err == nil {
		t.Fatal("closed session committed a carrier upgrade")
	}
}
