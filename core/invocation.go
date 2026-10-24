package core

import (
	"crude/coreprotocol"
	"crude/workspace"
	"errors"
)

// prepareInvocation is called on the actor before dispatching the lifecycle
// worker. Receipts retain only a payload digest, never the write-only selection.
func (a *actor) prepareInvocation(request coreprotocol.Request) (*workspace.InvocationLifecycleOperation, error) {
	var input coreprotocol.InvocationSessionRequest
	if err := decodeRequest(request, &input); err != nil {
		return nil, errors.New("invalid invocation request")
	}
	if (request.Method == coreprotocol.OpenSession || request.Method == coreprotocol.ForkSession) && input.SessionID == "" {
		return nil, errors.New("session identity is required")
	}
	if request.Method == coreprotocol.ForkSession {
		if !input.Selection.Empty() {
			return nil, errors.New("a fork keeps its source's connection; open it without selectors")
		}
		return a.ws.PrepareInvocationFork(input.SessionID)
	}
	if request.Method == coreprotocol.NewSession && input.SessionID != "" {
		return nil, errors.New("new session must not name an existing session")
	}
	return a.ws.PrepareInvocationSession(input.SessionID, input.Selection)
}
