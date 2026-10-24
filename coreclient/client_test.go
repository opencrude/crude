package coreclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

// fakeCore answers sessions over carriers it is handed, with a handler per
// command; it resumes sessions by ID unless told the session is gone.
type fakeCore struct {
	t       *testing.T
	handle  func(open coreprotocol.Open, payload []byte, s corewire.Stream)
	session func(s corewire.Stream) // serves the session topic when set
	ending  func(s corewire.Stream) // overrides the clean-close acknowledgement
	mu      sync.Mutex
	gone    bool
	hellos  []coreprotocol.Hello
	carrier []*corewire.Mux
}

func (f *fakeCore) serve(conn net.Conn) {
	m := corewire.NewMux(conn, corewire.CarrierLocal, false)
	f.mu.Lock()
	f.carrier = append(f.carrier, m)
	f.mu.Unlock()
	go func() {
		control, err := m.AcceptStream(context.Background())
		if err != nil {
			return
		}
		body, err := corewire.NewReader(control, 1<<16).Next()
		if err != nil {
			return
		}
		var hello coreprotocol.Hello
		if err := coreprotocol.Decode(body, &hello); err != nil {
			f.t.Error(err)
			return
		}
		f.mu.Lock()
		f.hellos = append(f.hellos, hello)
		gone := f.gone
		f.mu.Unlock()
		welcome := coreprotocol.Welcome{Owner: coreprotocol.Owner{ClientID: hello.ClientID, Epoch: 7}, SessionID: "session", Secret: "secret", Schema: coreprotocol.Schema, Build: hello.Build, Grace: 5 * time.Second, Resumed: hello.Resume != nil}
		if gone && hello.Resume != nil {
			welcome = coreprotocol.Welcome{Fault: &coreprotocol.Fault{Code: "session_gone", Message: "expired"}}
		}
		if err := sendValues(control, &welcome); err != nil {
			return
		}
		go func() {
			if _, err := corewire.NewReader(control, 1).Next(); errors.Is(err, io.EOF) {
				if f.ending != nil {
					f.ending(control)
					return
				}
				_ = sendValues(control, &coreprotocol.SessionEnd{Reason: "client closed"})
				_ = control.CloseWrite()
			}
		}()
		for {
			s, err := m.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go func() {
				body, err := corewire.NewReader(s, corewire.MaxMessage).Next()
				if err != nil {
					return
				}
				var open coreprotocol.Open
				rest, err := coreprotocol.DecodePrefix(body, &open)
				if err != nil {
					f.t.Error(err)
					return
				}
				if open.Kind == coreprotocol.StreamTopic {
					if f.session != nil && open.Topic == coreprotocol.TopicSession {
						f.session(s)
					}
					<-m.Done() // hold the topic open
					return
				}
				f.handle(open, rest, s)
			}()
		}
	}()
}

func TestCloseBoundsMissingCleanupReceipt(t *testing.T) {
	f := &fakeCore{t: t, ending: func(corewire.Stream) {}, handle: func(coreprotocol.Open, []byte, corewire.Stream) {}}
	c := dial(t, f)
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("unacknowledged graceful close did not expose its timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing receipt blocked client close")
	}
}

// reply answers a command with value.
func reply(s corewire.Stream, value coreprotocol.Value) {
	_ = sendValues(s, &coreprotocol.Reply{Final: true}, value)
	_ = s.CloseWrite()
}

// dialer returns a Dialer that hands each new carrier to f.
func (f *fakeCore) dialer() Dialer {
	return func(context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		f.serve(b)
		return corewire.NewMux(a, corewire.CarrierLocal, true), nil
	}
}

func (f *fakeCore) dropCarriers() {
	f.mu.Lock()
	carriers := f.carrier
	f.carrier = nil
	f.mu.Unlock()
	for _, m := range carriers {
		_ = m.Close()
	}
}

func dial(t *testing.T, f *fakeCore) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, f.dialer(), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Commands run concurrently on their own streams; replies arriving in any
// order reach exactly their callers.
func TestConcurrentCommandsAndHandshake(t *testing.T) {
	var mu sync.Mutex
	var waiting []func()
	f := &fakeCore{t: t}
	f.handle = func(open coreprotocol.Open, payload []byte, s corewire.Stream) {
		var in coreprotocol.ReceiptRequest
		if err := coreprotocol.Decode(payload, &in); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		waiting = append(waiting, func() { reply(s, &in) })
		ready := len(waiting) == 8
		mu.Unlock()
		if ready {
			for i := len(waiting) - 1; i >= 0; i-- {
				waiting[i]()
			}
		}
	}
	c := dial(t, f)
	if c.Owner().Epoch != 7 {
		t.Fatal(c.Owner())
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprint(i)
			var got coreprotocol.ReceiptRequest
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Call(ctx, "workspace", "echo", &coreprotocol.ReceiptRequest{RequestID: want}, &got); err != nil {
				t.Error(err)
			} else if got.RequestID != want {
				t.Errorf("result %q want %q", got.RequestID, want)
			}
		}()
	}
	wg.Wait()
}

// Cancelling an await reports the request identity as uncertain; the
// session stays usable for the receipt lookup that resolves it.
func TestCancelledAwaitReportsUncertainIdentity(t *testing.T) {
	admitted := make(chan struct{})
	f := &fakeCore{t: t}
	f.handle = func(open coreprotocol.Open, payload []byte, s corewire.Stream) {
		if open.Method == coreprotocol.SubmitInput {
			close(admitted)
			return // never answers
		}
		reply(s, &coreprotocol.Receipt{RequestID: "stable-mutation", State: "completed"})
	}
	c := dial(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := c.CallWith(ctx, "workspace", coreprotocol.SubmitInput, &coreprotocol.InputRequest{Text: "hi"}, nil, CallOptions{ID: "stable-mutation"})
		result <- err
	}()
	<-admitted
	cancel()
	var uncertain *UncertainError
	if err := <-result; !errors.As(err, &uncertain) || uncertain.RequestID != "stable-mutation" {
		t.Fatalf("missing uncertainty ID: %v", err)
	}
	var receipt coreprotocol.Receipt
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := c.Call(ctx2, "", coreprotocol.GetReceipt, &coreprotocol.ReceiptRequest{RequestID: "stable-mutation"}, &receipt); err != nil || receipt.State != "completed" {
		t.Fatal(receipt, err)
	}
}

// A carrier lost while a command runs is replaced: the session resumes and
// the command is re-attached under its original identity, never lost.
func TestCarrierLossResumesSessionAndReattachesCommand(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	first := make(chan struct{})
	f := &fakeCore{t: t}
	f.handle = func(open coreprotocol.Open, payload []byte, s corewire.Stream) {
		mu.Lock()
		seen[open.ID]++
		count := seen[open.ID]
		mu.Unlock()
		if count == 1 {
			close(first) // first delivery: the carrier dies before the reply
			return
		}
		reply(s, &coreprotocol.BoolResult{OK: true})
	}
	c := dial(t, f)
	result := make(chan error, 1)
	var out coreprotocol.BoolResult
	go func() {
		_, err := c.CallWith(context.Background(), "workspace", "mutate", &coreprotocol.Empty{}, &out, CallOptions{ID: "resumed-command"})
		result <- err
	}()
	<-first
	f.dropCarriers()
	select {
	case err := <-result:
		if err != nil || !out.OK {
			t.Fatal(err, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command did not survive carrier loss")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hellos) != 2 || f.hellos[1].Resume == nil || f.hellos[1].Resume.SessionID != "session" || f.hellos[1].Resume.Secret != "secret" {
		t.Fatalf("session not resumed: %+v", f.hellos)
	}
	if seen["resumed-command"] != 2 {
		t.Fatalf("command delivered %d times, want original plus re-attachment", seen["resumed-command"])
	}
}

// When the core no longer holds the session, the client ends instead of
// silently acting under a new identity.
func TestLostSessionEndsClient(t *testing.T) {
	f := &fakeCore{t: t, handle: func(coreprotocol.Open, []byte, corewire.Stream) {}}
	c := dial(t, f)
	f.mu.Lock()
	f.gone = true
	f.mu.Unlock()
	f.dropCarriers()
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("lost session not reported")
	}
	if !errors.Is(c.Err(), ErrSessionLost) {
		t.Fatal(c.Err())
	}
}
