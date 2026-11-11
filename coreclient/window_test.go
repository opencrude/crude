package coreclient

import (
	"context"
	"errors"
	"sync"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
)

// Done marks the actual wait boundary. It is not a sleep or a deadline that
// assumes the command goroutine has received enough scheduler time.
type observedWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestCommandWindowHonorsCancellationAndCloseBeforeAdmission(t *testing.T) {
	var window commandWindow
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := window.acquire(ctx, done, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller reserved a slot:", err)
	}
	close(done)
	if err := window.acquire(t.Context(), done, 1); !errors.Is(err, errWindowClosed) {
		t.Fatal("closed client reserved a slot:", err)
	}
	if window.count != 0 || window.bytes != 0 {
		t.Fatal("unsent calls leaked capacity", window.count, window.bytes)
	}
}

func TestCommandWindowWakesOnActualCapacityRelease(t *testing.T) {
	for _, bytesBound := range []bool{false, true} {
		t.Run(map[bool]string{false: "count", true: "bytes"}[bytesBound], func(t *testing.T) {
			var window commandWindow
			size, count := 1, coreprotocol.SessionRetainedCommands
			if bytesBound {
				size, count = coreprotocol.SessionRetainedCommandBytes, 1
			}
			done := make(chan struct{})
			for range count {
				if err := window.acquire(t.Context(), done, size); err != nil {
					t.Fatal(err)
				}
			}
			ctx := &observedWaitContext{Context: t.Context(), waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() { result <- window.acquire(ctx, done, 1) }()
			<-ctx.waiting
			window.release(size)
			if err := <-result; err != nil {
				t.Fatal("released capacity did not admit the queued call:", err)
			}
			window.release(1)
			for range count - 1 {
				window.release(size)
			}
			if window.count != 0 || window.bytes != 0 {
				t.Fatal("completed calls leaked capacity", window.count, window.bytes)
			}
		})
	}
}

func TestLaterCallWaitsForInFlightReleaseOfItsIdentity(t *testing.T) {
	opened, releasing, allow := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allow) }) }
	t.Cleanup(unblock)
	var mu sync.Mutex
	calls := 0
	f := &fakeCore{t: t, release: func(identities []coreprotocol.CommandIdentity, s corewire.Stream) {
		if len(identities) != 1 || identities[0].ID != "same" {
			t.Errorf("release changed the original identity: %+v", identities)
		}
		close(releasing)
		<-allow
		reply(s, &coreprotocol.Empty{})
	}, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
			close(opened)
			return // no final result before caller cancellation
		}
		reply(s, &coreprotocol.ReceiptRequest{RequestID: "next-result"})
	}}
	c := dial(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		_, err := c.CallWith(ctx, "workspace", "read", &coreprotocol.Empty{}, nil, CallOptions{ID: "same"})
		first <- err
	}()
	<-opened
	cancel()
	var uncertain *UncertainError
	if err := <-first; !errors.As(err, &uncertain) || uncertain.RequestID != "same" {
		t.Fatal("abandonment lost the original identity:", err)
	}
	<-releasing
	nextCtx := &observedWaitContext{Context: t.Context(), waiting: make(chan struct{})}
	next := make(chan error, 1)
	var out coreprotocol.ReceiptRequest
	go func() {
		_, err := c.CallWith(nextCtx, "workspace", "read", &coreprotocol.Empty{}, &out, CallOptions{ID: "same"})
		next <- err
	}()
	<-nextCtx.waiting
	c.releaseMu.Lock()
	waiting := c.releaseChanged != nil
	c.releaseMu.Unlock()
	if !waiting {
		t.Fatal("later call withdrew a release already sent on another stream")
	}
	unblock()
	if err := <-next; err != nil || out.RequestID != "next-result" {
		t.Fatalf("later call did not obtain its own result: %+v %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatal("call was replayed during release:", calls)
	}
}
