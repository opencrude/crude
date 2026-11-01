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
	fieldPredictionMode     = "prediction.mode"
	fieldPredictionLimit    = "prediction.hourly_token_limit"
	fieldPredictionProtocol = "prediction.protocol"
	fieldPredictionEndpoint = "prediction.endpoint_path"
)

// predictionFields is a separate workspace-only egress choice, not an
// application default or an extension of analysis Trust. It only edits a draft.
func (m *Model) predictionFields(cfg types.WorkspacePredictionSettings) []*ui.FieldState {
	if m.Scope() != types.SettingsScopeWorkspace {
		var fields []*ui.FieldState
		for _, row := range []struct{ id, label string }{
			{fieldPredictionMode, "AI predictions"},
			{fieldPredictionLimit, "Hourly cap"},
			{fieldPredictionProtocol, "Prediction protocol"},
			{fieldPredictionEndpoint, "Prediction endpoint"},
		} {
			field := ui.TextFieldState(row.id, row.label, "Workspace only",
				"Workspace consent is separate from analysis Trust; cannot be granted here.")
			field.ReadOnly = true
			fields = append(fields, field)
		}
		return fields
	}

	// Do not normalize the draft: incomplete enables and invalid endpoint
	// combinations must remain visible until corrected, not become Off silently.
	fields := []*ui.FieldState{
		ui.SegmentedFieldState(fieldPredictionMode, "AI predictions", []ui.FormOption{
			{ID: string(types.PredictionOff), Label: "Off"},
			{ID: string(types.PredictionLight), Label: "Light"},
			{ID: string(types.PredictionFull), Label: "Full"},
		}, string(cfg.Mode), "Light: on demand; Full: also while typing. Consent is separate from analysis Trust."),
		ui.NumberFieldState(fieldPredictionLimit, "Hourly cap", 0, "tokens",
			"Choose a positive cap to enable; usage is estimated, not a price."),
		ui.SelectFieldState(fieldPredictionProtocol, "Prediction protocol", string(cfg.Protocol), []ui.FormOption{
			{ID: string(types.PredictionChat), Label: "Chat"},
			{ID: string(types.PredictionOpenAI), Label: "OpenAI completions"},
			{ID: string(types.PredictionDeepSeek), Label: "DeepSeek FIM"},
			{ID: string(types.PredictionCodestral), Label: "Codestral FIM"},
		}, "Source goes to the selected Aeron account; sensitive and external files are excluded."),
		ui.TextFieldState(fieldPredictionEndpoint, "Prediction endpoint", cfg.EndpointPath,
			"Chat: empty. FIM: explicit mount-relative path; never guessed from a hostname."),
	}
	// NumberFieldState takes int, but the authority contract uses int64. Set the
	// exact decimal value without narrowing (including on 32-bit platforms).
	fields[1].Value = ui.StringValue(strconv.FormatInt(cfg.HourlyTokenLimit, 10))
	fields[1].Initial = fields[1].Value
	for _, field := range fields {
		id := field.ID
		field.Validator = func(value ui.FieldValue) []string {
			values := make(map[string]string, len(fields))
			for _, sibling := range fields {
				values[sibling.ID] = sibling.Value.String()
			}
			values[id] = value.String()
			// Check the individual leaf first, including safe-path syntax even
			// while the user is midway through changing the protocol.
			raw, _ := json.Marshal(value.String())
			if id == fieldPredictionLimit {
				raw = json.RawMessage(value.String())
			}
			if err := types.ValidatePredictionSettingEdits(types.SettingsScopeWorkspace, []types.SettingEdit{
				{Path: strings.Split(id, "."), After: raw},
			}); err != nil {
				return []string{err.Error()}
			}
			limit, err := predictionTokenLimit(values[fieldPredictionLimit])
			if err != nil {
				return []string{err.Error()}
			}
			if err := types.ValidatePredictionSettings(types.WorkspacePredictionSettings{
				Mode:             types.EditorPredictionMode(values[fieldPredictionMode]),
				HourlyTokenLimit: limit,
				Protocol:         types.EditorPredictionProtocol(values[fieldPredictionProtocol]),
				EndpointPath:     values[fieldPredictionEndpoint],
			}); err != nil {
				return []string{err.Error()}
			}
			return nil
		}
	}
	return fields
}

func predictionTokenLimit(value string) (int64, error) {
	var limit int64
	if json.Unmarshal([]byte(value), &limit) != nil || strings.TrimSpace(value) == "null" ||
		limit < 0 || limit > 1<<40 {
		return 0, errors.New("hourly cap must be a whole number from 0 to 1099511627776; enabled predictions require a positive cap")
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
	return types.WorkspacePredictionSettings{
		Mode:             types.EditorPredictionMode(m.value(catLSP, fieldPredictionMode)),
		HourlyTokenLimit: limit,
		Protocol:         types.EditorPredictionProtocol(m.value(catLSP, fieldPredictionProtocol)),
		EndpointPath:     m.value(catLSP, fieldPredictionEndpoint),
	}
}

// syncPredictionScope replaces all four rows and their baselines together.
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
