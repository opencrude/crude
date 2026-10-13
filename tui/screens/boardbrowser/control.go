package boardbrowser

import (
	"crude/tui/ui"
	"crude/types/control"
)

func (s *Screen) OwnsRequest(request Request) bool {
	return request.Target == s && request.Sequence == s.sequence && request.Query == s.filter.Value() && request.Thread == s.thread && request.Page == s.page
}

func (s *Screen) ResolveControlAction(id ui.ComponentID, action string, op ui.ControlOperation) (string, error) {
	if action != "board.action" {
		return action, nil
	}
	if id != s.narrowActions.ID() || op.Verb != "activate" {
		return "", &control.Fault{Code: control.Invalid, Message: "invalid Board action target"}
	}
	for _, item := range s.narrowActions.Items() {
		if item.ID == op.ItemID && item.Selectable() {
			return item.ID, nil
		}
	}
	return "", &control.Fault{Code: control.Unavailable, Message: "Board action unavailable"}
}
