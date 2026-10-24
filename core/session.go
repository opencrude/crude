package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

// DefaultSessionGrace is how long a session outlives its last carrier. Within
// it, any carrier (QUIC after a roam, SSH when UDP disappears) resumes the
// session with its attachments, leases, subscriptions and in-flight commands.
const DefaultSessionGrace = 15 * time.Minute

var errSessionGone = errors.New("session ended or is unknown")

// sessionState is the carrier-independent part of a Connection.
type sessionState struct {
	id     string
	secret string

	carrierMu sync.Mutex
	carrier   corewire.Conn
	control   corewire.Stream
	grace     *time.Timer

	commandMu    sync.Mutex
	commands     map[string]*command
	commandBytes int

	topicMu            sync.Mutex
	topicCancel        map[string]context.CancelFunc
	topicRunning       map[string]chan struct{}
	terminalViews      map[string]*terminalViewState
	documentTopicViews map[string]*documentTopicView
	// One unacknowledged event is retained, independent of carrier writes.
	// Stop-and-wait leaves further requests in their bounded owner inboxes.
	sessionSeq   uint64
	sessionAck   uint64
	sessionEvent []byte
}

func randomSecret() string {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

// SetSessionGrace configures how long sessions survive carrier loss.
func (r *Runtime) SetSessionGrace(grace time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if grace > 0 {
		r.grace = grace
	}
}

func (r *Runtime) sessionGrace() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.grace > 0 {
		return r.grace
	}
	return DefaultSessionGrace
}

// openSession resumes the session a Hello names, or starts a new one that
// replaces any earlier session of the same client identity.
func (r *Runtime) openSession(hello coreprotocol.Hello) (*Connection, bool, error) {
	if hello.Resume != nil {
		r.mu.Lock()
		if r.draining {
			r.mu.Unlock()
			return nil, false, errors.New("core is draining")
		}
		c := r.sessions[hello.Resume.SessionID]
		r.mu.Unlock()
		if c == nil || c.Owner.ClientID != hello.ClientID || subtle.ConstantTimeCompare([]byte(c.secret), []byte(hello.Resume.Secret)) != 1 {
			return nil, false, errSessionGone
		}
		select {
		case <-c.done:
			return nil, false, errSessionGone
		default:
		}
		return c, true, nil
	}
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		return nil, false, errors.New("core is draining")
	}
	previous := r.clients[hello.ClientID]
	r.mu.Unlock()
	if previous != nil {
		previous.Close()
	}
	c, err := r.Connect(hello.ClientID)
	if err != nil {
		return nil, false, err
	}
	return c, false, nil
}

// bind makes conn the session's carrier, closing any carrier it replaces.
func (c *Connection) bind(conn corewire.Conn, control corewire.Stream) {
	c.carrierMu.Lock()
	select {
	case <-c.done:
		c.carrierMu.Unlock()
		_ = conn.Close()
		return
	default:
	}
	old := c.carrier
	c.carrier, c.control = conn, control
	if c.grace != nil {
		c.grace.Stop()
		c.grace = nil
	}
	c.carrierMu.Unlock()
	if old != nil && old != conn {
		_ = old.Close()
	}
}

// unbind starts the grace period when conn was the current carrier.
func (c *Connection) unbind(conn corewire.Conn) {
	c.carrierMu.Lock()
	defer c.carrierMu.Unlock()
	if c.carrier != conn {
		return
	}
	c.carrier, c.control = nil, nil
	if c.closing {
		return
	}
	c.grace = time.AfterFunc(c.runtime.sessionGrace(), c.Close)
}

// Carrier reports the current carrier kind and RTT; empty while detached.
func (c *Connection) Carrier() (corewire.Carrier, time.Duration) {
	c.carrierMu.Lock()
	defer c.carrierMu.Unlock()
	if c.carrier == nil {
		return "", 0
	}
	return c.carrier.Carrier(), c.carrier.RTT()
}

// command is one admitted request. It outlives the stream that carried it,
// so a client resuming after carrier loss re-attaches by request identity
// instead of executing the request twice.
type command struct {
	request  coreprotocol.Request
	done     chan struct{}
	response coreprotocol.Response
	progress chan string
}

// Unacknowledged results are never evicted to admit another command. At most
// sixteen results (each bounded by MaxMessage) and 128 MiB of request preimages
// survive a carrier; session expiry releases them. Durable receipts separately
// protect journaled mutations across process crashes.
const sessionCommandLimit = 16
const sessionCommandBytes = 128 << 20

var errCommandCapacity = errors.New("unacknowledged command capacity exceeded")

// commandKey identifies a command in flight. Distinct methods may share a
// request identity (resolving a control, then executing it).
func commandKey(request coreprotocol.Request) string { return request.Method + "\x00" + request.ID }

// admit returns the in-flight command for request, starting it if new.
func (c *Connection) admit(ctx context.Context, request coreprotocol.Request) (*command, error) {
	c.commandMu.Lock()
	if existing := c.commands[commandKey(request)]; existing != nil {
		c.commandMu.Unlock()
		if existing.request.Method != request.Method || existing.request.WorkspaceID != request.WorkspaceID || !bytes.Equal(existing.request.Payload, request.Payload) {
			return nil, errors.New("request identity reused with different content")
		}
		return existing, nil
	}
	if len(c.commands) >= sessionCommandLimit || c.commandBytes+len(request.Payload) > sessionCommandBytes {
		c.commandMu.Unlock()
		return nil, errCommandCapacity
	}
	cmd := &command{request: request, done: make(chan struct{}), progress: make(chan string, 1)}
	if c.commands == nil {
		c.commands = make(map[string]*command)
	}
	c.commands[commandKey(request)] = cmd
	c.commandBytes += len(request.Payload)
	c.commandMu.Unlock()
	// Carrier replacement abandons only the waiter, not the admitted operation.
	pending := c.Dispatch(c.ctx, request)
	go func() {
		select {
		case cmd.response = <-pending:
		case <-c.done:
			cmd.response = fault(request, "outcome_uncertain", "session ended; inspect the receipt before retrying")
		}
		close(cmd.done)
	}()
	return cmd, nil
}

// forget drops a command only after the client acknowledged its final result.
func (c *Connection) forget(cmd *command) {
	c.commandMu.Lock()
	if key := commandKey(cmd.request); c.commands[key] == cmd {
		delete(c.commands, key)
		c.commandBytes -= len(cmd.request.Payload)
	}
	c.commandMu.Unlock()
}

// reportProgress offers the latest cumulative progress text to the stream
// awaiting the command; an unread older note is replaced.
func (c *Connection) reportProgress(requestID, text string) {
	c.commandMu.Lock()
	var cmd *command
	for _, candidate := range c.commands {
		if candidate.request.ID == requestID {
			cmd = candidate
			break
		}
	}
	c.commandMu.Unlock()
	if cmd == nil {
		return
	}
	for {
		select {
		case cmd.progress <- text:
			return
		default:
		}
		select {
		case <-cmd.progress:
		default:
		}
	}
}

// claimTopic gives one stream exclusive delivery of a session topic. A
// resumed subscription replaces the stream of the carrier it outlived.
func (c *Connection) claimTopic(ctx context.Context, key string, closeStream ...func()) (context.Context, func()) {
	c.topicMu.Lock()
	if c.topicCancel == nil {
		c.topicCancel = make(map[string]context.CancelFunc)
		c.topicRunning = make(map[string]chan struct{})
	}
	if cancel := c.topicCancel[key]; cancel != nil {
		cancel()
	}
	previous := c.topicRunning[key]
	ctx, cancel := context.WithCancel(ctx)
	var closeOnce sync.Once
	interrupt := func() {
		cancel()
		closeOnce.Do(func() {
			for _, close := range closeStream {
				close()
			}
		})
	}
	running := make(chan struct{})
	c.topicCancel[key], c.topicRunning[key] = interrupt, running
	c.topicMu.Unlock()
	if previous != nil {
		// Even a cancelled replacement must join its predecessor: otherwise a
		// third claimant could skip that still-running owner through this one.
		<-previous
	}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			interrupt()
			c.topicMu.Lock()
			if c.topicRunning[key] == running {
				delete(c.topicCancel, key)
				delete(c.topicRunning, key)
			}
			c.topicMu.Unlock()
			close(running)
		})
	}
}

// stopWorkspaceTopics joins exact producers before dropping their retained
// bases. Carrier loss never calls this: only explicit workspace/session detach.
func (c *Connection) stopWorkspaceTopics(workspaceID string) {
	prefix := workspaceID + "/"
	c.topicMu.Lock()
	var joined []chan struct{}
	for key, cancel := range c.topicCancel {
		if strings.HasPrefix(key, prefix) {
			cancel()
			joined = append(joined, c.topicRunning[key])
		}
	}
	c.topicMu.Unlock()
	for _, done := range joined {
		if done != nil {
			<-done
		}
	}
	c.topicMu.Lock()
	for key := range c.terminalViews {
		if strings.HasPrefix(key, prefix) {
			delete(c.terminalViews, key)
		}
	}
	for key := range c.documentTopicViews {
		if strings.HasPrefix(key, prefix) {
			delete(c.documentTopicViews, key)
		}
	}
	c.topicMu.Unlock()
}
