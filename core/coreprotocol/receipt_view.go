package coreprotocol

import (
	"time"

	"crude/types/control"
)

// ReceiptView is a receipt as a JSON document for semantic control callers:
// identity, state and fault, plus the result of a semantic control operation.
type ReceiptView struct {
	RequestID   string          `json:"request_id"`
	WorkspaceID string          `json:"workspace_id"`
	Method      string          `json:"method"`
	State       string          `json:"state"`
	CreatedAt   time.Time       `json:"created_at"`
	Error       *Fault          `json:"error,omitempty"`
	Result      *control.Result `json:"result,omitempty"`
}

func (r Receipt) View() ReceiptView {
	view := ReceiptView{RequestID: r.RequestID, WorkspaceID: r.WorkspaceID, Method: r.Method, State: r.State, CreatedAt: r.CreatedAt, Error: r.Response.Error}
	var result control.Result
	if r.Method == ControlExecute && Decode(r.Response.Payload, &result) == nil {
		view.Result = &result
	}
	return view
}
