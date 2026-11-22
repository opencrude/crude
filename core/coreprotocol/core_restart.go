package coreprotocol

import "crude/types/handover"

const (
	CoreRestartReserve  = "core.restart.reserve"
	CoreRestartInspect  = "core.restart.inspect"
	CoreRestartCancel   = "core.restart.cancel"
	CoreRestartContinue = "core.restart.continue"
)

// The local application calls these typed routes on its existing connection.
// Reserve is authorized by the original still-pending semantic shell control;
// continuation is authorized by a private single-use operation permit.
type CoreRestartReservationRequest struct {
	ControlRequestID string               `json:"control_request_id"`
	SessionID        string               `json:"session_id"`
	Reservation      handover.Reservation `json:"reservation"`
}

type CoreRestartStateRequest struct {
	SessionID   string `json:"session_id"`
	OperationID string `json:"operation_id"`
}

type CoreRestartPermitRequest struct {
	SessionID   string `json:"session_id"`
	OperationID string `json:"operation_id"`
	Token       string `json:"token"`
}

type CoreRestartResult struct {
	State handover.State `json:"state"`
}
