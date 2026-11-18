package settings

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

const (
	fieldPredictionMode  = "prediction.mode"
	fieldPredictionLimit = "prediction.hourly_token_limit"
)

// predictionFields are the workspace's own suggestion choice: the mode and
// the hourly cap. They are not an application default or part of analysis
// Trust; the connection is on Agents, Aeron, Suggestions. They only edit a
// draft; turning suggestions on records this workspace's consent on save.
func (m *Model) predictionFields(cfg types.WorkspacePredictionSettings) []*ui.FieldState {
	if m.Scope() != types.SettingsScopeWorkspace {
		var fields []*ui.FieldState
		for _, row := range []struct{ id, label string }{
			{fieldPredictionMode, "Aeron suggestions"},
			{fieldPredictionLimit, "Hourly limit"},
		} {
			field := ui.TextFieldState(row.id, row.label, "Workspace only",
				"Each workspace turns suggestions on for itself.")
			field.ReadOnly = true
			fields = append(fields, field)
		}
		return fields
	}
	limit := cfg.HourlyTokenLimit
	if limit == 0 && cfg.Mode == types.PredictionOff {
		limit = types.DefaultPredictionHourlyTokens
	}
	fields := []*ui.FieldState{
		ui.SegmentedFieldState(fieldPredictionMode, "Aeron suggestions", []ui.FormOption{
			{ID: string(types.PredictionOff), Label: "Off"},
			{ID: string(types.PredictionAsk), Label: "When I ask"},
			{ID: string(types.PredictionType), Label: "As I type"},
		}, string(cfg.Mode), "When I ask: press Alt+\\ in the editor. As I type: Aeron also suggests after a short pause. Aeron sends nearby code, recent edits and related code to its account."),
		ui.NumberFieldState(fieldPredictionLimit, "Hourly limit", 0, "tokens",
			"Suggestions stop for the rest of the hour at this limit. Usage is counted in tokens, not money."),
	}
	// NumberFieldState takes int, but the setting is int64. Set the exact
	// decimal value without narrowing (including on 32-bit platforms).
	fields[1].Value = ui.StringValue(strconv.FormatInt(limit, 10))
	fields[1].Initial = ui.StringValue(strconv.FormatInt(cfg.HourlyTokenLimit, 10))
	if cfg.HourlyTokenLimit == 0 && cfg.Mode == types.PredictionOff {
		fields[1].Initial = fields[1].Value
	}
	for _, field := range fields {
		id := field.ID
		field.Validator = func(value ui.FieldValue) []string {
			values := make(map[string]string, len(fields))
			for _, sibling := range fields {
				values[sibling.ID] = sibling.Value.String()
			}
			values[id] = value.String()
			raw, _ := json.Marshal(value.String())
			if id == fieldPredictionLimit {
				raw = json.RawMessage(value.String())
			}
			if err := types.ValidatePredictionSettingEdits(types.SettingsScopeWorkspace, []types.SettingEdit{
				{Path: strings.Split(id, "."), After: raw},
			}); err != nil {
				return []string{sentence(err.Error())}
			}
			limit, err := predictionTokenLimit(values[fieldPredictionLimit])
			if err != nil {
				return []string{sentence(err.Error())}
			}
			if err := types.ValidatePredictionSettings(types.WorkspacePredictionSettings{
				Mode: types.EditorPredictionMode(values[fieldPredictionMode]), HourlyTokenLimit: limit,
			}); err != nil {
				return []string{sentence(err.Error())}
			}
			return nil
		}
	}
	return fields
}

func sentence(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

func predictionTokenLimit(value string) (int64, error) {
	var limit int64
	if json.Unmarshal([]byte(value), &limit) != nil || strings.TrimSpace(value) == "null" ||
		limit < 0 || limit > 1<<40 {
		return 0, errors.New("enter a whole number of tokens; suggestions need a limit above 0")
	}
	return limit, nil
}

func (m *Model) collectPrediction() types.WorkspacePredictionSettings {
	if m.Scope() != types.SettingsScopeWorkspace {
		return types.DefaultWorkspacePredictionSettings()
	}
	limit, err := predictionTokenLimit(m.value(catLSP, fieldPredictionLimit))
	if err != nil {
		// Collection cannot return an error. Keep an invalid sentinel rather
		// than silently repairing the draft; field validation blocks Save.
		limit = -1
	}
	mode := types.EditorPredictionMode(m.value(catLSP, fieldPredictionMode))
	if mode == types.PredictionOff && limit == types.DefaultPredictionHourlyTokens && m.layerBase.Prediction.HourlyTokenLimit == 0 {
		limit = 0 // the offered default is not a stored choice while Off
	}
	return types.WorkspacePredictionSettings{Mode: mode, HourlyTokenLimit: limit}
}

// syncPredictionScope replaces both rows and their baselines together.
// Raw stored choices must never resurrect a root/connection-blocked grant: only
// the core's effective workspace snapshot can supply this preview.
func (m *Model) syncPredictionScope() {
	if m.fieldAt(catLSP, fieldPredictionMode) == nil {
		return
	}
	settings := types.DefaultWorkspacePredictionSettings()
	if m.Scope() == types.SettingsScopeWorkspace {
		settings = types.NormalizeWorkspacePrediction(m.layerBase.Prediction)
	}
	fields := m.predictionFields(settings)
	for i, fresh := range fields {
		if field := m.fieldAt(catLSP, fresh.ID); field != nil {
			*field = *fresh
			// Validators close over this slice: retain mounted sibling identities
			// rather than a detached copy after the scope transition.
			fields[i] = field
		}
	}
	m.collectedBase.Prediction = settings
	var baseline types.AppSettings
	if json.Unmarshal(m.preferencesBase, &baseline) == nil {
		baseline.Prediction = settings
		m.preferencesBase, _ = json.Marshal(baseline)
	}
}
