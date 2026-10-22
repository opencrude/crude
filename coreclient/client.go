// Package coreclient multiplexes the concrete coreprotocol API over corewire.
package coreclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/metrics"
)

var ErrResync = errors.New("core client requires resynchronization")
var ErrClosed = errors.New("core client disconnected")

// UncertainError means an admitted request may have committed. Never retry a
// mutation automatically; query request.receipt using RequestID after reconnect.
type UncertainError struct {
	RequestID string
	Cause     error
}

func (e *UncertainError) Error() string {
	return fmt.Sprintf("request %s outcome uncertain: %v", e.RequestID, e.Cause)
}
func (e *UncertainError) Unwrap() error { return e.Cause }

type pending struct {
	request coreprotocol.Request
	result  chan coreprotocol.Response
}
type queued struct {
	frame corewire.EncodedFrame
	bytes int
}
type Client struct {
	f               *corewire.Framer
	owner           coreprotocol.Owner
	mu              sync.Mutex
	pending         map[string]pending
	bytes           int
	err             error
	queue           chan queued
	events          chan coreprotocol.Change
	done            chan struct{}
	once            sync.Once
	wg              sync.WaitGroup
	controls        chan coreprotocol.ControlEnvelope
	controlReceived map[string]*receivedControl
	controlBytes    int
}

// Dial uses ctx only for negotiation (bounded to ten seconds). Once successful,
// the client lives until Close, a transport failure or queue overflow.
func Dial(ctx context.Context, conn io.ReadWriteCloser, clientID string) (*Client, error) {
	f := corewire.NewFramer(context.Background(), conn)
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	t := time.AfterFunc(10*time.Second, func() { _ = f.Close() })
	defer stop()
	defer t.Stop()
	if _, err := f.NegotiateClient(clientID, corewire.MaxFrameSize); err != nil {
		_ = f.Close()
		return nil, err
	}
	frame, err := f.Read()
	var owner coreprotocol.Owner
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if frame.Kind != corewire.KindEvent || frame.Method != "connection.ready" || frame.RequestID != "" || frame.WorkspaceID != "" || frame.Error != nil || json.Unmarshal(frame.Payload, &owner) != nil || owner.ClientID != clientID || owner.Epoch == 0 || frame.ClientID != owner.ClientID || frame.Epoch != owner.Epoch {
		_ = f.Close()
		return nil, errors.New("invalid connection.ready")
	}
	if !stop() && ctx.Err() != nil {
		_ = f.Close()
		return nil, ctx.Err()
	}
	t.Stop()
	c := &Client{f: f, owner: owner, pending: make(map[string]pending), queue: make(chan queued, 16), events: make(chan coreprotocol.Change, 64), done: make(chan struct{})}
	c.controls = make(chan coreprotocol.ControlEnvelope, coreprotocol.ControlMaxOutstanding)
	c.controlReceived = make(map[string]*receivedControl)
	c.wg.Add(2)
	go c.read()
	go c.write()
	return c, nil
}

func (c *Client) Owner() coreprotocol.Owner          { return c.owner }
func (c *Client) Events() <-chan coreprotocol.Change { return c.events }
func (c *Client) Done() <-chan struct{}              { return c.done }
func (c *Client) Err() error                         { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Client) fail(err error) {
	c.once.Do(func() { c.mu.Lock(); c.err = err; c.mu.Unlock(); close(c.done); _ = c.f.Close() })
}
func (c *Client) Close() error { c.fail(ErrClosed); c.wg.Wait(); return nil }

func (c *Client) Call(ctx context.Context, workspaceID, method string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return err
	}
	r, err := c.CallRequest(ctx, coreprotocol.Request{ID: hex.EncodeToString(id[:]), Owner: c.owner, WorkspaceID: workspaceID, Method: method, Payload: b})
	if err != nil {
		return err
	}
	if r.Error != nil {
		return r.Error
	}
	if output != nil {
		return json.Unmarshal(r.Payload, output)
	}
	return nil
}

// CallRequest preserves the caller's stable ID. A cancelled await removes only
// the waiter, not an already queued mutation, and always returns its uncertain ID.
func (c *Client) CallRequest(ctx context.Context, request coreprotocol.Request) (coreprotocol.Response, error) {
	if err := ctx.Err(); err != nil {
		return coreprotocol.Response{}, err
	}
	if request.Owner != c.owner || request.ID == "" || len(request.ID) > 256 || request.Method == "" || !json.Valid(request.Payload) {
		return coreprotocol.Response{}, errors.New("invalid request")
	}
	frame := corewire.Frame{Version: 1, Kind: corewire.KindRequest, RequestID: request.ID, ClientID: c.owner.ClientID, Epoch: c.owner.Epoch, WorkspaceID: request.WorkspaceID, Method: request.Method, Payload: append(json.RawMessage(nil), request.Payload...)}
	size, err := corewire.FrameSize(frame)
	if err != nil {
		return coreprotocol.Response{}, err
	}
	if size > corewire.MaxFrameSize {
		return coreprotocol.Response{}, errors.New("request frame too large")
	}
	encoded, err := corewire.EncodeFrame(frame)
	if err != nil {
		return coreprotocol.Response{}, err
	}
	if encoded.Len() > corewire.MaxFrameSize {
		return coreprotocol.Response{}, errors.New("request frame too large")
	}
	request.Payload = nil // Retain only correlation metadata after enqueue.
	started := time.Now()
	defer func() {
		elapsed := time.Since(started)
		clientRequests.Observe(request.Method, elapsed)
		metrics.Blocking("core request", request.Method, elapsed)
	}()
	p := pending{request: request, result: make(chan coreprotocol.Response, 1)}
	c.mu.Lock()
	if c.err != nil {
		err = c.err
	} else if _, exists := c.pending[request.ID]; exists {
		err = errors.New("request already pending")
	} else if len(c.pending) >= 16 || c.bytes+encoded.Len()+4 > 8<<20 {
		// The shell owns the terminal, so the evidence travels with the error.
		methods := make([]string, 0, len(c.pending))
		for _, pending := range c.pending {
			methods = append(methods, pending.request.Method)
		}
		slices.Sort(methods)
		err = fmt.Errorf("client capacity exceeded: pending=%d bytes=%d next=%s in-flight=%v", len(c.pending), c.bytes, request.Method, methods)
	} else {
		select {
		case c.queue <- queued{encoded, encoded.Len() + 4}:
			c.pending[request.ID] = p
			c.bytes += encoded.Len() + 4
		default:
			err = errors.New("client output queue full")
		}
	}
	c.mu.Unlock()
	if err != nil {
		return coreprotocol.Response{}, err
	}
	select {
	case r := <-p.result:
		return c.completePayload(ctx, request, r)
	case <-ctx.Done():
		return coreprotocol.Response{}, &UncertainError{request.ID, ctx.Err()}
	case <-c.done:
		return coreprotocol.Response{}, &UncertainError{request.ID, c.Err()}
	}
}

func (c *Client) write() {
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case q := <-c.queue:
			t := time.AfterFunc(30*time.Second, func() { c.fail(ErrClosed) })
			err := c.f.WriteEncoded(q.frame)
			t.Stop()
			c.mu.Lock()
			c.bytes -= q.bytes
			c.mu.Unlock()
			if err != nil {
				c.fail(err)
				return
			}
		}
	}
}

func (c *Client) read() {
	defer c.wg.Done()
	defer close(c.events)
	defer close(c.controls)
	for {
		f, err := c.f.Read()
		if err != nil {
			c.fail(err)
			return
		}
		if f.ClientID != c.owner.ClientID || f.Epoch != c.owner.Epoch {
			c.fail(errors.New("invalid frame owner"))
			return
		}
		switch f.Kind {
		case corewire.KindResponse:
			if f.RequestID == "" {
				c.fail(errors.New("missing response ID"))
				return
			}
			c.mu.Lock()
			p, ok := c.pending[f.RequestID]
			delete(c.pending, f.RequestID)
			c.mu.Unlock()
			if !ok {
				c.fail(errors.New("unknown response ID"))
				return
			}
			if f.Method != p.request.Method || f.WorkspaceID != p.request.WorkspaceID {
				c.fail(errors.New("response request binding mismatch"))
				return
			}
			r := coreprotocol.Response{ID: f.RequestID, Revision: f.Revision, Payload: f.Payload, Transfer: f.Transfer}
			if f.Error != nil {
				r.Error = &coreprotocol.Fault{Code: f.Error.Code, Message: f.Error.Message}
			}
			select {
			case p.result <- r:
			default:
				c.fail(errors.New("duplicate response"))
				return
			}
		case corewire.KindEvent, corewire.KindResync:
			if f.Method == coreprotocol.CapabilityHostRequest {
				if err := c.ReceiveCapabilityHost(f); err != nil {
					c.fail(err)
					return
				}
				continue
			}
			if f.Method == coreprotocol.ControlRequest {
				if err := c.receiveControl(f); err != nil {
					c.fail(err)
					return
				}
				continue
			}
			var change coreprotocol.Change
			if f.Method != "workspace.changed" || f.RequestID != "" || f.Error != nil || json.Unmarshal(f.Payload, &change) != nil || change.WorkspaceID != f.WorkspaceID || change.Revision != f.Revision || (f.Kind == corewire.KindResync) != change.Resync {
				c.fail(errors.New("invalid change event"))
				return
			}
			// 64 events of at most 128 KiB keep retained event bytes below 8 MiB.
			b, _ := json.Marshal(f)
			if len(b) > 128<<10 || change.Resync {
				for {
					select {
					case <-c.events:
					default:
						goto resync
					}
				}
			resync:
				c.events <- coreprotocol.Change{Resync: true}
				c.fail(ErrResync)
				return
			}
			select {
			case c.events <- change:
			default:
				for {
					select {
					case <-c.events:
					default:
						goto overflow
					}
				}
			overflow:
				c.events <- coreprotocol.Change{Resync: true}
				c.fail(ErrResync)
				return
			}
		default:
			c.fail(errors.New("unexpected frame kind"))
			return
		}
	}
}

var clientRequests = metrics.NewTable(metrics.Meta{Name: "link.requests", Group: metrics.GroupRPC, Title: "Shell requests by method",
	Help: "Round trip from the shell to the core and back, per method. Compare with the core's own time to separate transport from work.",
	Warn: 100_000, Crit: 1_000_000})
