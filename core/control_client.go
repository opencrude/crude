package core

import (
	"context"
	"errors"

	"crude/coreprotocol"
	"crude/types/control"
)

func (c *Connection) controlClientMethod(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	complete := func(value any, err error) {
		if err != nil {
			ready <- fault(request, "control_failed", err.Error())
		} else {
			ready <- response(request, value)
		}
	}
	c.mu.Lock()
	a := c.attached[request.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		complete(nil, errors.New("control workspace is not attached"))
		return ready
	}
	if request.Method == coreprotocol.ControlClientRegister {
		var grant coreprotocol.ControlClientGrant
		if err := decode(request.Payload, &grant); err != nil {
			complete(nil, err)
			return ready
		}
		if len(grant.Effects) == 0 || len(grant.Effects) > 5 {
			complete(nil, errors.New("select bounded explicit control effects"))
			return ready
		}
		effects := map[control.Effect]bool{}
		for _, effect := range grant.Effects {
			switch effect {
			case control.Read, control.Write, control.Delete, control.Execute, control.Communication:
				effects[effect] = true
			default:
				complete(nil, errors.New("invalid external control effect"))
				return ready
			}
		}
		if grant.Shell != nil {
			if err := grant.Shell.Validate(); err != nil {
				complete(nil, err)
				return ready
			}
			if !c.runtime.controlShellAvailable(request.WorkspaceID, *grant.Shell) {
				complete(nil, errors.New("exact control shell is not registered"))
				return ready
			}
		}
		c.mu.Lock()
		if c.controlExternal[request.WorkspaceID] != nil {
			c.mu.Unlock()
			complete(nil, errors.New("external control grant is immutable for this connection"))
			return ready
		}
		c.controlExternal[request.WorkspaceID] = effects
		if grant.Shell != nil {
			owner := *grant.Shell
			c.controlTargets[request.WorkspaceID] = &owner
		}
		c.mu.Unlock()
		complete(struct{}{}, nil)
		return ready
	}
	caller := control.Caller{Kind: control.ExternalCaller, ClientID: c.Owner.ClientID}
	runtime := &actorControlRuntime{actor: a}
	go func() {
		switch request.Method {
		case coreprotocol.ControlShells:
			var empty struct{}
			if err := decode(request.Payload, &empty); err != nil {
				complete(nil, err)
				return
			}
			complete(c.runtime.controlShells(request.WorkspaceID), nil)
		case coreprotocol.ControlDescribe:
			var input coreprotocol.ControlDescribeRequest
			if err := decode(request.Payload, &input); err != nil {
				complete(nil, err)
				return
			}
			input.Target.WorkspaceID = request.WorkspaceID
			values, err := runtime.Describe(ctx, caller, input.Target, input.Query)
			complete(values, err)
		case coreprotocol.ControlResolve, coreprotocol.ControlExecute:
			var input control.Request
			if err := decode(request.Payload, &input); err != nil {
				complete(nil, err)
				return
			}
			if input.ID != request.ID {
				complete(nil, errors.New("control request and RPC identity differ"))
				return
			}
			input.Caller = caller
			input.Target.WorkspaceID = request.WorkspaceID
			c.mu.Lock()
			bound := c.controlTargets[request.WorkspaceID]
			if bound != nil {
				owner := *bound
				if input.Target.Shell == nil {
					input.Target.Shell = &owner
				}
			}
			permitted := input.Target.Shell == nil || bound != nil && *input.Target.Shell == *bound
			c.mu.Unlock()
			if !permitted {
				complete(nil, errors.New("control shell is outside external launch grant"))
				return
			}
			if request.Method == coreprotocol.ControlResolve {
				value, err := runtime.Resolve(ctx, input)
				complete(value, err)
			} else {
				value, err := runtime.Execute(ctx, input)
				if value.RequestID != "" {
					ready <- response(request, value)
				} else {
					complete(nil, err)
				}
			}
		default:
			complete(nil, errors.New("unknown control client method"))
		}
	}()
	return ready
}

func (r *Runtime) controlShellAvailable(workspace string, owner control.ShellOwner) bool {
	for _, info := range r.controlShells(workspace) {
		if info.Owner == owner {
			return true
		}
	}
	return false
}

func (r *Runtime) controlShells(workspace string) []coreprotocol.ControlShellInfo {
	r.mu.Lock()
	clients := make([]*Connection, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	r.mu.Unlock()
	var result []coreprotocol.ControlShellInfo
	for _, client := range clients {
		client.mu.Lock()
		if client.attached[workspace] != nil && client.controlRegistered[workspace] {
			result = append(result, coreprotocol.ControlShellInfo{WorkspaceID: workspace, Owner: control.ShellOwner{ClientID: client.Owner.ClientID, Epoch: client.Owner.Epoch}})
		}
		client.mu.Unlock()
		if len(result) == 128 {
			break
		}
	}
	return result
}
