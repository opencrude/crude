package coreclient

import (
	"context"
	"crypto/sha256"
	"errors"

	"crude/coreprotocol"
	"crude/types/control"
)

type receivedControl struct {
	workspace string
	readOnly  bool
	bytes     int
	reply     []byte
	digest    [32]byte
}

func (c *Client) ControlRequests() <-chan coreprotocol.ControlEnvelope { return c.controls }

// RegisterControlShell is explicit, passive transport registration. It neither
// starts a consumer nor authorizes external/agent control of this shell.
func (c *Client) RegisterControlShell(ctx context.Context, workspaceID string) error {
	return c.Call(ctx, workspaceID, coreprotocol.ControlShellRegister, &coreprotocol.Empty{}, nil)
}

func (c *Client) ReplyControl(ctx context.Context, workspaceID string, result control.Result) error {
	result = result.Clone()
	if err := coreprotocol.ValidateControlResult(result); err != nil {
		return err
	}
	b, err := coreprotocol.Marshal(&result)
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
	err = c.Call(ctx, workspaceID, coreprotocol.ControlShellReply, &result, nil)
	if p.readOnly && err == nil {
		c.mu.Lock()
		if c.controlReceived[result.RequestID] == p {
			delete(c.controlReceived, result.RequestID)
			c.controlBytes -= p.bytes
		}
		c.mu.Unlock()
	}
	return err
}

// receiveControl admits one control request delivered on the session topic.
func (c *Client) receiveControl(workspaceID string, envelope coreprotocol.ControlEnvelope) error {
	size := envelope.Msgsize()
	if workspaceID == "" || size > coreprotocol.ControlMaxEnvelopeBytes {
		return errors.New("invalid control request")
	}
	if err := envelope.ValidateTransport(workspaceID, control.ShellOwner{ClientID: c.owner.ClientID, Epoch: c.owner.Epoch}); err != nil {
		return err
	}
	id := envelope.Request.ID
	b, err := coreprotocol.Marshal(&envelope)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if prior := c.controlReceived[id]; prior != nil {
		if prior.workspace != workspaceID || prior.digest != digest {
			return errors.New("control identity reused with changed content")
		}
		// The existing consumer or immutable reply owns reconciliation. A
		// duplicate delivery must never enqueue another shell effect.
		return nil
	}
	mutations := 0
	for _, receipt := range c.controlReceived {
		if !receipt.readOnly {
			mutations++
		}
	}
	if envelope.Descriptor.Effect != control.Read && mutations >= 256 || c.controlBytes+size > coreprotocol.ControlMaxQueuedBytes {
		return errors.New("control receive capacity exceeded")
	}
	select {
	case c.controls <- envelope.Clone():
		c.controlReceived[id] = &receivedControl{workspace: workspaceID, readOnly: envelope.Descriptor.Effect == control.Read, bytes: size, digest: digest}
		c.controlBytes += size
		return nil
	default:
		return errors.New("control request consumer queue full")
	}
}
