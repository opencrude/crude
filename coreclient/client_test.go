package coreclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

func fakePeer(t *testing.T) (*Client, *corewire.Framer) {
	t.Helper()
	a, b := net.Pipe()
	f := corewire.NewFramer(context.Background(), b)
	errCh := make(chan error, 1)
	go func() {
		id, _, err := f.NegotiateServer(corewire.MaxFrameSize)
		if err == nil {
			payload, _ := json.Marshal(coreprotocol.Owner{ClientID: id, Epoch: 7})
			err = f.Write(corewire.Frame{Version: 1, Kind: corewire.KindEvent, ClientID: id, Epoch: 7, Method: "connection.ready", Payload: payload})
		}
		errCh <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	c, err := Dial(ctx, a, "test-client")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err = <-errCh; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = f.Close() })
	return c, f
}

func TestMultiplexAndHandshakeLifetime(t *testing.T) {
	c, f := fakePeer(t)
	if c.Owner().Epoch != 7 {
		t.Fatal(c.Owner())
	}
	server := make(chan error, 1)
	go func() {
		var frames []corewire.Frame
		for range 8 {
			r, err := f.Read()
			if err != nil {
				server <- err
				return
			}
			frames = append(frames, r)
		}
		for i := len(frames) - 1; i >= 0; i-- {
			r := frames[i]
			r.Kind = corewire.KindResponse
			if err := f.Write(r); err != nil {
				server <- err
				return
			}
		}
		server <- nil
	}()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result int
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := c.Call(ctx, "workspace", "echo", i, &result); err != nil {
				t.Error(err)
			} else if result != i {
				t.Errorf("result %d want %d", result, i)
			}
		}()
	}
	wg.Wait()
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}

func TestCancelledAwaitPreservesQueuedRequest(t *testing.T) {
	c, f := fakePeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := c.CallRequest(ctx, coreprotocol.Request{ID: "stable-mutation", Owner: c.Owner(), WorkspaceID: "workspace", Method: coreprotocol.SubmitInput, Payload: json.RawMessage(`{}`)})
		result <- err
	}()
	frame, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	var uncertain *UncertainError
	if err := <-result; !errors.As(err, &uncertain) || uncertain.RequestID != "stable-mutation" {
		t.Fatalf("missing uncertainty ID: %v", err)
	}
	frame.Kind = corewire.KindResponse
	if err := f.Write(frame); err != nil {
		t.Fatal(err)
	}
	// Receipt lookup remains usable after late mutation acknowledgement.
	go func() {
		r, e := f.Read()
		if e == nil {
			r.Kind = corewire.KindResponse
			_ = f.Write(r)
		}
	}()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := c.Call(ctx2, "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: "stable-mutation"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEventOverflowStickyResync(t *testing.T) {
	c, f := fakePeer(t)
	for i := range 65 {
		b, _ := json.Marshal(coreprotocol.Change{WorkspaceID: "workspace", Revision: uint64(i + 1)})
		if err := f.Write(corewire.Frame{Version: 1, Kind: corewire.KindEvent, ClientID: c.Owner().ClientID, Epoch: 7, WorkspaceID: "workspace", Revision: uint64(i + 1), Method: "workspace.changed", Payload: b}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("overflow did not disconnect")
	}
	if !errors.Is(c.Err(), ErrResync) {
		t.Fatal(c.Err())
	}
	change := <-c.Events()
	if !change.Resync {
		t.Fatal(change)
	}
}

func TestRejectWrongResponseBinding(t *testing.T) {
	c, f := fakePeer(t)
	result := make(chan error, 1)
	go func() { result <- c.Call(context.Background(), "workspace", "echo", struct{}{}, nil) }()
	r, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	r.Kind = corewire.KindResponse
	r.WorkspaceID = "other"
	if err := f.Write(r); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("accepted foreign workspace response")
	}
}
