package question

import (
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
)

func (g *focusGate) ControlActions() []control.Descriptor {
	p, ok := g.child.(ui.ControlProvider)
	if !ok {
		return nil
	}
	out := p.ControlActions()
	// These wrappers own ordinary question drafts, never permission controls.
	for i := range out {
		out[i].Effect = control.Write
	}
	if _, ok := g.child.(*answerInput); ok {
		out = append(out, control.Descriptor{ID: "activate", Scope: control.Shell, Effect: control.Write, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: control.Availability{Available: true}})
	}
	if _, ok := g.child.(*ui.List); ok {
		out = append(out, control.Descriptor{ID: "activate", Scope: control.Shell, Effect: control.Write, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: control.Availability{Available: true}})
	}
	return out
}
func (g *focusGate) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	p, ok := g.child.(ui.ControlProvider)
	if !ok {
		return ui.EventResult{}, &control.Fault{Code: control.Unavailable, Message: "question control unavailable"}
	}
	return p.HandleControl(ctx, op)
}
func (a *answerInput) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	before := a.Value()
	res, err := a.TextArea.HandleControl(ctx, op)
	if err == nil && before != a.Value() && a.onChange != nil {
		a.onChange(a.Value())
		res.Action = nil
		res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return res, err
}

func (a *answerList) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	a.reflow(max(1, a.width))
	result, err := a.VirtualList.HandleControl(ctx, op)
	if result.Action != nil {
		if item, ok := result.Action.Payload.(ui.VirtItem); ok {
			if row, ok := item.Payload.(wrappedAnswer); ok {
				result.Action.Payload = row.source
			}
		}
	}
	return result, err
}
func (s *Screen) ControlMetadata(_ ui.ComponentID, _ string) (control.Effect, bool, error) {
	return control.Write, false, nil
}
