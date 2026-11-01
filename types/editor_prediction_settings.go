package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type EditorPredictionMode string

const (
	PredictionOff   EditorPredictionMode = "off"
	PredictionLight EditorPredictionMode = "light"
	PredictionFull  EditorPredictionMode = "full"
)

type EditorPredictionProtocol string

const (
	PredictionChat      EditorPredictionProtocol = "chat"
	PredictionOpenAI    EditorPredictionProtocol = "openai-completions"
	PredictionDeepSeek  EditorPredictionProtocol = "deepseek-fim"
	PredictionCodestral EditorPredictionProtocol = "codestral-fim"
)

// WorkspacePredictionSettings is a separate human-owned workspace authority.
// It never inherits from application/session settings or analysis Trust. These
// values alone do not bind a grant, authorize network access or start inference.
type WorkspacePredictionSettings struct {
	Mode             EditorPredictionMode     `json:"mode"`
	HourlyTokenLimit int64                    `json:"hourly_token_limit"`
	Protocol         EditorPredictionProtocol `json:"protocol"`
	EndpointPath     string                   `json:"endpoint_path"`
}

func DefaultWorkspacePredictionSettings() WorkspacePredictionSettings {
	return WorkspacePredictionSettings{Mode: PredictionOff, Protocol: PredictionChat}
}

// DecodeWorkspacePredictionSettings accepts a bounded sparse object, without
// JSON's duplicate-key/Unicode-repair behavior. Missing leaves use workspace
// defaults, not another settings layer. An incomplete enable is an error.
func DecodeWorkspacePredictionSettings(raw json.RawMessage) (WorkspacePredictionSettings, error) {
	s := DefaultWorkspacePredictionSettings()
	if !predictionJSON(raw) {
		return s, errors.New("invalid workspace prediction JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return s, errors.New("workspace prediction must be an object")
	}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return DefaultWorkspacePredictionSettings(), errors.New("duplicate workspace prediction field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return DefaultWorkspacePredictionSettings(), errors.New("invalid workspace prediction value")
		}
		if err := decodePredictionLeaf(&s, key, value); err != nil {
			return DefaultWorkspacePredictionSettings(), err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return DefaultWorkspacePredictionSettings(), errors.New("invalid workspace prediction object")
	}
	if err := ValidatePredictionSettings(s); err != nil {
		return DefaultWorkspacePredictionSettings(), err
	}
	return s, nil
}

// NormalizeWorkspacePrediction fails closed for the entire invalid combination;
// it does not repair an invalid enable, invent a budget or infer a wire dialect.
func NormalizeWorkspacePrediction(s WorkspacePredictionSettings) WorkspacePredictionSettings {
	if ValidatePredictionSettings(s) != nil {
		return DefaultWorkspacePredictionSettings()
	}
	return s
}

// ValidatePredictionSettings validates the final merged workspace choice.
// Off may retain a valid budget and protocol/path for later human editing.
func ValidatePredictionSettings(s WorkspacePredictionSettings) error {
	switch s.Mode {
	case PredictionOff, PredictionLight, PredictionFull:
	default:
		return errors.New("invalid workspace prediction mode")
	}
	if s.HourlyTokenLimit < 0 || s.HourlyTokenLimit > 1<<40 {
		return errors.New("workspace prediction hourly token limit is out of range")
	}
	if s.Mode != PredictionOff && s.HourlyTokenLimit == 0 {
		return errors.New("enabled workspace prediction requires a positive hourly token limit")
	}
	switch s.Protocol {
	case PredictionChat:
		if s.EndpointPath != "" {
			return errors.New("chat prediction requires an empty endpoint path")
		}
	case PredictionOpenAI, PredictionDeepSeek, PredictionCodestral:
		if s.EndpointPath == "" || !predictionEndpointPath(s.EndpointPath) {
			return errors.New("FIM prediction requires a safe explicit endpoint path")
		}
	default:
		return errors.New("invalid workspace prediction protocol")
	}
	return nil
}

// ValidatePredictionSettingEdits checks the entire batch without applying it.
// The persistence owner must also validate the final state after applying these
// edits to its latest overlay, atomically with any generated grant bindings.
// Leaf edits deliberately do not enforce cross-field constraints: a single
// transaction may select a mode, positive cap, protocol and endpoint together.
// A nil After resets to workspace defaults, never application inheritance.
func ValidatePredictionSettingEdits(scope SettingsScope, edits []SettingEdit) error {
	for _, edit := range edits {
		for _, segment := range edit.Path {
			switch segment {
			case "prediction_root_identity", "prediction_connection_identity", "prediction_grant_id":
				return errors.New("prediction metadata is generated by the workspace preference owner")
			}
		}
		if len(edit.Path) == 0 || edit.Path[0] != "prediction" {
			continue
		}
		if scope != SettingsScopeWorkspace {
			return errors.New("prediction belongs to Workspace settings")
		}
		if len(edit.Path) > 2 || len(edit.Path) == 2 && !predictionLeaf(edit.Path[1]) {
			return errors.New("unknown workspace prediction setting")
		}
		if edit.After == nil {
			continue
		}
		if len(edit.Path) == 1 {
			if _, err := DecodeWorkspacePredictionSettings(edit.After); err != nil {
				return err
			}
		} else {
			s := DefaultWorkspacePredictionSettings()
			if err := decodePredictionLeaf(&s, edit.Path[1], edit.After); err != nil {
				return err
			}
		}
	}
	return nil
}

func predictionLeaf(key string) bool {
	switch key {
	case "mode", "hourly_token_limit", "protocol", "endpoint_path":
		return true
	default:
		return false
	}
}

func decodePredictionLeaf(s *WorkspacePredictionSettings, key string, raw json.RawMessage) error {
	if !predictionJSON(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("invalid workspace prediction scalar")
	}
	switch key {
	case "mode":
		if json.Unmarshal(raw, &s.Mode) != nil ||
			(s.Mode != PredictionOff && s.Mode != PredictionLight && s.Mode != PredictionFull) {
			return errors.New("invalid workspace prediction mode")
		}
	case "hourly_token_limit":
		if json.Unmarshal(raw, &s.HourlyTokenLimit) != nil || s.HourlyTokenLimit < 0 || s.HourlyTokenLimit > 1<<40 {
			return errors.New("invalid workspace prediction hourly token limit")
		}
	case "protocol":
		if json.Unmarshal(raw, &s.Protocol) != nil ||
			(s.Protocol != PredictionChat && s.Protocol != PredictionOpenAI &&
				s.Protocol != PredictionDeepSeek && s.Protocol != PredictionCodestral) {
			return errors.New("invalid workspace prediction protocol")
		}
	case "endpoint_path":
		if json.Unmarshal(raw, &s.EndpointPath) != nil ||
			(s.EndpointPath != "" && !predictionEndpointPath(s.EndpointPath)) {
			return errors.New("invalid workspace prediction endpoint path")
		}
	default:
		return errors.New("unknown workspace prediction field")
	}
	return nil
}

// Match the provider FIM adapter's explicit mount-relative path restrictions
// without depending on provider or inferring any endpoint. Additionally refuse
// Unicode controls and bidi formatting in this human-facing authority choice.
func predictionEndpointPath(path string) bool {
	if len(path) > 4096 || !utf8.ValidString(path) || strings.TrimSpace(path) != path ||
		strings.ContainsAny(path, "\\%?#:@") || strings.HasPrefix(path, "//") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for _, r := range path {
		if r <= ' ' || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return false
		}
	}
	return true
}

// encoding/json accepts invalid UTF-8 and unpaired escaped UTF-16 surrogates by
// replacing them. Authority-bearing data must instead reject those inputs.
func predictionJSON(raw []byte) bool {
	if len(raw) > 4096 || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		code, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, _ := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
