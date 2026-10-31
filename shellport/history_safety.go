package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/workspace"
)

// HistorySafety is native workspace retention administration. It never changes
// the original edit outcome, restores files or grants an agent a release permit.
type HistorySafety interface {
	ListHistorySafety(context.Context, workspace.HistorySafetyListRequest) (workspace.HistorySafetyPage, error)
	InspectHistorySafety(context.Context, string) (workspace.HistorySafetyDetail, error)
	ChangeHistorySafetyProtection(context.Context, workspace.HistorySafetyChangeRequest) (workspace.HistorySafetyChangeResult, error)
	HistorySafetyReceipt(context.Context, string) (coreprotocol.Receipt, error)
}
