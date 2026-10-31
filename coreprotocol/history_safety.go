package coreprotocol

import "crude/workspace"

const (
	HistorySafetyList    = "history.safety_list"
	HistorySafetyInspect = "history.safety_inspect"
	HistorySafetyChange  = "history.safety_change"
)

type HistorySafetyListRequest struct {
	Cursor string
	Limit  int
}

type HistorySafetyInspectRequest struct {
	Reference string
}

type HistorySafetyChangeRequest struct {
	Consent workspace.HistorySafetyChangeRequest
}

type HistorySafetyReply struct {
	Page   workspace.HistorySafetyPage
	Detail workspace.HistorySafetyDetail
	Result workspace.HistorySafetyChangeResult
}
