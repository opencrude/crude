package sessionsettings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"crude/tui/ui"
	"crude/types/control"
)

// ControlChildren exposes the same manually painted picker, not a second model.
func (s *Screen) ControlChildren() []ui.Component {
	if !s.pickerPage() {
		return nil
	}
	return []ui.Component{s.modelFilter, s.modelList}
}

func authorityChanges(path string, before, after any) bool {
	if reflect.DeepEqual(before, after) {
		return false
	}
	a, aok := before.(map[string]any)
	b, bok := after.(map[string]any)
	if aok || bok {
		keys := map[string]bool{}
		for key := range a {
			keys[key] = true
		}
		for key := range b {
			keys[key] = true
		}
		for key := range keys {
			next := key
			if next == "Agents" {
				next = "agents"
			}
			if path != "" {
				next = path + "." + key
			}
			// A selected account owns these connection projections; they are not
			// credentials or independently editable transport authority here.
			if (strings.HasPrefix(path, "agents.Categories.") || strings.HasPrefix(path, "overrides.")) && (key == "Provider" || key == "ProviderURL" || key == "APIStandard") {
				next = path + ".account_id"
			}
			if authorityChanges(next, a[key], b[key]) {
				return true
			}
		}
		return false
	}
	return control.SettingsAuthority(path)
}

// ControlMetadata classifies canonical session leaves, including all edits Save
// carries. An ordinary picker must never smuggle a staged human-only edit.
func (s *Screen) ControlMetadata(id ui.ComponentID, action string) (control.Effect, bool, error) {
	human := false
	switch {
	case action == "session-settings.save":
		if s.snapshot == nil {
			return "", false, fmt.Errorf("session settings snapshot unavailable")
		}
		var before, after any
		old := cloneSettings(s.snapshot.effective)
		old.SessionOverrides = copyCategoryMap(s.snapshot.overrides)
		raw, err := json.Marshal(old)
		if err != nil {
			return "", false, err
		}
		if err = json.Unmarshal(raw, &before); err != nil {
			return "", false, err
		}
		raw, err = json.Marshal(s.ToSettings())
		if err != nil {
			return "", false, err
		}
		if err = json.Unmarshal(raw, &after); err != nil {
			return "", false, err
		}
		human = authorityChanges("", before, after)
		// Session-only values deliberately do not serialize in Settings JSON.
		human = human || s.snapshot.effective.Agents.Enabled != s.effective.Agents.Enabled
		keys := map[string]bool{}
		for key := range s.snapshot.overrides {
			keys[key] = true
		}
		for key := range s.overrides {
			keys[key] = true
		}
		for key := range keys {
			var oldOverride, newOverride any
			raw, err = json.Marshal(s.snapshot.overrides[key])
			if err != nil {
				return "", false, err
			}
			if err = json.Unmarshal(raw, &oldOverride); err != nil {
				return "", false, err
			}
			raw, err = json.Marshal(s.overrides[key])
			if err != nil {
				return "", false, err
			}
			if err = json.Unmarshal(raw, &newOverride); err != nil {
				return "", false, err
			}
			human = human || authorityChanges("overrides."+key, oldOverride, newOverride)
		}
	case action == "session-settings.save-workspace" || action == "session-settings.reset" || strings.Contains(action, "capabilities"):
		human = true
	case s.page == capabilityPage:
		human = true
	default:
		if s.formModel != nil {
			for _, field := range s.formModel.Fields() {
				if id == ui.NewID(FormID, field.ID) {
					switch field.ID {
					case fieldMultiAgent, "capabilities":
						human = true
					case fieldProvider, fieldModel, fieldThinking, fieldContextCompaction, fieldContextRetention:
						human = control.SettingsAuthority("agents.categories." + s.role + "." + map[string]string{fieldProvider: "account_id", fieldModel: "Model", fieldThinking: "ThinkingEffort", fieldContextCompaction: "context_compaction_percent", fieldContextRetention: "context_retention_tokens"}[field.ID])
					}
				}
			}
		}
	}
	if human {
		return control.Authority, true, nil
	}
	return control.Write, false, nil
}
