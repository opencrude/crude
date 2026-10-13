package memorybrowser

import (
	"crude/tui/ui"
	"crude/types/control"
)

// The wrappers retain human routing; semantic collections expose the very same
// native model so offscreen item identities and source revisions stay inspectable.
func (p *browserList) ControlChildren() []ui.Component { return []ui.Component{p.list} }
func (r *readingArea) ControlChildren() []ui.Component { return []ui.Component{r.area} }

func (f *searchField) ControlActions() []control.Descriptor { return f.field.ControlActions() }
func (f *searchField) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	before := f.field.Value()
	result, err := f.field.HandleControl(ctx, op)
	if err == nil && before != f.field.Value() {
		result = result.Merge(f.owner.commandResult(f.owner.Refresh()))
	}
	// The wrapper owns refresh; text.changed must not escape to unrelated owners.
	result.Action = nil
	return result, err
}

func (s *Screen) ControlEditGeneration() uint64 {
	return s.epoch + s.searchSequence + s.detailSequence + s.mutationSequence
}

// ResolveControlAction binds overflow choices to the current enabled action,
// never to the previously selected row or an arbitrary caller supplied prefix.
func (s *Screen) ResolveControlAction(id ui.ComponentID, action string, op ui.ControlOperation) (string, error) {
	if action != "memorybrowser.action" {
		return action, nil
	}
	if id != ActionListID || op.Verb != "activate" {
		return "", &control.Fault{Code: control.Invalid, Message: "invalid memory action target"}
	}
	for _, item := range s.actionList.Items() {
		if item.ID == op.ItemID && item.Selectable() {
			return item.ID, nil
		}
	}
	return "", &control.Fault{Code: control.Unavailable, Message: "memory action unavailable"}
}
