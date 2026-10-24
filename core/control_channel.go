package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"crude/coreprotocol"
	"crude/types/control"
)

// Epoch receipts are deliberately bounded and never evicted to make an old
// effect replayable. Exhaustion requires a new connection, not implicit replay.
const controlReceiptLimit = 256

type controlReceipt struct {
	workspace string
	request   control.Request
	readOnly  bool
	digest    [32]byte
	done      chan struct{}
	result    control.Result
	finished  bool
	bytes     int
}

func (c *Connection) Controls() <-chan coreprotocol.ControlEnvelope { return c.controls }

func uncertainControl(id string) control.Result {
	return control.Result{RequestID: id, OperationID: id, State: control.Uncertain, Fault: &control.Fault{Code: control.UnknownOutcome, Message: "Shell outcome unknown; explicitly inspect the exact shell and request before any new effect. Do not retry automatically."}}
}

// RequestShellControl never waits on an actor. Registration proves only an
// attached transport endpoint; it does not authenticate caller attribution.
func (r *Runtime) RequestShellControl(ctx context.Context, workspaceID string, target control.ShellOwner, envelope coreprotocol.ControlEnvelope) (control.Result, error) {
	if err := ctx.Err(); err != nil {
		return control.Result{}, err
	}
	envelope = envelope.Clone()
	if err := envelope.ValidateTransport(workspaceID, target); err != nil {
		return control.Result{}, err
	}
	r.mu.Lock()
	c := r.clients[target.ClientID]
	available := !r.closed && !r.draining && c != nil && c.Owner.Epoch == target.Epoch
	r.mu.Unlock()
	if !available {
		return control.Result{}, errors.New("exact control shell is unavailable or stale")
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return control.Result{}, err
	}
	digest := sha256.Sum256(b)
	id := envelope.Request.ID
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return control.Result{}, errors.New("control shell disconnected")
	default:
	}
	if c.attached[workspaceID] == nil || !c.controlRegistered[workspaceID] {
		c.mu.Unlock()
		return control.Result{}, errors.New("control shell is not registered for this attachment")
	}
	p := c.controlReceipts[id]
	if p != nil {
		if p.digest != digest || p.workspace != workspaceID {
			c.mu.Unlock()
			return control.Result{}, errors.New("control request identity reused with changed preimage")
		}
	} else {
		mutationReceipts := 0
		for _, receipt := range c.controlReceipts {
			if !receipt.readOnly {
				mutationReceipts++
			}
		}
		if c.controlOutstanding >= coreprotocol.ControlMaxOutstanding || envelope.Descriptor.Effect != control.Read && mutationReceipts >= controlReceiptLimit || c.controlBytes+len(b) > coreprotocol.ControlMaxQueuedBytes {
			c.mu.Unlock()
			return control.Result{}, errors.New("control shell capacity exceeded")
		}
		p = &controlReceipt{workspace: workspaceID, request: envelope.Request.Clone(), readOnly: envelope.Descriptor.Effect == control.Read, digest: digest, done: make(chan struct{}), bytes: len(b)}
		select {
		case c.controls <- envelope:
			c.controlReceipts[id] = p
			c.controlBytes += p.bytes
			c.controlOutstanding++
		default:
			c.mu.Unlock()
			return control.Result{}, errors.New("control shell queue full")
		}
	}
	c.mu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		c.mu.Lock()
		if !p.finished {
			c.finishControlLocked(p, uncertainControl(id))
			c.pruneControlsLocked()
		}
		c.mu.Unlock()
	case <-c.done:
		c.mu.Lock()
		if !p.finished {
			c.finishControlLocked(p, uncertainControl(id))
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	result := p.result.Clone()
	// Reads create no replayable effect. Do not cache an old contextual action
	// descriptor or exhaust the mutation-receipt budget on routine inspection.
	if p.readOnly && c.controlReceipts[id] == p {
		delete(c.controlReceipts, id)
		c.controlBytes -= p.bytes
	}
	c.mu.Unlock()
	if result.State == control.Uncertain {
		return result, fmt.Errorf("control request %s outcome uncertain; inspect explicitly", id)
	}
	return result, nil
}

// ControlAdmission returns trusted original attribution only while this exact
// shell operation is pending. It cannot authorize a different action or block.
func (c *Connection) ControlAdmission(id, workspace string) (control.Request, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.controlReceipts[id]
	if p == nil || p.finished || p.workspace != workspace || !c.controlRegistered[workspace] || c.attached[workspace] == nil {
		return control.Request{}, false
	}
	return p.request.Clone(), true
}

func (c *Connection) finishControlLocked(p *controlReceipt, result control.Result) {
	b, _ := json.Marshal(result)
	c.controlBytes += len(b) - p.bytes
	p.bytes = len(b)
	p.result = result.Clone()
	p.finished = true
	c.controlOutstanding--
	close(p.done)
}

// Remove cancelled/detached requests that have not yet left the core queue.
func (c *Connection) pruneControlsLocked() {
	var keep []coreprotocol.ControlEnvelope
	for {
		select {
		case e := <-c.controls:
			p := c.controlReceipts[e.Request.ID]
			if p != nil && !p.finished {
				keep = append(keep, e)
			}
		default:
			for _, e := range keep {
				c.controls <- e
			}
			return
		}
	}
}

func (c *Connection) detachControlsLocked(workspace string) {
	delete(c.controlRegistered, workspace)
	delete(c.controlExternal, workspace)
	delete(c.controlTargets, workspace)
	for id, p := range c.controlReceipts {
		if !p.finished && (workspace == "" || p.workspace == workspace) {
			c.finishControlLocked(p, uncertainControl(id))
		}
	}
	c.pruneControlsLocked()
}

// ControlPending fences dequeued requests before the server writes them. A
// request already in the wire can still arrive after cancellation; its outcome
// is explicitly uncertain and the shell must recheck effect-time fences.
func (c *Connection) ControlPending(e coreprotocol.ControlEnvelope) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.controlReceipts[e.Request.ID]
	return p != nil && !p.finished && c.attached[p.workspace] != nil && c.controlRegistered[p.workspace]
}

func (c *Connection) controlMethod(request coreprotocol.Request) coreprotocol.Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return fault(request, "detached", "control shell disconnected")
	default:
	}
	if c.attached[request.WorkspaceID] == nil {
		return fault(request, "not_attached", "control workspace is not attached")
	}
	if request.Method == coreprotocol.ControlShellRegister {
		var empty coreprotocol.Empty
		if err := decodeRequest(request, &empty); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		c.controlRegistered[request.WorkspaceID] = true
		return response(request, coreprotocol.Empty{})
	}
	var result control.Result
	if err := decodeRequest(request, &result); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	if err := coreprotocol.ValidateControlResult(result); err != nil {
		return fault(request, "invalid_request", err.Error())
	}
	p := c.controlReceipts[result.RequestID]
	if !c.controlRegistered[request.WorkspaceID] || p == nil || p.workspace != request.WorkspaceID {
		return fault(request, "stale_control", "reply does not own an admitted control request")
	}
	if p.finished {
		old, _ := json.Marshal(p.result)
		next, _ := json.Marshal(result)
		if string(old) != string(next) {
			return fault(request, "stale_control", "control result is immutable")
		}
		return response(request, coreprotocol.Empty{})
	}
	b, _ := json.Marshal(result)
	if c.controlBytes-p.bytes+len(b) > coreprotocol.ControlMaxQueuedBytes {
		return fault(request, "busy", "control receipt byte capacity exceeded")
	}
	c.finishControlLocked(p, result)
	return response(request, coreprotocol.Empty{})
}
