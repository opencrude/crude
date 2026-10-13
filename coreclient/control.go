package coreclient

import (
	"context"
	"encoding/json"
	"errors"

	"crude/coreprotocol"
	"crude/corewire"
	"crude/types/control"
)

type receivedControl struct {
	workspace string
	readOnly  bool
	bytes     int
	reply     []byte
}

func (c *Client) ControlRequests() <-chan coreprotocol.ControlEnvelope { return c.controls }

// RegisterControlShell is explicit, passive transport registration. It neither
// starts a consumer nor authorizes external/agent control of this shell.
func (c *Client) RegisterControlShell(ctx context.Context, workspaceID string) error {
	return c.Call(ctx, workspaceID, coreprotocol.ControlShellRegister, struct{}{}, nil)
}

func (c *Client) ReplyControl(ctx context.Context, workspaceID string, result control.Result) error {
	result = result.Clone()
	if err := coreprotocol.ValidateControlResult(result); err != nil {
		return err
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	c.mu.Lock()
	p := c.controlReceived[result.RequestID]
	if p == nil || p.workspace != workspaceID {
		c.mu.Unlock()
		return errors.New("reply is not bound to a received control request")
	}
	if p.reply != nil && string(p.reply) != string(b) {
		c.mu.Unlock()
		return errors.New("control reply preimage is immutable")
	}
	if p.reply == nil {
		if c.controlBytes-p.bytes+len(b) > coreprotocol.ControlMaxQueuedBytes {
			c.mu.Unlock()
			return errors.New("control reply byte capacity exceeded")
		}
		// Reserve the immutable reply before enqueue. A lost acknowledgement may
		// resend this same reply, never repeat the original shell effect.
		p.reply = b
		c.controlBytes += len(b) - p.bytes
		p.bytes = len(b)
	}
	c.mu.Unlock()
	err = c.Call(ctx, workspaceID, coreprotocol.ControlShellReply, result, nil)
	if p.readOnly {
		c.mu.Lock()
		if c.controlReceived[result.RequestID] == p {
			delete(c.controlReceived, result.RequestID)
			c.controlBytes -= p.bytes
		}
		c.mu.Unlock()
	}
	return err
}

func (c *Client) receiveControl(f corewire.Frame) error {
	size, err := corewire.FrameSize(f)
	if err != nil || size > coreprotocol.ControlMaxEnvelopeBytes {
		return errors.New("encoded control frame exceeds limit")
	}
	if f.Kind != corewire.KindEvent || f.RequestID == "" || f.WorkspaceID == "" || f.Revision != 0 || f.Error != nil || f.Transfer != nil || len(f.Payload) > coreprotocol.ControlMaxEnvelopeBytes {
		return errors.New("invalid control request frame")
	}
	var envelope coreprotocol.ControlEnvelope
	if err := json.Unmarshal(f.Payload, &envelope); err != nil {
		return err
	}
	if envelope.Request.ID != f.RequestID {
		return errors.New("control request ID mismatch")
	}
	if err := envelope.ValidateTransport(f.WorkspaceID, control.ShellOwner{ClientID: c.owner.ClientID, Epoch: c.owner.Epoch}); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.controlReceived[f.RequestID] != nil {
		return errors.New("duplicate control delivery")
	}
	mutations := 0
	for _, receipt := range c.controlReceived {
		if !receipt.readOnly {
			mutations++
		}
	}
	if envelope.Descriptor.Effect != control.Read && mutations >= 256 || c.controlBytes+len(f.Payload) > coreprotocol.ControlMaxQueuedBytes {
		return errors.New("control receive capacity exceeded")
	}
	select {
	case c.controls <- envelope.Clone():
		c.controlReceived[f.RequestID] = &receivedControl{workspace: f.WorkspaceID, readOnly: envelope.Descriptor.Effect == control.Read, bytes: len(f.Payload)}
		c.controlBytes += len(f.Payload)
		return nil
	default:
		return errors.New("control request consumer queue full")
	}
}
