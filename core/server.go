package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/corewire"
)

const handshakeTimeout = 10 * time.Second

// Authenticator admits a network carrier's Hello. Local and SSH carriers are
// authenticated by the operating system and pass nil.
type Authenticator func(context.Context, coreprotocol.Hello) (RemotePrincipal, error)

// Serve accepts local carriers (Unix socket, named pipe, SSH stdio relay).
// It owns the listener and carrier goroutines, but never closes Runtime.
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
		go func() {
			defer wg.Done()
			_ = ServeCarrier(ctx, runtime, corewire.NewMux(conn, corewire.CarrierLocal, false), nil)
		}()
	}
}

// ServeConnection serves one byte-stream carrier (a socket, pipe or stdio).
func ServeConnection(ctx context.Context, runtime *Runtime, rwc io.ReadWriteCloser) error {
	return ServeCarrier(ctx, runtime, corewire.NewMux(rwc, corewire.CarrierLocal, false), nil)
}

// ServeQUIC accepts network carriers; every Hello must pass auth.
func ServeQUIC(ctx context.Context, runtime *Runtime, listener *corewire.QUICListener, auth Authenticator) error {
	if auth == nil {
		return errors.New("network carriers require an authenticator")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() { defer wg.Done(); _ = ServeCarrier(ctx, runtime, conn, auth) }()
	}
}

// ServeCarrier runs one carrier: the session handshake on its first stream,
// then every stream the client opens. Losing the carrier leaves the session
// alive for its grace period; ending the control stream ends the session.
func ServeCarrier(ctx context.Context, runtime *Runtime, conn corewire.Conn, auth Authenticator) error {
	if auth == nil && conn.Carrier() != corewire.CarrierLocal && conn.Carrier() != corewire.CarrierSSH {
		_ = conn.Close()
		return errors.New("network carrier requires authentication")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	timer := time.AfterFunc(handshakeTimeout, func() { _ = conn.Close() })
	c, control, err := handshake(ctx, runtime, conn, auth)
	timer.Stop()
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	} // restricted pairing completed without a session
	select {
	case <-c.done:
		return errors.New("session revoked before carrier bind")
	default:
	}
	defer c.unbind(conn)
	controlDone := make(chan struct{})
	defer func() {
		cancel()
		_ = conn.Close()
		<-controlDone
	}()
	go func() {
		defer close(controlDone)
		// The client ends its session by closing the control stream; a reset
		// or a lost carrier is not an ending.
		if _, err := corewire.NewReader(control, 1).Next(); errors.Is(err, io.EOF) {
			c.closeCarrier(conn)
		}
		cancel()
	}()
	var streams sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.Close()
		streams.Wait()
	}()
	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			return err
		}
		streams.Add(1)
		go func() { defer streams.Done(); c.serveStream(ctx, s) }()
	}
}

func handshake(ctx context.Context, runtime *Runtime, conn corewire.Conn, auth Authenticator) (*Connection, corewire.Stream, error) {
	control, err := conn.AcceptStream(ctx)
	if err != nil {
		return nil, nil, err
	}
	if control.Class() != corewire.ClassControl {
		return nil, nil, errors.New("session must start on a control stream")
	}
	reader := corewire.NewReader(control, 64<<10)
	body, err := reader.Next()
	if err != nil {
		return nil, nil, err
	}
	if coreprotocol.IsLifecycleStop(body) {
		// Same-user local authority only. SSH and network admission cannot use
		// this schema-independent host lifecycle channel.
		if auth != nil || conn.Carrier() != corewire.CarrierLocal {
			return nil, nil, errors.New("lifecycle stop requires the local host")
		}
		input, err := coreprotocol.DecodeLifecycleStop(body)
		if err != nil {
			return nil, nil, err
		}
		result := runtime.shutdown(ctx, nil, input)
		if err := corewire.WriteMessage(control, coreprotocol.EncodeLifecycleResult(result.Accepted, result.Reason)); err != nil {
			return nil, nil, err
		}
		_ = control.CloseWrite()
		_, err = reader.Next() // FIN proves consumption, not merely enqueue.
		if !errors.Is(err, io.EOF) {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	var hello coreprotocol.Hello
	welcome := coreprotocol.Welcome{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild}
	refuse := func(code, message string) (*Connection, corewire.Stream, error) {
		welcome.Fault = &coreprotocol.Fault{Code: code, Message: message}
		_ = send(control, &welcome)
		_ = control.CloseWrite()
		_, _ = reader.Next() // peer FIN drains the typed refusal before close
		return nil, nil, errors.New(code + ": " + message)
	}
	if err := coreprotocol.Decode(body, &hello); err != nil {
		if schema, build, idErr := coreprotocol.HandshakeIdentity(body); idErr == nil && (schema != coreprotocol.Schema || build != buildinfo.ProtocolBuild) {
			return refuse("schema_mismatch", fmt.Sprintf("core speaks protocol %s (build %s); client speaks %s (build %s)", coreprotocol.Schema, buildinfo.ProtocolBuild, schema, build))
		}
		return refuse("invalid_hello", err.Error())
	}
	if hello.Schema != coreprotocol.Schema {
		return refuse("schema_mismatch", fmt.Sprintf("core speaks protocol %s (build %s); client speaks %s (build %s)", coreprotocol.Schema, buildinfo.ProtocolBuild, hello.Schema, hello.Build))
	}
	if !validID(hello.ClientID) {
		return refuse("invalid_hello", "invalid client identity")
	}
	if hello.Build != buildinfo.ProtocolBuild {
		return refuse("build_mismatch", "install the matching Crude build before connecting")
	}
	runtime.mu.Lock()
	draining := runtime.draining
	runtime.mu.Unlock()
	if draining {
		return refuse("core_draining", "core is draining; retry after its owner finishes shutting down")
	}
	if auth != nil && hello.SSHRelay {
		return refuse("unauthorized", "a network carrier cannot claim SSH authentication")
	}
	if hello.Pair != nil {
		if auth == nil || hello.Resume != nil || hello.Credential != "" || hello.SSHRelay || runtime.remote == nil {
			return refuse("invalid_pairing", "pairing is available only on the restricted direct enrollment endpoint")
		}
		credential, err := runtime.remote.Pair(ctx, *hello.Pair)
		if err != nil {
			return refuse("pairing_refused", err.Error())
		}
		welcome.Paired = &credential
		if err := send(control, &welcome); err != nil {
			return nil, nil, err
		}
		_ = control.CloseWrite()
		// A clean client FIN proves the pairing reply was consumed before the
		// carrier is closed. Never close immediately after enqueueing the reply.
		_, err = reader.Next()
		if !errors.Is(err, io.EOF) {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	runtime.remoteMu.Lock()
	var principal RemotePrincipal
	if hello.Resume == nil && auth != nil {
		var err error
		principal, err = auth(ctx, hello)
		if err != nil || principal.DeviceID == "" && !principal.SSHBootstrap {
			runtime.remoteMu.Unlock()
			return refuse("unauthorized", "remote credential is invalid, expired or revoked")
		}
	}
	c, resumed, err := runtime.openSession(hello)
	if err != nil {
		runtime.remoteMu.Unlock()
		code := "session_refused"
		if errors.Is(err, errSessionGone) {
			code = "session_gone"
		}
		return refuse(code, err.Error())
	}
	if resumed {
		c.mu.Lock()
		localHost, deviceID := c.localHost, c.principal.DeviceID
		c.mu.Unlock()
		if auth != nil && localHost {
			runtime.remoteMu.Unlock()
			return refuse("unauthorized", "a local-host session cannot resume over a network carrier")
		}
		if deviceID != "" && (runtime.remote == nil || runtime.remote.CheckDevice(ctx, deviceID) != nil) {
			runtime.remoteMu.Unlock()
			c.Close()
			return refuse("unauthorized", "remote device was disabled or revoked")
		}
	} else {
		c.mu.Lock()
		c.principal = principal
		c.localHost = auth == nil && conn.Carrier() == corewire.CarrierLocal && !hello.SSHRelay
		if auth == nil && (conn.Carrier() == corewire.CarrierSSH || hello.SSHRelay) {
			c.principal.SSHBootstrap = true
		}
		c.mu.Unlock()
	}
	runtime.remoteMu.Unlock()
	welcome.Owner, welcome.SessionID, welcome.Secret, welcome.Resumed, welcome.Grace = c.Owner, c.id, c.secret, resumed, runtime.sessionGrace()
	c.mu.Lock()
	welcome.LocalHost = c.localHost
	c.mu.Unlock()
	// Publish carrier ownership before Welcome makes the client usable. A core
	// close racing the completed handshake must notify this exact carrier.
	c.bind(conn, control)
	c.carrierMu.Lock()
	if c.closing || c.carrier != conn {
		c.carrierMu.Unlock()
		return nil, nil, errSessionGone
	}
	err = send(control, &welcome)
	c.carrierMu.Unlock()
	if err != nil {
		if !resumed {
			c.Close()
		} else {
			c.unbind(conn)
		}
		return nil, nil, err
	}
	return c, control, nil
}

// send writes one value as one message.
func send(s corewire.Stream, values ...coreprotocol.Value) error {
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

func (c *Connection) serveStream(ctx context.Context, s corewire.Stream) {
	defer s.Close()
	reader := corewire.NewReader(s, corewire.MaxMessage)
	body, err := reader.Next()
	if err != nil {
		return
	}
	var open coreprotocol.Open
	rest, err := coreprotocol.DecodePrefix(body, &open)
	if err != nil {
		return
	}
	switch open.Kind {
	case coreprotocol.StreamCommand:
		c.serveCommand(ctx, s, reader, open, rest)
	case coreprotocol.StreamTopic:
		c.serveTopic(ctx, s, reader, open)
	case coreprotocol.StreamInput:
		c.serveInput(ctx, s, reader, open)
	case coreprotocol.StreamBulk:
		if len(rest) == 0 && s.Class() == corewire.ClassBulk {
			reader.SetLimit(coreprotocol.BulkMessageBytes)
			c.serveBulk(ctx, s, reader, open)
		}
	}
}

func (c *Connection) serveCommand(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open, payload []byte) {
	switch open.Method {
	case coreprotocol.FilesRead, coreprotocol.ClientFilesReadExternal, coreprotocol.ReferenceReadContent, coreprotocol.ReferenceReadMedia, coreprotocol.HistoryReadMedia, coreprotocol.ReferenceUploadBegin, coreprotocol.ReferenceUploadChunk, coreprotocol.ReferenceUploadAbort:
		c.refuseCommand(ctx, s, reader, &coreprotocol.Fault{Code: "invalid_stream", Message: "content transfers require a bulk stream"})
		return
	}
	request := coreprotocol.Request{ID: open.ID, Owner: c.Owner, WorkspaceID: open.WorkspaceID, Method: open.Method, Payload: bytes.Clone(payload)}
	if !validID(open.ID) || open.Method == "" || open.WorkspaceID != "" && !validID(open.WorkspaceID) {
		c.refuseCommand(ctx, s, reader, &coreprotocol.Fault{Code: "invalid_request", Message: "invalid command identity"})
		return
	}
	admitted := time.Now()
	cmd, err := c.admit(ctx, request)
	if err != nil {
		code := "invalid_request"
		if errors.Is(err, errCommandCapacity) {
			code = "busy"
		}
		c.refuseCommand(ctx, s, reader, &coreprotocol.Fault{Code: code, Message: err.Error()})
		return
	}
	// Only an explicit final-result ACK releases the retained command. Neither
	// a successful write nor EOF proves the client decoded the result.
	acknowledged := make(chan bool, 1)
	go func() {
		body, err := reader.Next()
		var ack coreprotocol.TopicAck
		acknowledged <- err == nil && coreprotocol.Decode(body, &ack) == nil && ack.Seq == 1
	}()
	for {
		select {
		case text := <-cmd.progress:
			if send(s, &coreprotocol.Reply{Progress: text}) != nil {
				return
			}
		case <-cmd.done:
			coreRequests.Since(request.Method, admitted)
			response := cmd.response
			reply := coreprotocol.Reply{Final: true, Revision: response.Revision, Fault: response.Error}
			m := corewire.NewMessage()
			if m.B, err = reply.MarshalMsg(m.B); err == nil {
				m.B = append(m.B, response.Payload...)
				err = m.Send(s)
			}
			m.Release()
			if err != nil {
				return
			}
			select {
			case ok := <-acknowledged:
				if ok {
					c.forget(cmd)
				}
			case <-ctx.Done():
			case <-c.done:
			}
			_ = s.CloseWrite()
			return
		case <-acknowledged:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Even a refused command has a final-result boundary. Closing both directions
// immediately after queueing FIN can reset unread reply bytes (also on QUIC).
// Await the same applied ACK as admitted commands, but do not retain a journal
// entry for malformed input or let an uncooperative peer retain a worker forever.
func (c *Connection) refuseCommand(ctx context.Context, s corewire.Stream, reader *corewire.Reader, refusal *coreprotocol.Fault) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	if send(s, &coreprotocol.Reply{Final: true, Fault: refusal}) != nil {
		return
	}
	reader.SetLimit(256)
	body, err := reader.Next()
	var ack coreprotocol.TopicAck
	if err != nil || coreprotocol.Decode(body, &ack) != nil || ack.Seq != 1 {
		return
	}
	_ = s.CloseWrite()
}

func (c *Connection) serveTopic(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	switch open.Topic {
	case coreprotocol.TopicSession:
		c.serveSessionTopic(ctx, s, reader, open)
	case coreprotocol.TopicWorkspace:
		c.serveWorkspaceTopic(ctx, s, reader, open)
	case coreprotocol.TopicTerminal:
		c.serveTerminalTopic(ctx, s, reader, open)
	case coreprotocol.TopicDocument:
		c.serveDocumentTopic(ctx, s, reader, open)
	case coreprotocol.TopicInsight:
		c.serveInsightTopic(ctx, s, reader, open)
	case coreprotocol.TopicStatus, coreprotocol.TopicSettings, coreprotocol.TopicLSP, coreprotocol.TopicTerminalInventory, coreprotocol.TopicRegistry, coreprotocol.TopicGit, coreprotocol.TopicBoard:
		c.serveStateTopic(ctx, s, reader, open)
	default:
		_ = send(s, &coreprotocol.TopicHeader{}, &coreprotocol.Fault{Code: "unknown_topic", Message: open.Topic})
	}
}

func (c *Connection) serveInput(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	if open.Topic == coreprotocol.TopicTerminalInput {
		c.serveTerminalInput(ctx, s, reader, open)
		return
	}
	_ = send(s, &coreprotocol.Reply{Final: true, Fault: &coreprotocol.Fault{Code: "unknown_input", Message: open.Topic}})
}

// serveSessionTopic is a carrier-independent, stop-and-wait event journal.
// Successful writes stay retained until the client's cumulative applied ACK.
// One event (bounded by MaxMessage) plus the owners' bounded inboxes caps memory
// even when a live subscriber stops applying requests indefinitely.
func (c *Connection) serveSessionTopic(ctx context.Context, s corewire.Stream, reader *corewire.Reader, open coreprotocol.Open) {
	ctx, release := c.claimTopic(ctx, coreprotocol.TopicSession, func() { _ = s.Close() })
	defer release()
	if ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	acks := make(chan uint64)
	go func() {
		for {
			body, err := reader.Next()
			if err != nil {
				cancel()
				return
			}
			var ack coreprotocol.TopicAck
			if coreprotocol.Decode(body, &ack) != nil {
				cancel()
				return
			}
			select {
			case acks <- ack.Seq:
			case <-ctx.Done():
				return
			}
		}
	}()
	// claimTopic joins the previous owner before transferring journal ownership.
	if open.Since < c.sessionAck || open.Since > c.sessionSeq {
		return // session requests cannot be reconstructed by a snapshot
	}
	c.sessionAck = open.Since
	if c.sessionAck == c.sessionSeq {
		c.sessionEvent = nil
	}
	for {
		if c.sessionEvent != nil {
			if err := corewire.WriteMessage(s, c.sessionEvent); err != nil {
				return
			}
			observeTopicSent(ctx, c.sessionSeq, "")
			select {
			case seq := <-acks:
				if seq != c.sessionSeq {
					return
				}
				c.sessionAck = seq
				observeTopicAck(ctx, seq, 0)
				c.sessionEvent = nil
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
		}
		var event coreprotocol.SessionEvent
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case envelope := <-c.Controls():
			if !c.ControlPending(envelope) {
				continue
			}
			event = coreprotocol.SessionEvent{WorkspaceID: envelope.Request.Target.WorkspaceID, Control: &envelope}
		case request := <-c.CapabilityHostRequests():
			if !c.CapabilityHostPending(request) {
				continue
			}
			event = coreprotocol.SessionEvent{WorkspaceID: request.WorkspaceID, CapabilityHost: &request}
		case event = <-c.privateEffects:
			c.takePrivateEffect(event)
		case <-acks:
			return // ACK without an in-flight event
		}
		encoded, err := coreprotocol.Marshal(&coreprotocol.TopicHeader{Seq: c.sessionSeq + 1})
		if err == nil {
			encoded, err = event.MarshalMsg(encoded)
		}
		if err != nil || len(encoded) > corewire.MaxMessage {
			c.Close() // impossible to deliver safely; never silently drop a request
			return
		}
		c.sessionSeq++
		c.sessionEvent = encoded
	}
}
