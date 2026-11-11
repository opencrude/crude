package core

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
)

// gatedCarrier holds each command's final reply on the core side until the
// test releases it, so a caller can abandon an admitted command before its
// result exists. Admission replies are never held.
type gatedCarrier struct {
	corewire.Conn
	gate *atomic.Pointer[chan struct{}]
}

func (c *gatedCarrier) AcceptStream(ctx context.Context) (corewire.Stream, error) {
	s, err := c.Conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return &gatedStream{Stream: s, gate: c.gate}, nil
}

type gatedStream struct {
	corewire.Stream
	gate   *atomic.Pointer[chan struct{}]
	open   coreprotocol.Open
	opened bool
	writes int
}

func (s *gatedStream) Read(b []byte) (int, error) {
	n, err := s.Stream.Read(b)
	if !s.opened && n > 0 {
		s.opened = true
		if _, prefix := binary.Uvarint(b[:n]); prefix > 0 {
			_, _ = coreprotocol.DecodePrefix(b[prefix:n], &s.open)
		}
	}
	return n, err
}

func (s *gatedStream) Write(b []byte) (int, error) {
	if s.open.Kind == coreprotocol.StreamCommand && s.open.Method == coreprotocol.CoreStatusMethod {
		s.writes++
		if s.writes == 2 { // Admitted, then Final
			if gate := s.gate.Load(); gate != nil {
				<-*gate
			}
		}
	}
	return s.Stream.Write(b)
}

// admissionSignal reports each command admission the client reads.
type admissionSignal struct {
	corewire.Conn
	admitted chan<- struct{}
}

func (c *admissionSignal) OpenStream(ctx context.Context, class corewire.Class) (corewire.Stream, error) {
	s, err := c.Conn.OpenStream(ctx, class)
	if err != nil {
		return nil, err
	}
	return &admissionStream{Stream: s, admitted: c.admitted}, nil
}

type admissionStream struct {
	corewire.Stream
	admitted chan<- struct{}
	open     coreprotocol.Open
	opened   bool
	signaled bool
}

func (s *admissionStream) Write(b []byte) (int, error) {
	if !s.opened {
		s.opened = true
		if _, n := binary.Uvarint(b); n > 0 {
			_, _ = coreprotocol.DecodePrefix(b[n:], &s.open)
		}
	}
	return s.Stream.Write(b)
}

func (s *admissionStream) Read(b []byte) (int, error) {
	n, err := s.Stream.Read(b)
	if n > 0 && !s.signaled && s.open.Kind == coreprotocol.StreamCommand && s.open.Method == coreprotocol.CoreStatusMethod {
		s.signaled = true
		s.admitted <- struct{}{}
	}
	return n, err
}

func retainedCommands(r *Runtime, client *coreclient.Client) int {
	r.mu.Lock()
	c := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	c.commandMu.Lock()
	defer c.commandMu.Unlock()
	return len(c.commands)
}

func awaitRetained(t *testing.T, r *Runtime, client *coreclient.Client, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for retainedCommands(r, client) != want {
		if time.Now().After(deadline) {
			t.Fatalf("core retains %d command results, want %d", retainedCommands(r, client), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A caller that abandons an admitted command must not pin one of the
// session's few retained-result slots. Before the fix every cancelled call
// stayed retained until the session ended, and the seventeenth refused every
// further command as busy.
func TestAbandonedCommandsReleaseRetainedResults(t *testing.T) {
	r := transportRuntime(t)
	var gate atomic.Pointer[chan struct{}]
	admitted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	client, err := coreclient.Dial(ctx, func(context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ServeCarrier(ctx, r, &gatedCarrier{Conn: corewire.NewMux(b, corewire.CarrierLocal, false), gate: &gate}, nil)
		}()
		return &admissionSignal{Conn: corewire.NewMux(a, corewire.CarrierLocal, true), admitted: admitted}, nil
	}, "abandoning-client")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); cancel(); wg.Wait() })

	for i := range sessionCommandLimit + 4 {
		held := make(chan struct{})
		gate.Store(&held)
		callCtx, stop := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			result <- client.Call(callCtx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, nil)
		}()
		select {
		case <-admitted:
		case <-time.After(5 * time.Second):
			t.Fatalf("call %d was not admitted", i)
		}
		stop()
		var uncertain *coreclient.UncertainError
		if err := <-result; !errors.As(err, &uncertain) {
			t.Fatalf("call %d: abandoned admitted call must be uncertain, got %v", i, err)
		}
		close(held)
		awaitRetained(t, r, client, 0)
	}
	gate.Store(nil)
	var status coreprotocol.CoreStatus
	callCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := client.Call(callCtx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil {
		t.Fatal("abandoned calls exhausted the session's command capacity:", err)
	}
	awaitRetained(t, r, client, 0)
}

// A burst of slow commands larger than the core's retained-result capacity
// (many editor, Git and terminal requests over a slow link) waits for a slot
// on the client instead of failing as busy.
func TestCommandBurstWaitsForCapacityInsteadOfBusy(t *testing.T) {
	r := transportRuntime(t)
	var gate atomic.Pointer[chan struct{}]
	held := make(chan struct{})
	gate.Store(&held)
	admitted := make(chan struct{}, 64)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	client, err := coreclient.Dial(ctx, func(context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ServeCarrier(ctx, r, &gatedCarrier{Conn: corewire.NewMux(b, corewire.CarrierLocal, false), gate: &gate}, nil)
		}()
		return &admissionSignal{Conn: corewire.NewMux(a, corewire.CarrierLocal, true), admitted: admitted}, nil
	}, "bursting-client")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); cancel(); wg.Wait() })
	const burst = sessionCommandLimit + 8
	results := make(chan error, burst)
	callCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	for range burst {
		go func() {
			results <- client.Call(callCtx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, nil)
		}()
	}
	for range sessionCommandLimit {
		select {
		case <-admitted:
		case <-time.After(5 * time.Second):
			t.Fatal("burst did not fill the window")
		}
	}
	if got := retainedCommands(r, client); got != sessionCommandLimit {
		t.Fatalf("core retained %d commands at the blocked-reply barrier, want %d", got, sessionCommandLimit)
	}
	close(held)
	for i := range burst {
		if err := <-results; err != nil {
			t.Fatalf("call %d failed instead of waiting for capacity: %v", i, err)
		}
	}
	awaitRetained(t, r, client, 0)
}

// A carrier lost after the client decoded a final result, but before its ACK
// reached the core, must not pin the result for the rest of the session. The
// client releases it over the replacement carrier.
func TestLostFinalAckReleasedAfterResume(t *testing.T) {
	r := transportRuntime(t)
	var cuts atomic.Int32
	client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
		return open.Kind == coreprotocol.StreamCommand && open.Method == coreprotocol.CoreStatusMethod && writing && cuts.Add(1) <= sessionCommandLimit+4
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := range sessionCommandLimit + 4 {
		var status coreprotocol.CoreStatus
		if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil {
			t.Fatalf("call %d: known result reported as failure: %v", i, err)
		}
	}
	awaitRetained(t, r, client, 0)
}
