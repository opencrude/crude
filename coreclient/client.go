// Package coreclient is the Go client of the core protocol: a session over
// any carrier, typed commands on their own streams, resumable topic
// subscriptions and ordered input streams. A lost carrier is replaced
// transparently within the session grace period.
package coreclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/metrics"
	captypes "crude/types/capability"
)

// ErrResync reports a replica that must reload its snapshot.
var ErrResync = errors.New("core client requires resynchronization")

// ErrTopicReset asks Subscribe to reopen from a full snapshot after a rejected
// update. The handler must not commit any part of that rejected update.
var ErrTopicReset = errors.New("core topic requires a fresh snapshot")

// ErrClosed reports use of a client after Close.
var ErrClosed = errors.New("core client disconnected")

// ErrNoCarrier is a Dialer's answer when no further carrier can exist; a
// session that loses its carrier then ends at once instead of retrying.
var ErrNoCarrier = errors.New("core client has no further carrier")

// ErrSessionLost reports a session the core no longer holds (grace expired
// or the core restarted). Attachments and leases must be re-established.
var ErrSessionLost = errors.New("core session ended")

// UncertainError means an admitted request may have committed. Never retry a
// mutation under a new identity; query request.receipt using RequestID.
type UncertainError struct {
	RequestID string
	Cause     error
	// State distinguishes a still-pending operation from an unknown outcome.
	State string
	// ResultKnown distinguishes unconfirmed cleanup from an unknown operation:
	// the original final result was decoded and must never be replayed.
	ResultKnown bool
}

func (e *UncertainError) Error() string {
	state := e.State
	if state == "" {
		state = "uncertain"
	}
	return fmt.Sprintf("request %s outcome %s: %v", e.RequestID, state, e.Cause)
}
func (e *UncertainError) Unwrap() error { return e.Cause }

// Dialer opens one carrier to the core.
type Dialer func(ctx context.Context) (corewire.Conn, error)

// Client is one session with a core.
type Client struct {
	dial          Dialer
	clientID      string
	owner         coreprotocol.Owner
	resume        coreprotocol.SessionResume
	grace         time.Duration
	localHost     bool
	carrierChange chan struct{} // cancellation-aware carrier ownership gate
	lifetime      context.Context
	cancel        context.CancelFunc
	displayNS     atomic.Int64

	mu           sync.Mutex
	carrier      corewire.Conn
	control      corewire.Stream
	ready        chan struct{} // closed while carrier is usable
	err          error
	done         chan struct{}
	once         sync.Once
	wg           sync.WaitGroup
	stopped      chan struct{} // terminal transport shutdown has completed
	controlEnded chan struct{} // current control reader observed the peer's ending
	controlRead  corewire.Stream
	closeErr     error // published by stopped
	controlErr   error // protected by mu

	privateEffects  chan PrivateEffect
	privateMu       sync.Mutex
	privateQueue    []PrivateEffect
	privateFailure  *PrivateEffect
	privateCount    int
	privateBytes    int
	privateWake     chan struct{}
	controls        chan coreprotocol.ControlEnvelope
	controlReceived map[string]*receivedControl
	controlBytes    int
	hosts           *hostInbox
}

// Dial starts a session over a carrier from dial. ctx bounds only the
// handshake; the session lives until Close or until it cannot be resumed.
func Dial(ctx context.Context, dial Dialer, clientID string) (*Client, error) {
	return DialWithOptions(ctx, dial, clientID, DialOptions{})
}

// DialOptions carries the device credential only for initial network admission;
// resumed sessions use their independent resume secret and retained provenance.
type DialOptions struct {
	Credential string
}

func DialWithOptions(ctx context.Context, dial Dialer, clientID string, options DialOptions) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	welcome, control, err := handshake(ctx, conn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: clientID, Credential: options.Credential})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	lifetime, lifetimeCancel := context.WithCancel(context.Background())
	c := &Client{dial: dial, lifetime: lifetime, cancel: lifetimeCancel, clientID: clientID, carrierChange: make(chan struct{}, 1), owner: welcome.Owner, resume: coreprotocol.SessionResume{SessionID: welcome.SessionID, Secret: welcome.Secret}, grace: welcome.Grace, localHost: welcome.LocalHost,
		carrier: conn, control: control, ready: make(chan struct{}), done: make(chan struct{}), stopped: make(chan struct{}), controlEnded: make(chan struct{}, 1),
		privateEffects: make(chan PrivateEffect), privateWake: make(chan struct{}, 1), controls: make(chan coreprotocol.ControlEnvelope, coreprotocol.ControlMaxOutstanding),
		controlReceived: make(map[string]*receivedControl), hosts: newHostInbox()}
	close(c.ready)
	c.wg.Add(4)
	go c.watchControl(control)
	go c.supervise(conn)
	go c.sessionTopic()
	go c.deliverPrivateEffects()
	return c, nil
}

func handshake(ctx context.Context, conn corewire.Conn, hello coreprotocol.Hello) (coreprotocol.Welcome, corewire.Stream, error) {
	var welcome coreprotocol.Welcome
	control, err := conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		return welcome, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = control.Close() })
	defer stop()
	if err := sendValues(control, &hello); err != nil {
		return welcome, nil, err
	}
	body, err := corewire.NewReader(control, 64<<10).Next()
	if err != nil {
		return welcome, nil, fmt.Errorf("core handshake: %w", err)
	}
	if err := coreprotocol.Decode(body, &welcome); err != nil {
		// A different build may lay out the rest of Welcome differently; its
		// frozen identity prefix still identifies the mismatch.
		if schema, build, idErr := coreprotocol.HandshakeIdentity(body); idErr == nil && (schema != hello.Schema || build != hello.Build) {
			return welcome, nil, &coreprotocol.Fault{Code: "schema_mismatch", Message: fmt.Sprintf("core speaks protocol %s (build %s); client speaks %s (build %s)", schema, build, hello.Schema, hello.Build)}
		}
		return welcome, nil, fmt.Errorf("core handshake: %w", err)
	}
	if welcome.Fault != nil {
		_ = control.CloseWrite()
		return welcome, nil, welcome.Fault
	}
	if welcome.Schema != coreprotocol.Schema || welcome.Build != hello.Build || welcome.Paired != nil {
		return welcome, nil, errors.New("invalid negotiated protocol or build")
	}
	if welcome.Owner.ClientID != hello.ClientID || welcome.Owner.Epoch == 0 || welcome.SessionID == "" {
		return welcome, nil, errors.New("invalid welcome")
	}
	return welcome, control, nil
}

func sendValues(s corewire.Stream, values ...coreprotocol.Value) error {
	m := corewire.NewMessage()
	defer m.Release()
	var err error
	for _, v := range values {
		if m.B, err = v.MarshalMsg(m.B); err != nil {
			return err
		}
	}
	return m.Send(s)
}

func (c *Client) Owner() coreprotocol.Owner { return c.owner }
func (c *Client) LocalHost() bool           { return c.localHost }

func (c *Client) SetDisplayInterval(interval time.Duration) {
	c.displayNS.Store(int64(max(time.Millisecond, min(interval, 250*time.Millisecond))))
}
func (c *Client) DisplayInterval() time.Duration {
	if ns := c.displayNS.Load(); ns > 0 {
		return time.Duration(ns)
	}
	return 16 * time.Millisecond
}

// PrivateEffect is either an admitted controller-local clipboard offer or an
// explicit refusal notice. Domain state and command progress never appear here.
type PrivateEffect struct {
	WorkspaceID       string
	TerminalClipboard *coreprotocol.TerminalClipboard
	Error             string
}

func (c *Client) PrivateEffects() <-chan PrivateEffect { return c.privateEffects }
func (c *Client) Done() <-chan struct{}                { return c.done }

func (c *Client) acquireCarrierChange(ctx context.Context) error {
	select {
	case c.carrierChange <- struct{}{}:
		if err := ctx.Err(); err != nil {
			c.releaseCarrierChange()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
}
func (c *Client) releaseCarrierChange() { <-c.carrierChange }

func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Carrier reports the current carrier and its RTT; empty while resuming.
func (c *Client) Carrier() (corewire.Carrier, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.carrier == nil {
		return "", 0
	}
	return c.carrier.Carrier(), c.carrier.RTT()
}

func (c *Client) fail(err error) {
	var carrier corewire.Conn
	var control corewire.Stream
	ending := false
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		carrier, control = c.carrier, c.control
		ending = true
		c.mu.Unlock()
		close(c.done)
		if c.cancel != nil {
			c.cancel()
		}
	})
	if !ending {
		return
	}
	defer close(c.stopped)
	if carrier == nil {
		return
	}
	defer carrier.Close()
	if control != nil && errors.Is(err, ErrClosed) {
		select {
		case <-carrier.Done():
			return // already-lost transport: local close is idempotent
		default:
		}
		// FIN requests session cleanup. Keep this carrier (never a replacement)
		// alive until its reader consumes the core's cleanup receipt. CloseWrite
		// only queues FIN on both mux and QUIC; it is not a delivery guarantee.
		timeout := make(chan struct{})
		timer := time.AfterFunc(time.Second, func() { close(timeout); _ = carrier.Close() })
		defer timer.Stop()
		defer func() {
			select {
			case <-timeout:
				c.closeErr = fmt.Errorf("core session close was not acknowledged: %w", context.DeadlineExceeded)
			default:
			}
		}()
		if control.CloseWrite() == nil {
			for {
				c.mu.Lock()
				read := c.controlRead == control
				readErr := c.controlErr
				c.mu.Unlock()
				if read {
					c.closeErr = readErr
					break
				}
				select {
				case <-c.controlEnded:
				case <-carrier.Done():
					c.closeErr = errors.New("core session close was not acknowledged before the carrier ended")
					return
				}
			}
		} else {
			c.closeErr = errors.New("core session close could not send control FIN")
		}
	}
}

// Close ends the session. Work in the core continues; this client's leases
// and attachments are released after the core acknowledges cleanup. If the
// carrier was already lost, Close only stops local workers: core resources
// remain retained until its session grace expires. A live peer that fails to
// acknowledge returns an error; exceeding one second wraps context.DeadlineExceeded.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	<-c.stopped
	c.wg.Wait()
	return c.closeErr
}

// current returns the usable carrier, waiting while a lost one is replaced.
func (c *Client) current(ctx context.Context) (corewire.Conn, error) {
	for {
		c.mu.Lock()
		ready, carrier := c.ready, c.carrier
		err := c.err
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case <-ready:
			if carrier != nil {
				return carrier, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, c.Err()
		}
	}
}

// watchControl ends the client when the core ends the session on one
// carrier's control stream. A failing carrier is the supervisor's concern;
// it starts a watcher for the replacement's control stream.
func (c *Client) watchControl(control corewire.Stream) {
	defer c.wg.Done()
	body, err := corewire.NewReader(control, 1<<16).Next()
	var end coreprotocol.SessionEnd
	if err == nil {
		err = coreprotocol.Decode(body, &end)
	}
	c.mu.Lock()
	current := c.control == control
	ending := c.err != nil
	if current {
		c.controlRead = control
		if err != nil && !errors.Is(err, io.EOF) {
			c.controlErr = fmt.Errorf("core session close was not acknowledged: %w", err)
		} else {
			c.controlErr = nil
		}
		select {
		case c.controlEnded <- struct{}{}:
		default:
		}
	}
	c.mu.Unlock()
	if !current || ending {
		return
	}
	if err == nil {
		c.fail(fmt.Errorf("%w: %s", ErrSessionLost, end.Reason))
		return
	}
	if errors.Is(err, io.EOF) {
		c.fail(fmt.Errorf("%w: the core closed the session", ErrSessionLost))
	}
}

// supervise replaces a lost carrier by resuming the session over a new one.
func (c *Client) supervise(conn corewire.Conn) {
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case <-conn.Done():
		}
		if c.acquireCarrierChange(c.lifetime) != nil {
			return
		}
		c.mu.Lock()
		// An explicit upgrade may already have replaced the watched carrier.
		// Follow it in this same supervisor; do not start a second resumer.
		if c.carrier != nil && c.carrier != conn {
			conn = c.carrier
			c.mu.Unlock()
			c.releaseCarrierChange()
			continue
		}
		if c.carrier == conn {
			c.carrier, c.control, c.ready = nil, nil, make(chan struct{})
		}
		c.mu.Unlock()
		next, control, err := c.reconnect()
		if err != nil {
			c.releaseCarrierChange()
			c.fail(err)
			return
		}
		c.mu.Lock()
		if c.err != nil {
			c.mu.Unlock()
			_ = next.Close()
			c.releaseCarrierChange()
			return
		}
		c.carrier, c.control = next, control
		close(c.ready)
		c.mu.Unlock()
		c.wg.Add(1)
		go c.watchControl(control)
		c.releaseCarrierChange()
		linkResumes.Inc()
		conn = next
	}
}

func (c *Client) reconnect() (corewire.Conn, corewire.Stream, error) {
	deadline := time.Now().Add(c.grace)
	for delay := 50 * time.Millisecond; ; delay = min(delay*2, 2*time.Second) {
		select {
		case <-c.done:
			return nil, nil, c.Err()
		default:
		}
		ctx, cancel := context.WithTimeout(c.lifetime, 10*time.Second)
		c.mu.Lock()
		dial := c.dial
		c.mu.Unlock()
		conn, err := dial(ctx)
		if errors.Is(err, ErrNoCarrier) {
			cancel()
			return nil, nil, fmt.Errorf("%w: %v", ErrSessionLost, err)
		}
		if err == nil {
			resume := c.resume
			var welcome coreprotocol.Welcome
			var control corewire.Stream
			welcome, control, err = handshake(ctx, conn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: c.clientID, Resume: &resume})
			cancel()
			if err == nil && welcome.Resumed && welcome.Owner == c.owner && welcome.LocalHost == c.localHost {
				return conn, control, nil
			}
			_ = conn.Close()
			var fault *coreprotocol.Fault
			if errors.As(err, &fault) && fault.Code == "session_gone" || err == nil {
				return nil, nil, ErrSessionLost
			}
		} else {
			cancel()
		}
		if time.Now().After(deadline) {
			return nil, nil, ErrSessionLost
		}
		select {
		case <-c.done:
			return nil, nil, c.Err()
		case <-time.After(delay):
		}
	}
}

func randomID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

// CallOptions adjusts one command.
type CallOptions struct {
	// ID is the request identity; empty allocates one. Reusing an identity
	// with the same content returns the original outcome, never a replay.
	ID string
	// Progress receives the latest cumulative partial output.
	Progress func(string)
	// Class schedules the command on shared carriers; zero is interactive.
	Class corewire.Class
}

// Call performs one command. A nil out discards the result.
func (c *Client) Call(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target) error {
	_, err := c.CallWith(ctx, workspaceID, method, in, out, CallOptions{})
	return err
}

// CallWith performs one command and returns the core revision it reported.
// A carrier lost while it runs is replaced and the command re-attached by its
// identity; only a lost session or cancellation leaves the outcome uncertain.
func (c *Client) CallWith(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target, opts CallOptions) (uint64, error) {
	return c.callWith(ctx, workspaceID, method, in, out, opts, commandBoundary{})
}

// commandBoundary supplies the read job's admission and confirmed-release
// boundaries without adding a second framing, retry or result-decoding path.
// Ordinary calls retain their best-effort final ACK and known-result semantics.
type commandBoundary struct {
	admitted    func()
	acknowledge func(corewire.Stream, *corewire.Reader) error
}

func (c *Client) callWith(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target, opts CallOptions, boundary commandBoundary) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	id := opts.ID
	if id == "" {
		id = randomID()
	}
	class := opts.Class
	if class == corewire.ClassControl {
		class = corewire.ClassInteractive
	}
	m := corewire.NewMessage()
	defer m.Release()
	var err error
	open := coreprotocol.Open{Kind: coreprotocol.StreamCommand, ID: id, WorkspaceID: workspaceID, Method: method}
	if m.B, err = open.MarshalMsg(m.B); err == nil {
		m.B, err = in.MarshalMsg(m.B)
	}
	if err != nil {
		return 0, err
	}
	framed, err := m.Framed()
	if err != nil {
		return 0, err
	}
	started := time.Now()
	defer func() {
		elapsed := time.Since(started)
		clientRequests.Observe(method, elapsed)
		metrics.Blocking("core request", method, elapsed)
	}()
	sent := false
	for {
		conn, err := c.current(ctx)
		if err != nil {
			if sent {
				return 0, &UncertainError{RequestID: id, Cause: err, State: "uncertain"}
			}
			return 0, err
		}
		revision, written, retry, err := c.attempt(ctx, conn, class, framed, out, opts.Progress, boundary)
		sent = sent || written
		if retry {
			continue
		}
		var cleanup *finalCleanupError
		if errors.As(err, &cleanup) {
			return revision, &UncertainError{RequestID: id, Cause: err, State: "completed_cleanup_unconfirmed", ResultKnown: true}
		}
		var fault *coreprotocol.Fault
		if errors.As(err, &fault) && (fault.Code == "outcome_pending" || fault.Code == "outcome_uncertain") {
			return revision, &UncertainError{RequestID: id, Cause: err, State: fault.Code[len("outcome_"):]}
		}
		if err != nil && sent && isTransport(err) {
			return revision, &UncertainError{RequestID: id, Cause: err, State: "uncertain"}
		}
		return revision, err
	}
}

// attempt runs a command over one carrier. written reports that the request
// may have reached the core; retry that the carrier was lost and the command
// must be re-attached by its identity on the replacement.
func (c *Client) attempt(ctx context.Context, conn corewire.Conn, class corewire.Class, framed []byte, out coreprotocol.Target, progress func(string), boundary commandBoundary) (revision uint64, written, retry bool, err error) {
	s, err := conn.OpenStream(ctx, class)
	if err != nil {
		return 0, false, ctx.Err() == nil && carrierLost(conn), err
	}
	defer s.Close()
	stop := joinedAfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	_, err = s.Write(framed)
	if err != nil {
		return 0, true, ctx.Err() == nil && carrierLost(conn), err
	}
	reader := corewire.NewReader(s, corewire.MaxMessage)
	for {
		body, err := reader.Next()
		if err != nil {
			if ctx.Err() != nil {
				return 0, true, false, ctx.Err()
			}
			return 0, true, carrierLost(conn), err
		}
		var reply coreprotocol.Reply
		rest, err := coreprotocol.DecodePrefix(body, &reply)
		if err != nil {
			return 0, true, false, err
		}
		if reply.Admitted && boundary.admitted != nil {
			boundary.admitted()
		}
		if !reply.Final {
			if progress != nil && reply.Progress != "" {
				progress(reply.Progress)
			}
			continue
		}
		if out != nil && (reply.Fault == nil || len(rest) > 0) {
			if err := coreprotocol.Decode(rest, out); err != nil {
				return reply.Revision, true, false, fmt.Errorf("decode result: %w", err)
			}
		}
		// Once decoded, the original outcome is known even if this ACK or its
		// confirmation is lost. Never replay a known result just to release it.
		if boundary.acknowledge != nil {
			if err := boundary.acknowledge(s, reader); err != nil {
				cause := err
				if reply.Fault != nil {
					cause = errors.Join(err, reply.Fault)
				}
				return reply.Revision, true, false, &finalCleanupError{cause: cause}
			}
		} else if sendValues(s, &coreprotocol.TopicAck{Seq: 1}) == nil {
			_ = s.CloseWrite()
			_, _ = reader.Next() // server EOF confirms release; cancellation closes s
		}
		if reply.Fault != nil {
			return reply.Revision, true, false, reply.Fault
		}
		return reply.Revision, true, false, nil
	}
}

// carrierLost waits briefly for a failing stream's carrier to report its
// end, distinguishing carrier loss from a stream-level failure.
func carrierLost(conn corewire.Conn) bool {
	select {
	case <-conn.Done():
		return true
	case <-time.After(250 * time.Millisecond):
		select {
		case <-conn.Done():
			return true
		default:
			return false
		}
	}
}

// isTransport reports a failure that says nothing about the command's
// outcome: anything but a typed fault from the core.
func isTransport(err error) bool {
	var fault *coreprotocol.Fault
	return !errors.As(err, &fault)
}

// Subscription is one topic stream that follows the session across carriers.
type Subscription struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Subscription) Close() { s.cancel(); <-s.done }

// Done closes when the subscription ended (Close or a lost session).
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Subscribe follows a topic. handle runs on the subscription's goroutine for
// each update in order; header.Reset marks a full snapshot. After a carrier
// change it resubscribes from the last applied sequence. An error from
// handle ends the subscription.
func (c *Client) Subscribe(ctx context.Context, workspaceID, topic, key string, handle func(header coreprotocol.TopicHeader, body []byte) error) *Subscription {
	ctx, cancel := context.WithCancel(ctx)
	sub := &Subscription{cancel: cancel, done: make(chan struct{})}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		cancel()
		close(sub.done)
		return sub
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer close(sub.done)
		var since uint64
		for {
			conn, err := c.current(ctx)
			if err != nil {
				return
			}
			s, err := conn.OpenStream(ctx, corewire.ClassInteractive)
			if err != nil {
				if !carrierLost(conn) {
					return
				}
				continue
			}
			err = c.follow(ctx, s, coreprotocol.Open{Kind: coreprotocol.StreamTopic, WorkspaceID: workspaceID, Topic: topic, Key: key, Since: since}, func(header coreprotocol.TopicHeader, body []byte) error {
				if err := handle(header, body); err != nil {
					return err
				}
				since = header.Seq
				return nil
			})
			_ = s.Close()
			if errors.Is(err, ErrTopicReset) && ctx.Err() == nil {
				since = 0
				continue
			}
			if ctx.Err() != nil || !carrierLost(conn) {
				if err != nil && ctx.Err() == nil {
					metrics.Note(metrics.GroupRPC, "Topic subscription ended", topic, err.Error(), "warn")
				}
				return
			}
		}
	}()
	return sub
}

func (c *Client) follow(ctx context.Context, s corewire.Stream, open coreprotocol.Open, handle func(coreprotocol.TopicHeader, []byte) error) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	open.DisplayIntervalNS = int64(c.DisplayInterval())
	if err := sendValues(s, &open); err != nil {
		return err
	}
	reader := corewire.NewReader(s, corewire.MaxMessage)
	for {
		body, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var header coreprotocol.TopicHeader
		rest, err := coreprotocol.DecodePrefix(body, &header)
		if err != nil {
			return err
		}
		if err := handle(header, rest); err != nil {
			return err
		}
		if err := sendValues(s, &coreprotocol.TopicAck{Seq: header.Seq, DisplayIntervalNS: int64(c.DisplayInterval())}); err != nil {
			return err
		}
	}
}

// sessionTopic receives core-initiated requests for this session.
func (c *Client) sessionTopic() {
	defer c.wg.Done()
	sub := c.Subscribe(context.Background(), "", coreprotocol.TopicSession, "", func(_ coreprotocol.TopicHeader, body []byte) error {
		var deliveryErr error
		defer func() {
			if deliveryErr != nil {
				c.fail(fmt.Errorf("%w: invalid session event: %v", ErrSessionLost, deliveryErr))
			}
		}()
		var event coreprotocol.SessionEvent
		if err := coreprotocol.Decode(body, &event); err != nil {
			deliveryErr = err
			return err
		}
		switch {
		case event.Control != nil:
			deliveryErr = c.receiveControl(event.WorkspaceID, *event.Control)
		case event.CapabilityHost != nil:
			deliveryErr = c.receiveCapabilityHost(event.WorkspaceID, *event.CapabilityHost)
		case event.TerminalClipboard != nil:
			c.receivePrivateEffect(event)
		default:
			deliveryErr = errors.New("empty session event")
		}
		return deliveryErr
	})
	select {
	case <-c.done:
		sub.Close()
	case <-sub.Done():
		// The control reader alone owns the peer's session ending. The core
		// closes topics while releasing resources, before publishing its final
		// cleanup receipt; closing the carrier here would lose that receipt.
		<-c.done
	}
	close(c.controls)
	close(c.hosts.requests)
}

// receivePrivateEffect never waits for presentation. Refusal reserves one
// coalesced notice outside the accepted FIFO, leaving request delivery live.
func (c *Client) receivePrivateEffect(event coreprotocol.SessionEvent) {
	offer := event.TerminalClipboard
	size := len(offer.TerminalID) + 32
	for _, value := range offer.Clipboard {
		size += len(value) + 8
	}
	c.privateMu.Lock()
	if c.privateCount >= coreprotocol.SessionPrivateMaxOutstanding || size > coreprotocol.SessionPrivateMaxBytes-c.privateBytes {
		if c.privateFailure == nil {
			c.privateFailure = &PrivateEffect{WorkspaceID: event.WorkspaceID, Error: "Terminal clipboard offer refused: the shell's private inbox is full or the offer exceeds its 1 MiB limit. Copy again after the shell catches up."}
		}
	} else {
		c.privateQueue = append(c.privateQueue, PrivateEffect{WorkspaceID: event.WorkspaceID, TerminalClipboard: offer})
		c.privateCount++
		c.privateBytes += size
	}
	c.privateMu.Unlock()
	select {
	case c.privateWake <- struct{}{}:
	default:
	}
}

func (c *Client) deliverPrivateEffects() {
	defer c.wg.Done()
	defer close(c.privateEffects)
	for {
		c.privateMu.Lock()
		var effect PrivateEffect
		switch {
		case c.privateFailure != nil:
			effect, c.privateFailure = *c.privateFailure, nil
		case len(c.privateQueue) > 0:
			effect = c.privateQueue[0]
			c.privateQueue[0] = PrivateEffect{}
			c.privateQueue = c.privateQueue[1:]
		default:
			c.privateMu.Unlock()
			select {
			case <-c.privateWake:
				continue
			case <-c.done:
				return
			}
		}
		c.privateMu.Unlock()
		select {
		case c.privateEffects <- effect:
		case <-c.done:
			return
		}
		if offer := effect.TerminalClipboard; offer != nil {
			size := len(offer.TerminalID) + 32
			for _, value := range offer.Clipboard {
				size += len(value) + 8
			}
			c.privateMu.Lock()
			c.privateCount--
			c.privateBytes -= size
			c.privateMu.Unlock()
		}
	}
}

// CapabilityHostRequests delivers MCP requests this shell hosts.
func (c *Client) CapabilityHostRequests() <-chan captypes.HostRequest { return c.hosts.requests }

var (
	clientRequests = metrics.NewTable(metrics.Meta{Name: "link.requests", Group: metrics.GroupRPC, Title: "Shell requests by method",
		Help: "Round trip from the shell to the core and back, per method. Compare with the core's own time to separate transport from work.",
		Warn: 100_000, Crit: 1_000_000})
	linkResumes = metrics.NewCounter(metrics.Meta{Name: "link.resume", Group: metrics.GroupRPC, Title: "Session resumes",
		Help: "Carrier losses survived by resuming the session over a new carrier."})
)

// SingleCarrier dials one already-connected byte stream as the session's only
// carrier (tests, and transports owned by their caller). The session cannot
// resume once that stream ends.
func SingleCarrier(rwc io.ReadWriteCloser) Dialer {
	var once sync.Once
	return func(context.Context) (corewire.Conn, error) {
		var conn corewire.Conn
		once.Do(func() { conn = corewire.NewMux(rwc, corewire.CarrierLocal, true) })
		if conn == nil {
			return nil, ErrNoCarrier
		}
		return conn, nil
	}
}

// encoded is a request value that is already encoded.
type encoded []byte

func (e encoded) MarshalMsg(b []byte) ([]byte, error) { return append(b, e...), nil }
func (e encoded) Msgsize() int                        { return len(e) }

// rawResult keeps a result value encoded.
type rawResult []byte

func (r *rawResult) UnmarshalMsg(b []byte) ([]byte, error) {
	*r = append((*r)[:0], b...)
	return b[len(b):], nil
}

// CallRequest performs a request whose payload is already encoded, under its
// exact identity, and returns the response with its result still encoded. A
// typed fault from the core is reported in the response, not as an error.
func (c *Client) CallRequest(ctx context.Context, request coreprotocol.Request) (coreprotocol.Response, error) {
	var out rawResult
	revision, err := c.CallWith(ctx, request.WorkspaceID, request.Method, encoded(request.Payload), &out, CallOptions{ID: request.ID})
	var fault *coreprotocol.Fault
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) && errors.As(err, &fault) {
		return coreprotocol.Response{ID: request.ID, Revision: revision, Error: fault}, nil
	}
	if err != nil {
		return coreprotocol.Response{}, err
	}
	return coreprotocol.Response{ID: request.ID, Revision: revision, Payload: out}, nil
}
