package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	captypes "crude/types/capability"
	"crude/types/control"
)

// deliveryCutCarrier injects loss at application boundaries on a real mux.
// Reads are discarded only after the peer's successful write reached the carrier;
// ACK cuts occur after client admission but before the ACK reaches the core.
type deliveryCutCarrier struct {
	corewire.Conn
	cut     func(coreprotocol.Open, bool) bool
	written <-chan struct{}
}

type deliveryObservedCarrier struct {
	corewire.Conn
	written chan<- struct{}
}

func (c *deliveryObservedCarrier) AcceptStream(ctx context.Context) (corewire.Stream, error) {
	s, err := c.Conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return &deliveryObservedStream{Stream: s, written: c.written}, nil
}

type deliveryObservedStream struct {
	corewire.Stream
	written chan<- struct{}
	open    coreprotocol.Open
	opened  bool
}

func (s *deliveryObservedStream) Read(b []byte) (int, error) {
	n, err := s.Stream.Read(b)
	if !s.opened && n > 0 {
		s.opened = true
		_, prefix := binary.Uvarint(b[:n])
		if prefix > 0 {
			_, _ = coreprotocol.DecodePrefix(b[prefix:n], &s.open)
		}
	}
	return n, err
}

func (s *deliveryObservedStream) Write(b []byte) (int, error) {
	n, err := s.Stream.Write(b)
	if err == nil && (s.open.Kind == coreprotocol.StreamCommand || s.open.Topic == coreprotocol.TopicSession) {
		s.written <- struct{}{}
	}
	return n, err
}

func (c *deliveryCutCarrier) OpenStream(ctx context.Context, class corewire.Class) (corewire.Stream, error) {
	s, err := c.Conn.OpenStream(ctx, class)
	if err != nil {
		return nil, err
	}
	return &deliveryCutStream{Stream: s, conn: c}, nil
}

type deliveryCutStream struct {
	corewire.Stream
	conn   *deliveryCutCarrier
	open   coreprotocol.Open
	opened bool
}

func (s *deliveryCutStream) Write(b []byte) (int, error) {
	if !s.opened {
		s.opened = true
		_, n := binary.Uvarint(b)
		if n > 0 {
			_, _ = coreprotocol.DecodePrefix(b[n:], &s.open)
		}
	} else if s.conn.cut(s.open, true) {
		<-s.conn.written // do not cut until the core's preceding write succeeded
		_ = s.conn.Close()
		return 0, corewire.ErrClosed
	}
	return s.Stream.Write(b)
}

func (s *deliveryCutStream) Read(b []byte) (int, error) {
	n, err := s.Stream.Read(b)
	if n > 0 && s.conn.cut(s.open, false) {
		<-s.conn.written
		_ = s.conn.Close()
		return 0, corewire.ErrClosed
	}
	return n, err
}

func deliveryClient(t *testing.T, r *Runtime, cut func(coreprotocol.Open, bool) bool) *coreclient.Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	c, err := coreclient.Dial(ctx, func(context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		written := make(chan struct{}, 32)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ServeCarrier(ctx, r, &deliveryObservedCarrier{Conn: corewire.NewMux(b, corewire.CarrierLocal, false), written: written}, nil)
		}()
		return &deliveryCutCarrier{Conn: corewire.NewMux(a, corewire.CarrierLocal, true), cut: cut, written: written}, nil
	}, "delivery-client")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); cancel(); wg.Wait() })
	return c
}

func TestSessionControlCarrierCutsExactlyOnce(t *testing.T) {
	for _, beforeAck := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted-write", true: "admitted-before-ack"}[beforeAck], func(t *testing.T) {
			r := transportRuntime(t)
			var once sync.Once
			cut := make(chan struct{})
			client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
				if open.Topic != coreprotocol.TopicSession || writing != beforeAck {
					return false
				}
				fired := false
				once.Do(func() { fired = true; close(cut) })
				return fired
			})
			r.mu.Lock()
			owner := r.clients[client.Owner().ClientID]
			r.mu.Unlock()
			owner.mu.Lock()
			owner.attached["ws"] = &actor{}
			owner.controlRegistered["ws"] = true
			owner.mu.Unlock()
			// The placeholder attachment owns no resources; remove before cleanup.
			t.Cleanup(func() { owner.mu.Lock(); delete(owner.attached, "ws"); owner.mu.Unlock() })
			envelope := shellEnvelope("one-effect", "ws", client.Owner())
			envelope.Request.CommandID, envelope.Descriptor.ID = "tree.set_visible", "tree.set_visible"
			envelope.Descriptor.Effect = control.Write
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			completed := make(chan error, 1)
			go func() {
				result, err := r.RequestShellControl(ctx, "ws", *envelope.Request.Target.Shell, envelope)
				if err == nil && result.State != control.Applied {
					err = errors.New("effect did not resolve")
				}
				completed <- err
			}()
			select {
			case <-cut:
			case <-ctx.Done():
				t.Fatal("fault boundary not reached")
			}
			select {
			case got := <-client.ControlRequests():
				if got.Request.ID != "one-effect" {
					t.Fatal(got)
				}
			case <-ctx.Done():
				t.Fatal("unacknowledged control lost")
			}
			result := control.Result{RequestID: "one-effect", OperationID: "one-effect", State: control.Applied, Output: json.RawMessage(`{}`)}
			if err := client.ReplyControl(ctx, "ws", result); err != nil {
				t.Fatal(err)
			}
			if err := <-completed; err != nil {
				t.Fatal(err)
			}
			if _, err := r.RequestShellControl(ctx, "ws", *envelope.Request.Target.Shell, envelope); err != nil {
				t.Fatal(err)
			}
			// A command barrier proves the replacement carrier is serving requests.
			if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.ControlRequests():
				t.Fatal("control effect enqueued twice")
			default:
			}
		})
	}
}

func TestCommandSuccessfulWriteRetainedUntilFinalAck(t *testing.T) {
	r := transportRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var once sync.Once
	client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
		if open.Kind != coreprotocol.StreamCommand || writing {
			return false
		}
		cut := false
		once.Do(func() {
			cut = true
			// A replayed read would now observe one *other* client rather than
			// zero (core.status excludes its administrative caller).
			if _, err := r.Connect("later-observer"); err != nil {
				t.Error(err)
			}
		})
		return cut
	})
	var got coreprotocol.CoreStatus
	if _, err := client.CallWith(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &got, coreclient.CallOptions{ID: "retained-read"}); err != nil {
		t.Fatal(err)
	}
	if got.Clients != 0 {
		t.Fatalf("lost result caused command replay: %+v", got)
	}
	r.mu.Lock()
	c := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	c.commandMu.Lock()
	retained, retainedBytes := len(c.commands), c.commandBytes
	c.commandMu.Unlock()
	if retained != 0 || retainedBytes != 0 {
		t.Fatalf("ACK did not release commands: %d/%d", retained, retainedBytes)
	}
}

func TestSessionHostAcceptedWriteSurvivesCarrierLoss(t *testing.T) {
	r := transportRuntime(t)
	var once sync.Once
	client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
		if open.Topic != coreprotocol.TopicSession || writing {
			return false
		}
		cut := false
		once.Do(func() { cut = true })
		return cut
	})
	r.mu.Lock()
	c := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	c.mu.Lock()
	c.attached["ws"] = &actor{}
	c.mu.Unlock()
	t.Cleanup(func() { c.mu.Lock(); delete(c.attached, "ws"); c.mu.Unlock() })
	h := c.hostChannel()
	h.mu.Lock()
	h.registered["ws"] = true
	h.mu.Unlock()
	shell := control.ShellOwner{ClientID: client.Owner().ClientID, Epoch: client.Owner().Epoch}
	request := captypes.HostRequest{ID: "host-once", WorkspaceID: "ws", Operation: "connect", Actor: captypes.RuntimeActor{ID: "agent", RunID: "run", WorkspaceID: "ws", RuntimeEpoch: 1, TurnGeneration: 1, ShellOwner: &shell}, Connection: captypes.ConnectionConfig{Host: "shell", Transport: "stdio", Directory: t.TempDir()}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		reply, err := r.DispatchHost(ctx, request)
		if err == nil && reply.ID != request.ID {
			err = errors.New("host reply identity lost")
		}
		completed <- err
	}()
	select {
	case got := <-client.CapabilityHostRequests():
		if got.ID != request.ID {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("accepted host write lost")
	}
	if err := client.ReplyCapabilityHost(ctx, "ws", captypes.HostReply{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if _, err := r.DispatchHost(ctx, request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.CapabilityHostRequests():
		t.Fatal("host effect replayed")
	default:
	}
}

func TestCommandLostFinalAckReturnsKnownResultWithoutReplay(t *testing.T) {
	r := transportRuntime(t)
	var once sync.Once
	client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
		if open.Kind != coreprotocol.StreamCommand || !writing {
			return false
		}
		cut := false
		once.Do(func() {
			cut = true
			if _, err := r.Connect("after-result"); err != nil {
				t.Error(err)
			}
		})
		return cut
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var first, again coreprotocol.CoreStatus
	options := coreclient.CallOptions{ID: "known-result"}
	if _, err := client.CallWith(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &first, options); err != nil {
		t.Fatal("known outcome incorrectly uncertain", err)
	}
	if _, err := client.CallWith(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &again, options); err != nil {
		t.Fatal(err)
	}
	if first.Clients != 0 || again.Clients != 0 {
		t.Fatal("lost ACK replayed command", first.Clients, again.Clients)
	}
}

func TestTopicReplacementClosesBlockedWriterBeforeJoin(t *testing.T) {
	c := &Connection{}
	closed := make(chan struct{})
	ctx, release := c.claimTopic(context.Background(), "blocked", func() { close(closed) })
	go func() { <-closed; <-ctx.Done(); release() }()
	done := make(chan struct{})
	go func() { _, release := c.claimTopic(context.Background(), "blocked"); release(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replacement waited without interrupting owned stream")
	}
}

func TestCommandRetentionIdentityAndCapacity(t *testing.T) {
	r := transportRuntime(t)
	c, err := r.Connect("bounded")
	if err != nil {
		t.Fatal(err)
	}
	request := coreprotocol.Request{Owner: c.Owner, ID: "same", Method: coreprotocol.CoreStatusMethod, Payload: emptyPayload}
	cmd, err := c.admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	<-cmd.done
	if again, err := c.admit(context.Background(), request); err != nil || again != cmd {
		t.Fatal("command not reconciled", err)
	}
	request.Payload = append(bytes.Clone(emptyPayload), 0)
	if _, err := c.admit(context.Background(), request); err == nil {
		t.Fatal("changed identity accepted")
	}
	for i := 1; i < sessionCommandLimit; i++ {
		request.ID = string(rune('A' + i))
		request.Payload = emptyPayload
		if _, err := c.admit(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	request.ID = "overflow"
	if _, err := c.admit(context.Background(), request); !errors.Is(err, errCommandCapacity) {
		t.Fatal("unbounded command retention", err)
	}
	c.forget(cmd)
	if _, err := c.admit(context.Background(), request); err != nil {
		t.Fatal("ACK did not restore admission", err)
	}
}

func TestPrivateClipboardCarrierCutsExactlyOnce(t *testing.T) {
	for _, beforeAck := range []bool{false, true} {
		t.Run(fmt.Sprint(beforeAck), func(t *testing.T) {
			r := transportRuntime(t)
			var once sync.Once
			client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
				if open.Topic != coreprotocol.TopicSession || writing != beforeAck {
					return false
				}
				cut := false
				once.Do(func() { cut = true })
				return cut
			})
			r.mu.Lock()
			owner := r.clients[client.Owner().ClientID]
			r.mu.Unlock()
			if !owner.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 7, Clipboard: [][]byte{[]byte("private")}}) {
				t.Fatal("offer refused")
			}
			select {
			case got := <-client.PrivateEffects():
				if got.WorkspaceID != "ws" || got.TerminalClipboard == nil || got.TerminalClipboard.Generation != 7 || string(got.TerminalClipboard.Clipboard[0]) != "private" {
					t.Fatal("private effect lost identity", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("private effect lost across carrier cut")
			}
			// The next retained offer proves the replacement topic processed the
			// first cursor; no replay of the first offer may precede it.
			if !owner.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 8, Clipboard: [][]byte{[]byte("next")}}) {
				t.Fatal("offer refused")
			}
			select {
			case got := <-client.PrivateEffects():
				if got.TerminalClipboard == nil || got.TerminalClipboard.Generation != 8 {
					t.Fatal("private offer replayed", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("replacement private topic stalled")
			}
		})
	}
}

func TestPrivateClipboardSlowConsumerDoesNotDisplaceControlOrHost(t *testing.T) {
	r := transportRuntime(t)
	admitted := make(chan struct{}, 2)
	client := deliveryClient(t, r, func(open coreprotocol.Open, writing bool) bool {
		if open.Topic == coreprotocol.TopicSession && writing {
			select {
			case admitted <- struct{}{}:
			default:
			}
		}
		return false
	})
	r.mu.Lock()
	owner := r.clients[client.Owner().ClientID]
	r.mu.Unlock()
	owner.mu.Lock()
	owner.attached["ws"] = &actor{}
	owner.controlRegistered["ws"] = true
	owner.mu.Unlock()
	t.Cleanup(func() { owner.mu.Lock(); delete(owner.attached, "ws"); owner.mu.Unlock() })
	host := owner.hostChannel()
	host.mu.Lock()
	host.registered["ws"] = true
	host.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// One accepted maximum-size offer fills the client's byte budget. A later
	// small offer must be refused locally, without stalling the request journal.
	large := make([]byte, coreprotocol.SessionPrivateMaxBytes-44)
	if !owner.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 1, Clipboard: [][]byte{large}}) {
		t.Fatal("large offer refused early")
	}
	select {
	case <-admitted:
	case <-ctx.Done():
		t.Fatal("large offer did not reach client admission")
	}
	if !owner.offerClipboard("ws", coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 1, Clipboard: [][]byte{[]byte("overflow")}}) {
		t.Fatal("test requires two core-admitted offers")
	}
	select {
	case <-admitted:
	case <-ctx.Done():
		t.Fatal("refused offer blocked session ACK")
	}
	envelope := shellEnvelope("after-private", "ws", client.Owner())
	envelope.Request.CommandID, envelope.Descriptor.ID = "tree.set_visible", "tree.set_visible"
	envelope.Descriptor.Effect = control.Write
	controlDone := make(chan error, 1)
	go func() {
		_, err := r.RequestShellControl(ctx, "ws", *envelope.Request.Target.Shell, envelope)
		controlDone <- err
	}()
	select {
	case <-client.ControlRequests():
	case <-ctx.Done():
		t.Fatal("clipboard presentation blocked control request")
	}
	if err := client.ReplyControl(ctx, "ws", control.Result{RequestID: "after-private", OperationID: "after-private", State: control.Applied, Output: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := <-controlDone; err != nil {
		t.Fatal(err)
	}
	shell := control.ShellOwner{ClientID: client.Owner().ClientID, Epoch: client.Owner().Epoch}
	request := captypes.HostRequest{ID: "after-private-host", WorkspaceID: "ws", Operation: "connect", Actor: captypes.RuntimeActor{ID: "agent", RunID: "run", WorkspaceID: "ws", RuntimeEpoch: 1, TurnGeneration: 1, ShellOwner: &shell}, Connection: captypes.ConnectionConfig{Host: "shell", Transport: "stdio", Directory: t.TempDir()}}
	hostDone := make(chan error, 1)
	go func() { _, err := r.DispatchHost(ctx, request); hostDone <- err }()
	select {
	case <-client.CapabilityHostRequests():
	case <-ctx.Done():
		t.Fatal("clipboard presentation blocked host request")
	}
	if err := client.ReplyCapabilityHost(ctx, "ws", captypes.HostReply{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
	// Drain exactly the accepted offer and the reserved visible refusal. Neither
	// may be silently replaced by an invalidation or a request payload.
	accepted, refused := 0, 0
	for accepted+refused < 2 {
		select {
		case effect := <-client.PrivateEffects():
			if effect.Error != "" {
				refused++
			} else if effect.TerminalClipboard != nil && len(effect.TerminalClipboard.Clipboard[0]) == len(large) {
				accepted++
			} else {
				t.Fatal("unexpected private payload", effect)
			}
		case <-ctx.Done():
			t.Fatal("accepted private offer or visible refusal lost")
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatal("private admission outcome", accepted, refused)
	}
}
