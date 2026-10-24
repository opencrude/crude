package core

import (
	"context"
	"crude/coreprotocol"
	"strings"
)

// QuestionControlMethod accepts only a still-pending admitted shell operation.
// The shell can supply answers, but cannot manufacture an agent caller/run.
func (c *Connection) QuestionControlMethod(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	ready := make(chan coreprotocol.Response, 1)
	fail := func(code, message string) <-chan coreprotocol.Response {
		ready <- fault(request, code, message)
		return ready
	}
	if request.Owner != c.Owner {
		return fail("invalid_owner", "question connection owner changed")
	}
	var input coreprotocol.QuestionControlRequest
	if err := decodeRequest(request, &input); err != nil {
		return fail("invalid_request", err.Error())
	}
	original, ok := c.ControlAdmission(input.ControlRequestID, request.WorkspaceID)
	if !ok || original.CommandID != "shell.control" || original.Target.SessionID != input.SessionID || original.Target.Shell == nil || original.Target.Shell.ClientID != c.Owner.ClientID || original.Target.Shell.Epoch != c.Owner.Epoch {
		return fail("denied", "exact admitted question control required")
	}
	var intent struct {
		ComponentID    string `json:"component_id"`
		ExpectedAction string `json:"expected_action"`
		Operation      struct {
			Verb string `json:"verb"`
		} `json:"operation"`
	}
	if err := decodeJSON(original.Input, &intent); err != nil || intent.Operation.Verb != "activate" || !strings.HasPrefix(intent.ComponentID, "deck/"+input.SessionID+"/") || !strings.Contains(intent.ComponentID, "::session-question/") || (intent.ExpectedAction != "question.submit" && intent.ExpectedAction != "question.cancel") {
		return fail("denied", "admission is not an ordinary question decision")
	}
	c.mu.Lock()
	a := c.attached[request.WorkspaceID]
	c.mu.Unlock()
	if a == nil {
		return fail("detached", "question workspace detached")
	}
	command := actorCommand{inspect: func() {
		// Recheck pending admission AND live caller/run at the actual effect boundary.
		if _, ok := c.ControlAdmission(input.ControlRequestID, request.WorkspaceID); !ok {
			ready <- fault(request, "stale", "question control admission settled")
			return
		}
		if _, err := a.controlCaller(original.Caller, original.Target); err != nil {
			ready <- fault(request, "denied", err.Error())
			return
		}
		if ctx.Err() != nil {
			ready <- fault(request, "cancelled", ctx.Err().Error())
			return
		}
		accepted := a.ws.ResolveQuestionControl(original.Caller, input.SessionID, input.AgentID, input.BlockID, input.Answers, input.Questions, input.Cancelled)
		if accepted {
			a.changed([]string{input.AgentID})
		}
		ready <- response(request, coreprotocol.QuestionControlResult{Accepted: accepted, BlockID: input.BlockID})
	}}
	// No workspace maps are read on this goroutine.
	select {
	case a.commands <- command:
	case <-ctx.Done():
		return fail("cancelled", ctx.Err().Error())
	case <-a.done:
		return fail("detached", "question actor closed")
	}
	return ready
}
