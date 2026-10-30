package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/types/handover"
)

var _ shellport.CoreRestartPort = (*Workspace)(nil)

func (w *Workspace) ReserveCoreRestart(ctx context.Context, controlID, sessionID string, reservation handover.Reservation) (handover.State, error) {
	var out coreprotocol.CoreRestartResult
	err := w.call(ctx, coreprotocol.CoreRestartReserve, &coreprotocol.CoreRestartReservationRequest{
		ControlRequestID: controlID, SessionID: sessionID, Reservation: reservation,
	}, &out)
	return out.State, err
}

func (w *Workspace) InspectCoreRestart(ctx context.Context, sessionID, operationID string) (handover.State, error) {
	var out coreprotocol.CoreRestartResult
	err := w.call(ctx, coreprotocol.CoreRestartInspect, &coreprotocol.CoreRestartStateRequest{
		SessionID: sessionID, OperationID: operationID,
	}, &out)
	return out.State, err
}

func (w *Workspace) CancelCoreRestart(ctx context.Context, sessionID, operationID, token string) (handover.State, error) {
	var out coreprotocol.CoreRestartResult
	err := w.call(ctx, coreprotocol.CoreRestartCancel, &coreprotocol.CoreRestartPermitRequest{
		SessionID: sessionID, OperationID: operationID, Token: token,
	}, &out)
	return out.State, err
}

func (w *Workspace) ContinueCoreRestart(ctx context.Context, sessionID, operationID, token string) (handover.State, error) {
	// A mounted successor's control stream must be registered before its
	// operation-specific continuation can bind the new exact shell lifetime.
	client, workspaceID := w.conn()
	if client == nil || w.ctx.Err() != nil {
		return handover.State{}, errDisconnected
	}
	if err := client.RegisterControlShell(ctx, workspaceID); err != nil {
		return handover.State{}, err
	}
	var out coreprotocol.CoreRestartResult
	err := w.call(ctx, coreprotocol.CoreRestartContinue, &coreprotocol.CoreRestartPermitRequest{
		SessionID: sessionID, OperationID: operationID, Token: token,
	}, &out)
	return out.State, err
}
