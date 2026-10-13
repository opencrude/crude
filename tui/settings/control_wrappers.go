package settings

import (
	"crude/tui/ui"
	"crude/types/control"
	"encoding/json"
	"fmt"
)

func settingsControlActions(ids ...string) []control.Descriptor {
	out := make([]control.Descriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, control.Descriptor{ID: id, Scope: control.Shell, Effect: control.Write, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: control.Availability{Available: true}})
	}
	return out
}

func (s *settingsSearchField) ControlActions() []control.Descriptor {
	return settingsControlActions("set_value", "replace", "undo", "redo", "select_range", "activate")
}
func (s *settingsSearchField) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	if s.owner == nil || s.owner.model == nil || s.owner.model.Saving() {
		return ui.EventResult{}, fmt.Errorf("settings owner unavailable")
	}
	if op.Verb == "activate" {
		return s.owner.openCategory(s.owner.model.searchTarget()), nil
	}
	res, err := s.field.HandleControl(ctx, op)
	if err != nil {
		return res, err
	}
	s.owner.model.SetSearchActive(true)
	s.owner.model.SetSearchQuery(s.field.Value())
	s.owner.categories.syncItems()
	s.owner.refreshForm()
	res.Action = nil
	res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	return res, nil
}

func (s *settingsCategoryList) ControlActions() []control.Descriptor {
	return settingsControlActions("activate", "select", "scroll", "set_offset")
}
func (s *settingsCategoryList) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	if s.owner == nil || s.owner.model == nil || s.owner.model.Saving() {
		return ui.EventResult{}, fmt.Errorf("settings owner unavailable")
	}
	res, err := s.list.HandleControl(ctx, op)
	if err != nil {
		return res, err
	}
	if op.Verb == "activate" || op.Verb == "select" {
		if selected, ok := s.list.Selected(); ok {
			if index, ok := selected.Payload.(int); ok {
				return s.owner.openCategory(index), nil
			}
		}
	}
	res.Action = nil
	return res, nil
}

func (f *agentCatalogFilter) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	if f.owner == nil || f.owner.model == nil || f.owner.model.Saving() {
		return ui.EventResult{}, fmt.Errorf("agents owner unavailable")
	}
	res, err := f.FilterField.HandleControl(ctx, op)
	if err == nil {
		f.owner.refreshRows()
		res.Action = nil
		res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return res, err
}

func (f *lspCatalogFilter) HandleControl(ctx ui.EventContext, op ui.ControlOperation) (ui.EventResult, error) {
	if f.owner == nil || f.owner.owner.model == nil || f.owner.owner.model.Saving() {
		return ui.EventResult{}, fmt.Errorf("language server owner unavailable")
	}
	res, err := f.FilterField.HandleControl(ctx, op)
	if err == nil {
		f.owner.refreshRows()
		res.Action = nil
		res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return res, err
}
