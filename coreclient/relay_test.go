package coreclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

func TestRelaySSHPreservesBufferedControlRemainder(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, b := net.Pipe()
	x, y := net.Pipe()
	client := corewire.NewMux(a, corewire.CarrierSSH, true)
	incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
	local := corewire.NewMux(x, corewire.CarrierLocal, true)
	upstream := corewire.NewMux(y, corewire.CarrierLocal, false)
	defer client.Close()
	defer upstream.Close()
	done := make(chan error, 1)
	go func() { done <- RelaySSH(ctx, incoming, local) }()
	s, err := client.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	hello := coreprotocol.Hello{Schema: coreprotocol.Schema, ClientID: "buffered-control"}
	body, err := coreprotocol.Marshal(&hello)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := corewire.WriteMessage(&wire, body); err != nil {
		t.Fatal(err)
	}
	want := []byte("control bytes buffered after Hello")
	if err := corewire.WriteMessage(&wire, want); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(wire.Bytes()); err != nil {
		t.Fatal(err)
	}
	u, err := upstream.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = u.Close() })
	defer stop()
	r := corewire.NewReader(u, 64<<10)
	body, err = r.Next()
	if err != nil {
		t.Fatal(err)
	}
	var forwarded coreprotocol.Hello
	if err := coreprotocol.Decode(body, &forwarded); err != nil {
		t.Fatal(err)
	}
	if !forwarded.SSHRelay || forwarded.ClientID != hello.ClientID {
		t.Fatal("relay lost narrowing authority or client identity")
	}
	if body, err := r.Next(); err != nil || !bytes.Equal(body, want) {
		t.Fatalf("lost buffered control remainder: %q %v", body, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled relay: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not join buffered-control forwarding")
	}
}

// Exercise the relay with two independent mux links; copying the carrier bytes
// instead of decoded streams would fail because only one link uses compression.
func TestRelaySSHRejectsCredentialAndPairingEscalation(t *testing.T) {
	for _, mode := range []string{"credential", "pairing"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			a, b := net.Pipe()
			x, y := net.Pipe()
			client := corewire.NewMux(a, corewire.CarrierSSH, true)
			incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
			local := corewire.NewMux(x, corewire.CarrierLocal, true)
			upstream := corewire.NewMux(y, corewire.CarrierLocal, false)
			defer client.Close()
			defer upstream.Close()
			done := make(chan error, 1)
			go func() { done <- RelaySSH(ctx, incoming, local) }()
			s, err := client.OpenStream(ctx, corewire.ClassControl)
			if err != nil {
				t.Fatal(err)
			}
			hello := coreprotocol.Hello{Schema: coreprotocol.Schema, ClientID: "relay-escalation"}
			if mode == "credential" {
				hello.Credential = "device:token"
			} else {
				hello.Pair = &coreprotocol.RemotePairRequest{Code: "secret", DeviceName: "attacker"}
			}
			if err := sendValues(s, &hello); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "does not accept") {
					t.Fatalf("relay admitted %s: %v", mode, err)
				}
			case <-ctx.Done():
				t.Fatal("relay did not reject forbidden authentication")
			}
			// No upstream Hello may be published before rejection.
			if s, err := upstream.AcceptStream(ctx); err == nil {
				_ = s.Close()
				t.Fatal("forbidden Hello reached local core")
			}
		})
	}
}

func TestRelaySSHCancellationJoinsBeforeHello(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	a, b := net.Pipe()
	x, y := net.Pipe()
	client := corewire.NewMux(a, corewire.CarrierSSH, true)
	incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
	local := corewire.NewMux(x, corewire.CarrierLocal, true)
	upstream := corewire.NewMux(y, corewire.CarrierLocal, false)
	defer client.Close()
	defer upstream.Close()
	done := make(chan error, 1)
	go func() { done <- RelaySSH(ctx, incoming, local) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled relay: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay leaked waiting for initial control stream")
	}
	select {
	case <-local.Done():
	default:
		t.Fatal("relay retained its local carrier")
	}
	select {
	case <-incoming.Done():
	default:
		t.Fatal("relay retained its SSH carrier")
	}
}

func TestRelaySSHClosedBeforeHelloExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	x, y := net.Pipe()
	client := corewire.NewMux(a, corewire.CarrierSSH, true)
	incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
	local := corewire.NewMux(x, corewire.CarrierLocal, true)
	upstream := corewire.NewMux(y, corewire.CarrierLocal, false)
	defer upstream.Close()
	done := make(chan error, 1)
	go func() { done <- RelaySSH(ctx, incoming, local) }()
	_ = client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("closed carrier before Hello: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("relay did not join after carrier close")
	}
}

// This fault occurs after the valid Hello has already been forwarded. It must
// not be lost behind the other control pump's shutdown/reset error.
func TestRelaySSHReportsMalformedControlAfterHello(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	x, y := net.Pipe()
	client := corewire.NewMux(a, corewire.CarrierSSH, true)
	incoming := corewire.NewMux(b, corewire.CarrierSSH, false)
	local := corewire.NewMux(x, corewire.CarrierLocal, true)
	upstream := corewire.NewMux(y, corewire.CarrierLocal, false)
	defer client.Close()
	defer upstream.Close()
	done := make(chan error, 1)
	go func() { done <- RelaySSH(ctx, incoming, local) }()
	s, err := client.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendValues(s, &coreprotocol.Hello{Schema: coreprotocol.Schema, ClientID: "control-fault"}); err != nil {
		t.Fatal(err)
	}
	u, err := upstream.AcceptStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := corewire.NewReader(u, 64<<10).Next(); err != nil {
		t.Fatal(err)
	}
	// An unterminated message length followed by clean FIN is not clean EOF.
	if _, err := s.Write([]byte{0x80}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("control protocol fault was hidden: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("relay did not join after malformed control")
	}
}
