package coreclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

func joinedReply(t *testing.T, s corewire.Stream, value coreprotocol.Value, fault *coreprotocol.Fault) bool {
	t.Helper()
	values := []coreprotocol.Value{&coreprotocol.Reply{Final: true, Fault: fault}}
	if value != nil {
		values = append(values, value)
	}
	if err := sendValues(s, values...); err != nil {
		t.Error(err)
		return false
	}
	reader := corewire.NewReader(s, 256)
	body, err := reader.Next()
	var ack coreprotocol.TopicAck
	if err != nil || coreprotocol.Decode(body, &ack) != nil || ack.Seq != 1 {
		t.Errorf("missing exact final ACK: %+v, %v", ack, err)
		return false
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("missing final FIN: %v", err)
		return false
	}
	return true
}

func cancellableResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("cancellable call did not join")
		return nil
	}
}

func TestCallCancellablePreCancelledHasNoEffect(t *testing.T) {
	var calls atomic.Int32
	c := dial(t, &fakeCore{t: t, handle: func(coreprotocol.Open, []byte, corewire.Stream) { calls.Add(1) }})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil)
	var uncertain *UncertainError
	if !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) || calls.Load() != 0 {
		t.Fatalf("pre-admission cancellation had an effect or unknown outcome: %v, calls %d", err, calls.Load())
	}
}

func TestCallCancellableNormalAndFaultAck(t *testing.T) {
	for _, fault := range []*coreprotocol.Fault{nil, {Code: "read_failed", Message: "provider failed"}} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			var calls atomic.Int32
			acked := make(chan struct{})
			c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
				calls.Add(1)
				if open.Method != "read" {
					t.Errorf("successful/faulted read sent cancellation: %+v", open)
				}
				_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
				var value coreprotocol.Value
				if fault == nil {
					value = &coreprotocol.ReceiptRequest{RequestID: "result"}
				}
				if joinedReply(t, s, value, fault) {
					close(acked)
					_ = s.CloseWrite()
				}
			}})
			var out coreprotocol.ReceiptRequest
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, &out)
			if fault == nil && (err != nil || out.RequestID != "result") {
				t.Fatal(out, err)
			}
			var got *coreprotocol.Fault
			if fault != nil && (!errors.As(err, &got) || got.Code != fault.Code) {
				t.Fatal("final provider fault was lost", err)
			}
			select {
			case <-acked:
			default:
				t.Fatal("returned before final ACK")
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected additional command", calls.Load())
			}
		})
	}
}

func TestCallCancellableAdmissionCancellationRace(t *testing.T) {
	opened, admit, cancelled := make(chan coreprotocol.Open, 1), make(chan struct{}), make(chan struct{})
	acked := make(chan string, 2)
	f := &fakeCore{t: t}
	var original coreprotocol.Open
	f.handle = func(open coreprotocol.Open, payload []byte, s corewire.Stream) {
		if open.Method == "read" {
			opened <- open
			_ = sendValues(s, &coreprotocol.Reply{Progress: "not admission"})
			<-admit
			_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
			<-cancelled
			if joinedReply(t, s, nil, &coreprotocol.Fault{Code: "cancelled"}) {
				acked <- open.ID
				_ = s.CloseWrite()
			}
			return
		}
		var in coreprotocol.CancelRequestPayload
		if err := coreprotocol.Decode(payload, &in); err != nil {
			t.Error(err)
		}
		if open.Method != coreprotocol.CancelRequest || open.WorkspaceID != original.WorkspaceID || in.RequestID != original.ID {
			t.Errorf("retargeted cancellation: %+v %+v (original %+v)", open, in, original)
		}
		close(cancelled)
		if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
			acked <- open.ID
			_ = s.CloseWrite()
		}
	}
	c := dial(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil) }()
	original = <-opened
	cancel()
	close(admit)
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) || len(acked) != 2 {
		t.Fatal("cancel did not join and ACK both exact results", err, len(acked))
	}
}

func TestCallCancellableWaitsForAdmissionNotProgress(t *testing.T) {
	opened := make(chan coreprotocol.Open, 1)
	var cancels atomic.Int32
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		if open.Method == coreprotocol.CancelRequest {
			cancels.Add(1)
			return
		}
		_ = sendValues(s, &coreprotocol.Reply{Progress: "provider is not registered"})
		opened <- open
	}})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- c.callCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil, 40*time.Millisecond)
	}()
	original := <-opened
	cancel()
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID != original.ID || !errors.Is(err, context.DeadlineExceeded) || cancels.Load() != 0 {
		t.Fatal("progress inferred admission or lost the original outcome", err, cancels.Load())
	}
}

func TestCallCancellableUncooperativeProviderBounded(t *testing.T) {
	opened := make(chan coreprotocol.Open, 1)
	cancelAck := make(chan struct{})
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if open.Method == "read" {
			opened <- open // deliberately ignores request.cancel; no final reply
			return
		}
		if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
			close(cancelAck)
			_ = s.CloseWrite()
		}
	}})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- c.callCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil, 80*time.Millisecond)
	}()
	original := <-opened
	start := time.Now()
	cancel()
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.ResultKnown || uncertain.RequestID != original.ID || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("uncooperative provider falsely joined or unbounded", err)
	}
	select {
	case <-cancelAck:
	default:
		t.Fatal("cancel command result was not ACKed")
	}
}

func TestCallCancellableLostFinalAckDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	var original atomic.Value
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		calls.Add(1)
		original.Store(open.ID)
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if joinedReply(t, s, &coreprotocol.ReceiptRequest{RequestID: "known-result"}, nil) {
			_ = s.Close() // final ACK accepted, but no release confirmation
		}
	}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var out coreprotocol.ReceiptRequest
	err := c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, &out)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || !uncertain.ResultKnown || uncertain.State != "completed_cleanup_unconfirmed" ||
		uncertain.RequestID != original.Load() || out.RequestID != "known-result" || calls.Load() != 1 {
		t.Fatal("lost ACK confirmation replayed or lost exact identity", err, calls.Load())
	}
}

func TestCallCancellableKnownFinalFaultRetainedAfterLostAck(t *testing.T) {
	c := dial(t, &fakeCore{t: t, handle: func(_ coreprotocol.Open, _ []byte, s corewire.Stream) {
		if joinedReply(t, s, nil, &coreprotocol.Fault{Code: "read_failed", Message: "known failure"}) {
			_ = s.Close()
		}
	}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err := c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil)
	var uncertain *UncertainError
	var fault *coreprotocol.Fault
	if !errors.As(err, &uncertain) || !uncertain.ResultKnown || uncertain.State != "completed_cleanup_unconfirmed" ||
		!errors.As(err, &fault) || fault.Code != "read_failed" {
		t.Fatal("lost ACK discarded known original fault", err)
	}
}

func TestCallCancellableJoinsCancelAckAfterOriginalFinal(t *testing.T) {
	opened := make(chan struct{})
	cancelled, originalAcked, releaseCancel := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if open.Method == "read" {
			close(opened)
			<-cancelled
			if joinedReply(t, s, &coreprotocol.Empty{}, nil) {
				_ = s.CloseWrite()
				close(originalAcked)
			}
			return
		}
		close(cancelled)
		<-releaseCancel
		if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
			_ = s.CloseWrite()
		}
	}})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil) }()
	<-opened
	cancel()
	<-originalAcked
	select {
	case err := <-result:
		t.Fatal("original final abandoned the cancellation helper's retained result", err)
	default:
	}
	close(releaseCancel)
	var uncertain *UncertainError
	if err := cancellableResult(t, result); !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) {
		t.Fatal("cancellation helper did not retire", err)
	}
}

func TestCallCancellableCancelAckFailureKeepsKnownOriginal(t *testing.T) {
	opened := make(chan coreprotocol.Open, 1)
	cancelled, originalAcked := make(chan struct{}), make(chan struct{})
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if open.Method == "read" {
			opened <- open
			<-cancelled
			if joinedReply(t, s, &coreprotocol.Empty{}, nil) {
				_ = s.CloseWrite()
				close(originalAcked)
			}
			return
		}
		close(cancelled)
		<-originalAcked
		if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
			_ = s.Close() // cancel result known; its retained release unconfirmed
		}
	}})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil) }()
	original := <-opened
	cancel()
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID != original.ID || !uncertain.ResultKnown ||
		uncertain.State != "completed_cleanup_unconfirmed" {
		t.Fatal("cancel ACK failure turned a known original result into unknown work", err)
	}
}

func TestCallCancellableCarrierResumeUsesOriginalIdentity(t *testing.T) {
	opened, resumed := make(chan coreprotocol.Open, 1), make(chan struct{})
	cancelled := make(chan struct{})
	var original coreprotocol.Open
	var reads atomic.Int32
	f := &fakeCore{t: t}
	f.handle = func(open coreprotocol.Open, payload []byte, s corewire.Stream) {
		if open.Method == "read" {
			if reads.Add(1) == 1 {
				opened <- open
				return
			}
			if open != original {
				t.Errorf("reattachment changed identity: %+v, original %+v", open, original)
			}
			_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
			close(resumed)
			<-cancelled
			if joinedReply(t, s, &coreprotocol.Empty{}, nil) {
				_ = s.CloseWrite()
			}
			return
		}
		var in coreprotocol.CancelRequestPayload
		if coreprotocol.Decode(payload, &in) != nil || in.RequestID != original.ID || open.WorkspaceID != original.WorkspaceID {
			t.Error("resumed cancellation changed owner", open, in)
		}
		close(cancelled)
		if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
			_ = s.CloseWrite()
		}
	}
	c := dial(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.CallCancellable(ctx, "exact-workspace", "read", &coreprotocol.Empty{}, nil) }()
	original = <-opened
	f.dropCarriers()
	select {
	case <-resumed:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not resume")
	}
	cancel()
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) || reads.Load() != 2 {
		t.Fatal("exact resumed read was not retired", err, reads.Load())
	}
}

func TestCallCancellableClientCloseJoinsHelper(t *testing.T) {
	opened := make(chan coreprotocol.Open, 1)
	c := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		opened <- open
	}})
	result := make(chan error, 1)
	go func() {
		result <- c.CallCancellable(t.Context(), "exact-workspace", "read", &coreprotocol.Empty{}, nil)
	}()
	original := <-opened
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	err := cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.RequestID != original.ID {
		t.Fatal("client close lost exact unjoined identity", err)
	}
	if err := cancellableResult(t, closed); err != nil {
		t.Fatal(err)
	}
}

func TestCallCancellableCarrierLossWithoutReplacementKeepsIdentity(t *testing.T) {
	opened := make(chan coreprotocol.Open, 1)
	f := &fakeCore{t: t, handle: func(open coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if open.Method == "read" {
			opened <- open
		}
	}}
	var attempts atomic.Int32
	first := f.dialer()
	c, err := Dial(t.Context(), func(ctx context.Context) (corewire.Conn, error) {
		if attempts.Add(1) > 1 {
			return nil, ErrNoCarrier
		}
		return first(ctx)
	}, "lost-read")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	result := make(chan error, 1)
	go func() {
		result <- c.CallCancellable(t.Context(), "exact-workspace", "read", &coreprotocol.Empty{}, nil)
	}()
	original := <-opened
	f.dropCarriers()
	err = cancellableResult(t, result)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || uncertain.ResultKnown || uncertain.RequestID != original.ID {
		t.Fatal("carrier loss discarded unknown original identity", err)
	}
}

func TestJoinedAfterFuncJoinsRunningCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := joinedAfterFunc(ctx, func() { close(entered); <-release })
	cancel()
	<-entered
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); stop(); close(returned) }()
	select {
	case <-returned:
		t.Fatal("returned with a live cancellation callback")
	default:
	}
	close(release)
	wg.Wait()
	<-returned
}
