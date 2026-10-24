package coreclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"crude/coreprotocol"
	captypes "crude/types/capability"
	"crude/types/control"
)

// hostInbox bounds MCP requests this shell hosts and fences their replies:
// a lost acknowledgement may resend the same reply, never a changed one.
type hostInbox struct {
	mu       sync.Mutex
	requests chan captypes.HostRequest
	received map[string]captypes.HostRequest
	replies  map[string][]byte
	bytes    int
}

func newHostInbox() *hostInbox {
	return &hostInbox{requests: make(chan captypes.HostRequest, 16), received: map[string]captypes.HostRequest{}, replies: map[string][]byte{}}
}

func (c *Client) RegisterCapabilityHost(ctx context.Context, workspace string) error {
	return c.Call(ctx, workspace, coreprotocol.CapabilityHostRegister, &coreprotocol.Empty{}, nil)
}

func (c *Client) receiveCapabilityHost(workspaceID string, r captypes.HostRequest) error {
	size := r.Msgsize()
	if size > coreprotocol.CapabilityHostMaxBytes {
		return errors.New("MCP request exceeds budget")
	}
	if err := coreprotocol.ValidateCapabilityHostRequest(r, workspaceID, control.ShellOwner{ClientID: c.owner.ClientID, Epoch: c.owner.Epoch}); err != nil {
		return err
	}
	h := c.hosts
	h.mu.Lock()
	defer h.mu.Unlock()
	if prior, exists := h.received[r.ID]; exists {
		// JSON's sorted map keys provide a stable preimage; generated map wire
		// order is not an identity and can differ on an exact redelivery.
		old, err := json.Marshal(prior)
		if err != nil {
			return err
		}
		next, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(old, next) {
			return errors.New("MCP request identity reused with changed content")
		}
		return nil // retained consumer/reply owns the original request
	}
	if len(h.received) >= 256 || h.bytes+size > 4<<20 {
		return errors.New("MCP receive receipts exhausted")
	}
	encoded, err := coreprotocol.Marshal(&r)
	if err != nil {
		return err
	}
	var delivered captypes.HostRequest
	if err := coreprotocol.Decode(encoded, &delivered); err != nil {
		return err
	}
	select {
	case h.requests <- delivered:
		h.received[r.ID] = r
		h.bytes += size
		return nil
	default:
		return errors.New("MCP host inbox full")
	}
}

func (c *Client) ReplyCapabilityHost(ctx context.Context, workspace string, reply captypes.HostReply) error {
	b, err := coreprotocol.Marshal(&reply)
	if err != nil {
		return err
	}
	if len(b) > coreprotocol.CapabilityHostMaxBytes {
		return errors.New("MCP reply exceeds budget")
	}
	h := c.hosts
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
	err = c.Call(ctx, workspace, coreprotocol.CapabilityHostReply, &reply, nil)
	if err == nil && captypes.HostRequestReadOnly(r) {
		h.mu.Lock()
		h.bytes -= r.Msgsize() + len(h.replies[reply.ID])
		delete(h.received, reply.ID)
		delete(h.replies, reply.ID)
		h.mu.Unlock()
	}
	return err
}
