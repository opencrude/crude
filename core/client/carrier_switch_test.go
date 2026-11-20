package coreclient

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/core/protocol"
	"crude/core/wire"
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
	if err := c.SwitchCarrier(t.Context(), f.dialer(), f.dialer()); err != nil {
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
	if err := c.SwitchCarrier(t.Context(), func(context.Context) (corewire.Conn, error) { return nil, dialFailure }, f.dialer()); !errors.Is(err, dialFailure) {
		t.Fatal(err)
	}
	if err := c.acquireCarrierChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err = c.SwitchCarrier(ctx, func(context.Context) (corewire.Conn, error) { called = true; return nil, dialFailure }, f.dialer())
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

// The upgrade attempt and the later reconnect policy are separate: an SSH
// session must not re-dial SSH just to try a direct carrier, and only a
// successful switch installs the policy that later carrier loss uses.
func TestCarrierSwitchInstallsReconnectPolicyOnlyOnSuccess(t *testing.T) {
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { reply(s, &coreprotocol.BoolResult{OK: true}) }}
	c := dial(t, f)
	var reconnects atomic.Int32
	reconnect := func(ctx context.Context) (corewire.Conn, error) { reconnects.Add(1); return f.dialer()(ctx) }
	failed := func(context.Context) (corewire.Conn, error) { return nil, errors.New("direct carrier unreachable") }
	if err := c.SwitchCarrier(t.Context(), failed, reconnect); err == nil {
		t.Fatal("unreachable upgrade reported success")
	}
	if reconnects.Load() != 0 {
		t.Fatal("a failed upgrade dialed the reconnect policy")
	}
	call := func() {
		t.Helper()
		var out coreprotocol.BoolResult
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := c.Call(ctx, "workspace", "still-usable", &coreprotocol.Empty{}, &out); err != nil || !out.OK {
			t.Fatal(err, out)
		}
	}
	f.dropCarriers()
	call()
	if reconnects.Load() != 0 {
		t.Fatal("a failed upgrade replaced the session's reconnect policy")
	}
	if err := c.SwitchCarrier(t.Context(), f.dialer(), reconnect); err != nil {
		t.Fatal(err)
	}
	if reconnects.Load() != 0 {
		t.Fatal("the upgrade attempt dialed the reconnect policy")
	}
	f.dropCarriers()
	call()
	if reconnects.Load() == 0 {
		t.Fatal("carrier loss after an upgrade did not use the installed reconnect policy")
	}
}

// A background upgrade never delays the session: calls run on the current
// carrier while the attempt is still dialing, and Close cancels and joins it.
func TestUpgradeInBackgroundDoesNotBlockSessionAndCloseJoinsIt(t *testing.T) {
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { reply(s, &coreprotocol.BoolResult{OK: true}) }}
	c := dial(t, f)
	before, err := c.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dialing, ended := make(chan struct{}), make(chan struct{})
	stalled := func(ctx context.Context) (corewire.Conn, error) {
		close(dialing)
		<-ctx.Done() // an unreachable endpoint that never answers
		close(ended)
		return nil, ctx.Err()
	}
	c.UpgradeInBackground(func(context.Context) (Dialer, Dialer, error) { return stalled, f.dialer(), nil })
	select {
	case <-dialing:
	case <-time.After(time.Second):
		t.Fatal("background upgrade did not start")
	}
	var out coreprotocol.BoolResult
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := c.Call(ctx, "workspace", "still-usable", &coreprotocol.Empty{}, &out); err != nil || !out.OK {
		t.Fatal("a pending upgrade blocked the session", err, out)
	}
	if now, err := c.current(t.Context()); err != nil || now != before {
		t.Fatal("a pending upgrade replaced the carrier", err)
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the upgrade attempt timeout")
	}
	select {
	case <-ended:
	default:
		t.Fatal("Close returned before joining the upgrade attempt")
	}
}

// A successful background upgrade resumes the same session on the new carrier.
func TestUpgradeInBackgroundSwitchesCarrier(t *testing.T) {
	f := &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) { reply(s, &coreprotocol.BoolResult{OK: true}) }}
	c := dial(t, f)
	owner := c.Owner()
	before, err := c.current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c.UpgradeInBackground(func(context.Context) (Dialer, Dialer, error) { return f.dialer(), f.dialer(), nil })
	select {
	case <-before.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("background upgrade did not replace the carrier")
	}
	if c.Owner() != owner {
		t.Fatal("background upgrade replaced the session owner")
	}
	var out coreprotocol.BoolResult
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := c.Call(ctx, "workspace", "still-usable", &coreprotocol.Empty{}, &out); err != nil || !out.OK {
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
		}, f.dialer())
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
