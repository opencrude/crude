package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"

	"crude/coreprotocol"
	captypes "crude/types/capability"
)

type hostReceipt struct {
	workspace string
	digest    [32]byte
	done      chan struct{}
	reply     captypes.HostReply
	finished  bool
	bytes     int
}
type hostChannel struct {
	mu          sync.Mutex
	queue       chan captypes.HostRequest
	registered  map[string]bool
	receipts    map[string]*hostReceipt
	bytes       int
	outstanding int
	cleanup     sync.Once
}

func (h *hostChannel) cleanupOnce() bool {
	created := false
	h.cleanup.Do(func() { created = true })
	return created
}

var hostChannels sync.Map

func (c *Connection) hostChannel() *hostChannel {
	value, _ := hostChannels.LoadOrStore(c, &hostChannel{queue: make(chan captypes.HostRequest, 16), registered: map[string]bool{}, receipts: map[string]*hostReceipt{}})
	if value.(*hostChannel).cleanupOnce() {
		go func() { <-c.done; hostChannels.Delete(c) }()
	}
	return value.(*hostChannel)
}
func (c *Connection) CapabilityHostRequests() <-chan captypes.HostRequest {
	return c.hostChannel().queue
}
func (c *Connection) CapabilityHostPending(r captypes.HostRequest) bool {
	h := c.hostChannel()
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.receipts[r.ID]
	return p != nil && !p.finished && h.registered[r.WorkspaceID]
}
func (c *Connection) DetachCapabilityHosts(workspace string) {
	h := c.hostChannel()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, p := range h.receipts {
		if !p.finished && (workspace == "" || p.workspace == workspace) {
			p.reply = captypes.HostReply{ID: id, Result: captypes.Result{Unknown: true, IsError: true}, Error: "exact MCP shell detached; outcome unknown"}
			p.finished = true
			h.outstanding--
			close(p.done)
		}
	}
	if workspace == "" {
		h.registered = map[string]bool{}
	} else {
		delete(h.registered, workspace)
	}
}
func (r *Runtime) DispatchHost(ctx context.Context, request captypes.HostRequest) (captypes.HostReply, error) {
	if request.Actor.ShellOwner == nil {
		return captypes.HostReply{}, errors.New("missing exact MCP shell owner")
	}
	owner := *request.Actor.ShellOwner
	if err := coreprotocol.ValidateCapabilityHostRequest(request, request.WorkspaceID, owner); err != nil {
		return captypes.HostReply{}, err
	}
	r.mu.Lock()
	c := r.clients[owner.ClientID]
	valid := !r.closed && !r.draining && c != nil && c.Owner.Epoch == owner.Epoch
	r.mu.Unlock()
	if !valid {
		return captypes.HostReply{}, errors.New("exact MCP shell unavailable")
	}
	c.mu.Lock()
	attached := c.attached[request.WorkspaceID] != nil
	c.mu.Unlock()
	if !attached {
		return captypes.HostReply{}, errors.New("MCP workspace detached")
	}
	h := c.hostChannel()
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(encoded)
	h.mu.Lock()
	if !h.registered[request.WorkspaceID] {
		h.mu.Unlock()
		return captypes.HostReply{}, errors.New("MCP shell worker not registered")
	}
	p := h.receipts[request.ID]
	if p != nil {
		if p.digest != digest {
			h.mu.Unlock()
			return captypes.HostReply{}, errors.New("MCP request preimage changed")
		}
	} else {
		if len(h.receipts) >= 256 || h.outstanding >= 16 || h.bytes+len(encoded) > 4<<20 {
			h.mu.Unlock()
			return captypes.HostReply{}, errors.New("MCP receipt capacity exhausted")
		}
		p = &hostReceipt{workspace: request.WorkspaceID, digest: digest, done: make(chan struct{}), bytes: len(encoded)}
		select {
		case h.queue <- request:
			h.receipts[request.ID] = p
			h.bytes += len(encoded)
			h.outstanding++
		default:
			h.mu.Unlock()
			return captypes.HostReply{}, errors.New("MCP host queue full")
		}
	}
	h.mu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		h.mu.Lock()
		if !p.finished {
			p.reply = captypes.HostReply{ID: request.ID, Result: captypes.Result{Unknown: request.Operation == "call" && request.Tool.Effect != captypes.EffectRead, IsError: true}, Error: "MCP await cancelled; no replay"}
			p.finished = true
			h.outstanding--
			close(p.done)
		}
		h.mu.Unlock()
	case <-c.done:
		h.mu.Lock()
		if !p.finished {
			p.reply = captypes.HostReply{ID: request.ID, Result: captypes.Result{Unknown: request.Operation == "call" && request.Tool.Effect != captypes.EffectRead, IsError: true}, Error: "MCP shell disconnected; outcome must not be replayed"}
			p.finished = true
			h.outstanding--
			close(p.done)
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	reply := p.reply
	if captypes.HostRequestReadOnly(request) && h.receipts[request.ID] == p {
		delete(h.receipts, request.ID)
		h.bytes -= p.bytes
	}
	h.mu.Unlock()
	return reply, nil
}
func (c *Connection) CapabilityHostMethod(request coreprotocol.Request) coreprotocol.Response {
	c.mu.Lock()
	attached := c.attached[request.WorkspaceID] != nil
	c.mu.Unlock()
	if !attached {
		return fault(request, "detached", "MCP workspace detached")
	}
	h := c.hostChannel()
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.Method == coreprotocol.CapabilityHostRegister {
		var empty coreprotocol.Empty
		if err := decodeRequest(request, &empty); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		h.registered[request.WorkspaceID] = true
		return response(request, coreprotocol.Empty{})
	}
	var reply captypes.HostReply
	if err := decodeRequest(request, &reply); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	if len(request.Payload) > coreprotocol.CapabilityHostMaxBytes {
		return fault(request, "invalid_request", "MCP reply too large")
	}
	p := h.receipts[reply.ID]
	if p == nil || p.workspace != request.WorkspaceID || !h.registered[request.WorkspaceID] {
		return fault(request, "stale", "MCP reply has no exact request")
	}
	if p.finished {
		old, _ := json.Marshal(p.reply)
		next, _ := json.Marshal(reply)
		if string(old) != string(next) {
			return fault(request, "stale", "MCP outcome is immutable")
		}
	} else {
		if h.bytes-p.bytes+len(request.Payload) > 4<<20 {
			return fault(request, "busy", "MCP receipt byte budget exceeded")
		}
		h.bytes += len(request.Payload) - p.bytes
		p.bytes = len(request.Payload)
		p.reply = reply
		h.outstanding--
		p.finished = true
		close(p.done)
	}
	return response(request, coreprotocol.Empty{})
}
