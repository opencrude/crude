package coreclient

import (
	"context"
	"crude/coreprotocol"
	"crude/corewire"
	captypes "crude/types/capability"
	"crude/types/control"
	"encoding/json"
	"errors"
	"sync"
)

type hostInbox struct {
	mu       sync.Mutex
	requests chan captypes.HostRequest
	received map[string]captypes.HostRequest
	replies  map[string][]byte
	bytes    int
	cleanup  sync.Once
}

var hostInboxes sync.Map

func (c *Client) hostInbox() *hostInbox {
	v, _ := hostInboxes.LoadOrStore(c, &hostInbox{requests: make(chan captypes.HostRequest, 16), received: map[string]captypes.HostRequest{}, replies: map[string][]byte{}})
	h := v.(*hostInbox)
	h.cleanup.Do(func() { go func() { <-c.done; hostInboxes.Delete(c) }() })
	return h
}
func (c *Client) CapabilityHostRequests() <-chan captypes.HostRequest { return c.hostInbox().requests }
func (c *Client) RegisterCapabilityHost(ctx context.Context, workspace string) error {
	return c.Call(ctx, workspace, coreprotocol.CapabilityHostRegister, struct{}{}, nil)
}
func (c *Client) ReceiveCapabilityHost(f corewire.Frame) error {
	if f.Kind != corewire.KindEvent || f.Method != coreprotocol.CapabilityHostRequest || f.ClientID != c.owner.ClientID || f.Epoch != c.owner.Epoch || f.Error != nil || f.Transfer != nil || f.Revision != 0 || len(f.Payload) > coreprotocol.CapabilityHostMaxBytes {
		return errors.New("invalid MCP shell frame")
	}
	var r captypes.HostRequest
	if err := json.Unmarshal(f.Payload, &r); err != nil {
		return err
	}
	if f.RequestID != r.ID {
		return errors.New("MCP request identity mismatch")
	}
	if err := coreprotocol.ValidateCapabilityHostRequest(r, f.WorkspaceID, control.ShellOwner{ClientID: c.owner.ClientID, Epoch: c.owner.Epoch}); err != nil {
		return err
	}
	h := c.hostInbox()
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.received[r.ID]; exists {
		return errors.New("duplicate MCP host delivery")
	}
	if len(h.received) >= 256 || h.bytes+len(f.Payload) > 4<<20 {
		return errors.New("MCP receive receipts exhausted")
	}
	select {
	case h.requests <- r:
		h.received[r.ID] = r
		h.bytes += len(f.Payload)
		return nil
	default:
		return errors.New("MCP host inbox full")
	}
}
func (c *Client) ReplyCapabilityHost(ctx context.Context, workspace string, reply captypes.HostReply) error {
	b, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	if len(b) > coreprotocol.CapabilityHostMaxBytes {
		return errors.New("MCP reply exceeds budget")
	}
	h := c.hostInbox()
	h.mu.Lock()
	r, exists := h.received[reply.ID]
	if !exists || r.WorkspaceID != workspace {
		h.mu.Unlock()
		return errors.New("MCP reply not owned")
	}
	if prior := h.replies[reply.ID]; prior != nil && string(prior) != string(b) {
		h.mu.Unlock()
		return errors.New("MCP reply changed")
	}
	if h.replies[reply.ID] == nil {
		if h.bytes+len(b) > 4<<20 {
			h.mu.Unlock()
			return errors.New("MCP reply receipt budget exhausted")
		}
		h.bytes += len(b)
	}
	h.replies[reply.ID] = b
	h.mu.Unlock()
	err = c.Call(ctx, workspace, coreprotocol.CapabilityHostReply, reply, nil)
	if err == nil && captypes.HostRequestReadOnly(r) {
		h.mu.Lock()
		encoded, _ := json.Marshal(r)
		h.bytes -= len(encoded) + len(h.replies[reply.ID])
		delete(h.received, reply.ID)
		delete(h.replies, reply.ID)
		h.mu.Unlock()
	}
	return err
}
