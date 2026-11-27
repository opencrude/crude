package core

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/internal/buildinfo"
)

// DataUpdateServer answers shells while the core updates saved data, before it
// opens any database or serves sessions. A shell of this build gets the
// update's progress (FaultCoreUpdating, then a progress stream on request); a
// shell of another build gets the usual mismatch refusal. A lifecycle stop is
// refused while databases are written and accepted otherwise.
type DataUpdateServer struct {
	owner    string
	mu       sync.Mutex
	status   coreprotocol.DataUpdate
	changed  chan struct{}
	writing  bool
	watchers int
	idleAt   time.Time
	retry    chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

// NewDataUpdateServer starts with the checking phase of this build.
func NewDataUpdateServer(owner string) *DataUpdateServer {
	return &DataUpdateServer{owner: owner,
		status:  coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateChecking, Version: buildinfo.VersionString()},
		changed: make(chan struct{}),
		idleAt:  time.Now(),
		retry:   make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

// Publish replaces the status every watcher receives next. Writing reports
// that databases are being written, which refuses a lifecycle stop.
func (s *DataUpdateServer) Publish(status coreprotocol.DataUpdate, writing bool) {
	// The publisher keeps changing its own slices; watchers encode this copy.
	status.Stores = slices.Clone(status.Stores)
	status.Problems = slices.Clone(status.Problems)
	status.Newer = slices.Clone(status.Newer)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.writing = status, writing
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *DataUpdateServer) snapshot() (coreprotocol.DataUpdate, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.changed
}

// Retry delivers a shell's request to run the update again.
func (s *DataUpdateServer) Retry() <-chan struct{} { return s.retry }

// Stopped closes when a lifecycle stop was accepted.
func (s *DataUpdateServer) Stopped() <-chan struct{} { return s.stop }

// IdleFor reports how long no shell has watched the update.
func (s *DataUpdateServer) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers > 0 {
		return 0
	}
	return time.Since(s.idleAt)
}

func (s *DataUpdateServer) watch(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watchers += delta
	if s.watchers == 0 {
		s.idleAt = time.Now()
	}
}

// Serve answers every carrier the listener accepts until ctx ends, then joins
// them. Watchers receive the final status before their carrier closes.
func (s *DataUpdateServer) Serve(ctx context.Context, listener *HandoffListener) error {
	var carriers sync.WaitGroup
	defer carriers.Wait()
	for {
		conn, err := listener.AcceptContext(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		carriers.Add(1)
		go func() {
			defer carriers.Done()
			s.serveCarrier(ctx, corewire.NewMux(conn, corewire.CarrierLocal, false))
		}()
	}
}

// serveCarrier answers one local or SSH-relayed carrier. Network carriers do
// not reach the core during the update: remote listeners start afterwards.
func (s *DataUpdateServer) serveCarrier(ctx context.Context, conn corewire.Conn) {
	// The carrier stays open after ctx ends until a watcher has its last
	// status; every path below returns promptly once ctx ends.
	defer conn.Close()
	// A peer that stops reading must not hold the handoff open forever.
	endDrain := make(chan struct{})
	defer close(endDrain)
	go func() {
		select {
		case <-endDrain:
			return
		case <-ctx.Done():
		}
		select {
		case <-endDrain:
		case <-time.After(handshakeTimeout):
			_ = conn.Close()
		}
	}()
	timer := time.AfterFunc(handshakeTimeout, func() { _ = conn.Close() })
	// A carrier accepted at the end of the update still gets its answer: the
	// handshake timer, not the update, bounds it.
	control, err := conn.AcceptStream(context.WithoutCancel(ctx))
	if err != nil || control.Class() != corewire.ClassControl {
		timer.Stop()
		return
	}
	reader := corewire.NewReader(control, 64<<10)
	body, err := reader.Next()
	timer.Stop()
	if err != nil {
		return
	}
	finish := func() {
		_ = control.CloseWrite()
		_, _ = reader.Next() // peer FIN drains the last message before close
	}
	if coreprotocol.IsLifecycleStop(body) {
		input, err := coreprotocol.DecodeLifecycleStop(body)
		if err != nil {
			return
		}
		s.mu.Lock()
		writing := s.writing
		s.mu.Unlock()
		reason := ""
		accepted := input.Force || !writing
		if input.ExpectedOwner != "" && (input.ExpectedOwner != s.owner || input.Force) {
			accepted = false
			reason = "The running core changed."
		} else if accepted {
			s.stopOnce.Do(func() { close(s.stop) })
		} else {
			reason = "Crude is updating its saved data. It stops when the update is done."
		}
		_ = corewire.WriteMessage(control, coreprotocol.EncodeLifecycleResult(accepted, reason))
		finish()
		return
	}
	welcome := coreprotocol.Welcome{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, CoreVersion: buildinfo.VersionString()}
	hello, fault := admitHello(body)
	if fault != nil {
		welcome.Fault = fault
		_ = send(control, &welcome)
		finish()
		return
	}
	if hello.DataUpdate == nil {
		status, _ := s.snapshot()
		welcome.Fault = &coreprotocol.Fault{Code: coreprotocol.FaultCoreUpdating, Message: "Crude is updating its saved data. Wait for the update to finish."}
		welcome.DataUpdate = &status
		_ = send(control, &welcome)
		finish()
		return
	}
	if hello.DataUpdate.Retry {
		select {
		case s.retry <- struct{}{}:
		default:
		}
	}
	s.watch(1)
	defer s.watch(-1)
	// The peer ends a watch by closing its side; the update keeps running.
	peerGone := make(chan struct{})
	go func() {
		defer close(peerGone)
		for {
			if _, err := reader.Next(); err != nil {
				return
			}
		}
	}()
	var sent time.Time
	for {
		status, changed := s.snapshot()
		welcome.DataUpdate = &status
		if err := send(control, &welcome); err != nil {
			return
		}
		if status.Final || ctx.Err() != nil {
			// The last status of this update phase. A watcher that did not
			// get a final one dials again and learns what the core does next.
			_ = control.CloseWrite()
			select {
			case <-peerGone:
			case <-time.After(2 * time.Second):
			}
			return
		}
		sent = time.Now()
		select {
		case <-changed:
		case <-peerGone:
			return
		case <-ctx.Done():
			continue
		}
		// Progress coalesces: at most ten messages a second per watcher.
		if wait := 100*time.Millisecond - time.Since(sent); wait > 0 {
			select {
			case <-time.After(wait):
			case <-peerGone:
				return
			case <-ctx.Done():
			}
		}
	}
}

// RetainDataUpdate makes a completed, nonempty update visible to the first
// shell even when no shell was watching during the update.
func (r *Runtime) RetainDataUpdate(s coreprotocol.DataUpdate) {
	if s.Final && s.Phase == coreprotocol.DataUpdateFinished && !s.Quiet() {
		r.pendingDataUpdate.Store(&s)
	}
}

// HandoffListener owns one accept loop on the core's endpoint, so the data
// update can answer shells first and core.Serve can take the same endpoint
// afterwards without closing it.
type HandoffListener struct {
	inner  net.Listener
	conns  chan net.Conn
	done   chan struct{} // the accept loop ended; err says why
	quit   chan struct{} // Close was called
	err    error
	closed sync.Once
}

// NewHandoffListener starts accepting on inner.
func NewHandoffListener(inner net.Listener) *HandoffListener {
	l := &HandoffListener{inner: inner, conns: make(chan net.Conn), done: make(chan struct{}), quit: make(chan struct{})}
	go func() {
		defer close(l.done)
		for {
			conn, err := inner.Accept()
			if err != nil {
				l.err = err
				return
			}
			select {
			case l.conns <- conn:
			case <-l.quit:
				_ = conn.Close()
				return
			}
		}
	}()
	return l
}

// AcceptContext returns the next connection, or the context's error.
func (l *HandoffListener) AcceptContext(ctx context.Context) (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, errors.Join(net.ErrClosed, l.err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Accept implements net.Listener for core.Serve.
func (l *HandoffListener) Accept() (net.Conn, error) {
	return l.AcceptContext(context.Background())
}

// Close closes the endpoint and ends the accept loop.
func (l *HandoffListener) Close() error {
	var err error
	l.closed.Do(func() {
		close(l.quit)
		err = l.inner.Close()
	})
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// Addr implements net.Listener.
func (l *HandoffListener) Addr() net.Addr { return l.inner.Addr() }

var _ io.Closer = (*HandoffListener)(nil)
