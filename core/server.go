package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

const handshakeTimeout = 10 * time.Second
const writeTimeout = 30 * time.Second
const outputByteLimit = 8 << 20
const maxInflight = 16

// Serve owns the listener and transport goroutines, but never closes Runtime.
func Serve(ctx context.Context, runtime *Runtime, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept()
		if err != nil {
			cancelled := ctx.Err() != nil
			cancel()
			if cancelled && errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = ServeConnection(ctx, runtime, conn) }()
	}
}

// ServeConnection binds all application frames to the negotiated immutable owner.
// Overflow or a stalled writer disconnects: the peer must fetch fresh snapshots.
func ServeConnection(ctx context.Context, runtime *Runtime, conn io.ReadWriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	f := corewire.NewFramer(ctx, conn)
	defer f.Close()
	timer := time.AfterFunc(handshakeTimeout, func() { _ = f.Close() })
	id, maximum, err := f.NegotiateServer(corewire.MaxFrameSize)
	if err != nil {
		timer.Stop()
		return err
	}
	c, err := runtime.Connect(id)
	if err != nil {
		timer.Stop()
		return err
	}
	defer c.Close()
	owner := c.Owner
	payload, _ := json.Marshal(owner)
	err = f.Write(corewire.Frame{Version: 1, Kind: corewire.KindEvent, ClientID: owner.ClientID, Epoch: owner.Epoch, Method: "connection.ready", Payload: payload})
	timer.Stop()
	if err != nil {
		return err
	}
	transfers := newPayloadTransfers(maximum)
	defer transfers.Close()

	queue := make(chan corewire.EncodedFrame, 64)
	var mu sync.Mutex
	bytes := 0
	enqueue := func(frame corewire.Frame) bool {
		frame = transfers.WrapFrame(frame)
		encoded, marshalErr := corewire.EncodeFrame(frame)
		if marshalErr != nil || encoded.Len() > corewire.MaxFrameSize {
			cancel()
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if bytes+encoded.Len()+4 > outputByteLimit {
			cancel()
			return false
		}
		select {
		case queue <- encoded:
			bytes += encoded.Len() + 4
			return true
		default:
			cancel()
			return false
		}
	}
	var workers sync.WaitGroup
	defer func() { cancel(); _ = f.Close(); workers.Wait() }()
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case encoded := <-queue:
				t := time.AfterFunc(writeTimeout, func() { _ = f.Close() })
				err := f.WriteEncoded(encoded)
				t.Stop()
				mu.Lock()
				bytes -= encoded.Len() + 4
				mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.Done():
				cancel()
				return
			case change, ok := <-c.Events():
				if !ok {
					cancel()
					return
				}
				b, _ := json.Marshal(change)
				kind := corewire.KindEvent
				if change.Resync {
					kind = corewire.KindResync
				}
				if !enqueue(corewire.Frame{Version: 1, Kind: kind, ClientID: owner.ClientID, Epoch: owner.Epoch, WorkspaceID: change.WorkspaceID, Revision: change.Revision, Method: "workspace.changed", Payload: b}) {
					return
				}
			}
		}
	}()
	active := make(map[string]bool)
	for {
		frame, err := f.Read()
		if err != nil {
			return err
		}
		if frame.Kind != corewire.KindRequest || !validID(frame.RequestID) || frame.ClientID != owner.ClientID || frame.Epoch != owner.Epoch || frame.Method == "" || frame.Error != nil || frame.Revision != 0 || (frame.WorkspaceID != "" && !validID(frame.WorkspaceID)) {
			return errors.New("invalid request envelope or owner")
		}
		if response, handled := transfers.ReadFrame(frame); handled {
			if !enqueue(response) {
				return errors.New("payload transfer output capacity exceeded")
			}
			continue
		}
		mu.Lock()
		if active[frame.RequestID] || len(active) >= maxInflight {
			mu.Unlock()
			return errors.New("duplicate request or in-flight limit exceeded")
		}
		active[frame.RequestID] = true
		mu.Unlock()
		// Admit in wire order, before scheduling response waiters. Awaiting a
		// slow effect must not stall unrelated responses, but goroutines calling
		// Call here would reorder input before it ever reaches the actor.
		pending := c.Dispatch(ctx, coreprotocol.Request{ID: frame.RequestID, Owner: owner, WorkspaceID: frame.WorkspaceID, Method: frame.Method, Payload: frame.Payload})
		workers.Add(1)
		go func(frame corewire.Frame, pending <-chan coreprotocol.Response) {
			defer workers.Done()
			var result coreprotocol.Response
			select {
			case result = <-pending:
			case <-ctx.Done():
				return
			}
			response := corewire.Frame{Version: 1, Kind: corewire.KindResponse, RequestID: result.ID, ClientID: owner.ClientID, Epoch: owner.Epoch, WorkspaceID: frame.WorkspaceID, Method: frame.Method, Revision: result.Revision, Payload: result.Payload}
			if result.Error != nil {
				response.Error = &corewire.Fault{Code: result.Error.Code, Message: result.Error.Message}
			}
			enqueue(response)
			mu.Lock()
			delete(active, frame.RequestID)
			mu.Unlock()
		}(frame, pending)
	}
}
