package setting

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPredictionSettingsDefaultsAndFailClosed(t *testing.T) {
	want := WorkspacePrediction{Mode: "off", HourlyTokenLimit: 0}
	if got := DefaultWorkspacePrediction(); got != want {
		t.Fatalf("default = %+v, want %+v", got, want)
	}
	for _, s := range []WorkspacePrediction{
		{},
		{Mode: "ask"},
		{Mode: "type", HourlyTokenLimit: -1},
		{Mode: "off", HourlyTokenLimit: 1099511627777},
		{Mode: "ASK", HourlyTokenLimit: 20},
		{Mode: "light", HourlyTokenLimit: 20},
	} {
		if err := ValidatePrediction(s); err == nil {
			t.Errorf("accepted invalid settings %+v", s)
		}
		if got := NormalizeWorkspacePrediction(s); got != want {
			t.Errorf("invalid %+v normalized to %+v, want default Off", s, got)
		}
	}
	for _, s := range []WorkspacePrediction{
		want,
		{Mode: "ask", HourlyTokenLimit: 1},
		{Mode: "type", HourlyTokenLimit: 1099511627776},
		{Mode: "off", HourlyTokenLimit: 321},
	} {
		if err := ValidatePrediction(s); err != nil {
			t.Errorf("valid %+v: %v", s, err)
		}
		if got := NormalizeWorkspacePrediction(s); got != s {
			t.Errorf("valid retained choice changed: %+v -> %+v", s, got)
		}
	}
}

func TestPredictionSettingsStrictSparseDecode(t *testing.T) {
	tests := []struct {
		raw  string
		want WorkspacePrediction
	}{
		{`{}`, WorkspacePrediction{"off", 0}},
		{`{"mode":"off"}`, WorkspacePrediction{"off", 0}},
		{`{"hourly_token_limit":23}`, WorkspacePrediction{"off", 23}},
		{`{"mode":"ask","hourly_token_limit":1}`, WorkspacePrediction{"ask", 1}},
		{`{"mode":"type","hourly_token_limit":1099511627776}`, WorkspacePrediction{"type", 1099511627776}},
		{`{"\u006dode":"off"}`, WorkspacePrediction{"off", 0}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := DecodeWorkspacePrediction(json.RawMessage(tt.raw))
			if err != nil || got != tt.want {
				t.Fatalf("decode = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	// Count whitespace in the stored byte bound.
	if _, err := DecodeWorkspacePrediction(json.RawMessage(`{}` + strings.Repeat(" ", 4094))); err != nil {
		t.Fatalf("4096-byte object rejected: %v", err)
	}
}

func TestPredictionSettingsRejectMalformedObjects(t *testing.T) {
	tests := []string{
		`{"protocol":"chat","endpoint_path":""}`, `{"mode":"light","hourly_token_limit":5}`,
		"", " ", "null", "[]", `"off"`, "true", "1", "{}{}", "{",
		`{"mode":"off",}`, `{"mode":"off"} trailing`,
		`{"mode":"ask"}`, `{"mode":"type","hourly_token_limit":0}`,
		`{"hourly_token_limit":-1}`, `{"hourly_token_limit":1099511627777}`,
		`{"hourly_token_limit":9223372036854775808}`,
		`{"hourly_token_limit":1.5}`, `{"hourly_token_limit":1.0}`, `{"hourly_token_limit":1e3}`,
		`{"hourly_token_limit":"10"}`, `{"hourly_token_limit":true}`,
		`{"mode":null}`, `{"protocol":null}`, `{"endpoint_path":null}`, `{"hourly_token_limit":null}`,
		`{"mode":{}}`, `{"protocol":[]}`, `{"endpoint_path":10}`,
		`{"mode":"Light"}`, `{"mode":""}`, `{"protocol":"openai"}`, `{"protocol":"CHAT"}`,
		`{"unknown":"secret-do-not-echo"}`, `{"Mode":"off"}`, `{"grant_id":"secret-do-not-echo"}`,
		`{"mode":"off","mode":"off"}`, `{"mode":"off","\u006dode":"off"}`,
		`{"mode":"off","mode":"ask","hourly_token_limit":99}`,
		`{"hourly_token_limit":1,"hourly_token_limit":2}`,
		`{"protocol":"chat","protocol":"chat"}`, `{"endpoint_path":"","endpoint_path":""}`,
		`{"protocol":"deepseek-fim"}`, `{"endpoint_path":"completions"}`,
		`{"mode":"\ud800"}`, `{"mode":"\udc00"}`, `{"\ud800":"off"}`,
		`{"protocol":"codestral-fim","endpoint_path":"\ud800/fim"}`,
		`{"protocol":"codestral-fim","endpoint_path":"\ud800\u0041"}`,
		`{"protocol":"codestral-fim","endpoint_path":"\ud800\ud800"}`,
		`{"mode":"\uXYZW"}`, `{"endpoint_path":"\x00"}`,
		`{"mode":"` + string([]byte{0xff}) + `"}`,
		`{"endpoint_path":"` + string([]byte{0xed, 0xa0, 0x80}) + `"}`,
		`{}` + strings.Repeat(" ", 4095),
	}
	for _, raw := range tests {
		if _, err := DecodeWorkspacePrediction(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted malformed object %q", raw)
		} else if strings.Contains(err.Error(), "secret-do-not-echo") {
			t.Errorf("error echoed source: %v", err)
		}
	}
}

func TestSuggestionEndpointSyntax(t *testing.T) {
	for _, path := range []string{
		"", "/", "//completions", "///completions", "https://secret-do-not-echo/fim",
		"//secret-do-not-echo/fim", "completions?key=secret-do-not-echo", "fim#fragment",
		"api@host/fim", "api:port/fim", `api\fim`, "%2fcompletions", "api/%2E%2E/fim",
		".", "..", "./fim", "../fim", "api/./fim", "api/../fim",
		"api//fim", "api/fim/", " completions", "completions ", "api/f im",
		"api/\tfim", "api/\nfim", "api/\x00fim", "api/\x7ffim", "api/\u0085fim",
		"api/\u061cfim", "api/\u200efim", "api/\u202efim", "api/\u2066fim",
		string([]byte{0xff}), strings.Repeat("x", 4097),
	} {
		if ValidSuggestionEndpointPath(path) {
			t.Errorf("accepted unsafe or absent path %q", path)
		}
		if got := (Suggestion{Format: SuggestionFormatDeepSeek, EndpointPath: path}).Normalized(); got.EndpointPath != "" {
			t.Errorf("normalized kept unsafe path %q", path)
		}
	}
	for _, path := range []string{"completions", "/completions", "v1/fim/completions", "路徑/fim", strings.Repeat("x", 4096)} {
		if !ValidSuggestionEndpointPath(path) {
			t.Errorf("safe mount-relative path rejected: %q", path)
		}
	}
	chat := Suggestion{Format: "unknown", EndpointPath: "/v1/completions"}.Normalized()
	if chat.Format != SuggestionFormatChat || chat.EndpointPath != "" || chat.TimeoutSeconds != 5 || chat.MaxOutputTokens != 256 {
		t.Fatalf("defaults = %+v", chat)
	}
	bounded := Suggestion{TimeoutSeconds: 999, MaxOutputTokens: 1}.Normalized()
	if bounded.TimeoutSeconds != 60 || bounded.MaxOutputTokens != 16 {
		t.Fatalf("bounds = %+v", bounded)
	}
}

func TestPredictionSettingEditsWorkspaceOnlyAndMetadata(t *testing.T) {
	for _, scope := range []Scope{ScopeApplication, "session", "", "WORKSPACE"} {
		for _, edit := range []Edit{
			{Path: []string{"prediction"}},
			{Path: []string{"prediction"}, After: json.RawMessage(`{}`)},
			{Path: []string{"prediction"}, After: json.RawMessage(`{"mode":"ask","hourly_token_limit":10}`)},
			{Path: []string{"prediction", "mode"}},
			{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)},
			{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`10`)},
		} {
			if err := ValidatePredictionEdits(scope, []Edit{edit}); err == nil {
				t.Errorf("non-workspace scope %q accepted %+v", scope, edit)
			}
		}
	}
	for _, key := range []string{"prediction_root_identity", "prediction_connection_identity", "prediction_grant_id"} {
		for _, path := range [][]string{{key}, {key, "child"}, {"prediction", key}, {"other", key, "child"}} {
			for _, value := range []json.RawMessage{nil, json.RawMessage(`"forged-secret"`)} {
				for _, scope := range []Scope{ScopeWorkspace, ScopeApplication, "session"} {
					if err := ValidatePredictionEdits(scope, []Edit{{Path: path, After: value}}); err == nil {
						t.Errorf("accepted owner metadata: %q at %q", path, scope)
					}
				}
			}
		}
	}
	for _, path := range [][]string{{"prediction", "unknown"}, {"prediction", "mode", "child"}, {"prediction", ""}} {
		if err := ValidatePredictionEdits(ScopeWorkspace, []Edit{{Path: path}}); err == nil {
			t.Errorf("accepted removal of invalid path %q", path)
		}
	}
	if err := ValidatePredictionEdits(ScopeApplication, []Edit{{Path: []string{"theme"}, After: json.RawMessage(`"dark"`)}}); err != nil {
		t.Fatalf("unrelated settings must remain with their own validator: %v", err)
	}
}

func TestPredictionSettingEditsScalarVersusMergedValidation(t *testing.T) {
	valid := []Edit{
		{Path: []string{"prediction"}},
		{Path: []string{"prediction"}, After: json.RawMessage(`{}`)},
		{Path: []string{"prediction"}, After: json.RawMessage(`{"mode":"ask","hourly_token_limit":1}`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"ask"`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"type"`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`0`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`1099511627776`)},
	}
	for _, leaf := range []string{"mode", "hourly_token_limit"} {
		valid = append(valid, Edit{Path: []string{"prediction", leaf}})
	}
	for _, edit := range valid {
		if err := ValidatePredictionEdits(ScopeWorkspace, []Edit{edit}); err != nil {
			t.Errorf("valid edit %q=%s: %v", edit.Path, edit.After, err)
		}
	}
	for _, raw := range []string{`{"mode":"ask"}`, `{"mode":"off","mode":"off"}`, `{"mode":"off","\u006dode":"off"}`, `null`, `"off"`, `{"unknown":1}`} {
		if err := ValidatePredictionEdits(ScopeWorkspace, []Edit{{Path: []string{"prediction"}, After: json.RawMessage(raw)}}); err == nil {
			t.Errorf("invalid whole object accepted: %s", raw)
		}
	}
	for leaf, values := range map[string][]string{
		"mode":               {`"LIGHT"`, `"light"`, `"full"`, `""`, `null`, `0`, `{}`, `"off" "ask"`, `"\ud800"`},
		"hourly_token_limit": {`-1`, `1099511627777`, `9223372036854775808`, `0.5`, `1e2`, `"1"`, `null`, `false`},
		"protocol":           {`"chat"`},
		"endpoint_path":      {`"/v1/completions"`},
	} {
		for _, raw := range values {
			err := ValidatePredictionEdits(ScopeWorkspace, []Edit{{Path: []string{"prediction", leaf}, After: json.RawMessage(raw)}})
			if err == nil {
				t.Errorf("invalid scalar accepted: %s=%s", leaf, raw)
			} else if strings.Contains(err.Error(), "secret-do-not-echo") {
				t.Errorf("error echoed source: %v", err)
			}
		}
	}
	// The owner checks cross-field invariants after merging the batch.
	edits := []Edit{
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"ask"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`25`)},
	}
	if err := ValidatePredictionEdits(ScopeWorkspace, edits); err != nil {
		t.Fatal(err)
	}
	patched, err := PatchStored(json.RawMessage(`{"prediction":{"mode":"off"}}`), edits)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(patched, &object); err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeWorkspacePrediction(object["prediction"]); err != nil || got != (WorkspacePrediction{"ask", 25}) {
		t.Fatalf("merged leaves = %+v, %v", got, err)
	}
	if _, err := DecodeWorkspacePrediction(json.RawMessage(`{"mode":"ask","hourly_token_limit":0}`)); err == nil {
		t.Fatal("final state incorrectly accepted zero cap")
	}
}

func TestPredictionSettingEditsNoMutationAndRemovalIsNotExplicitOff(t *testing.T) {
	edits := []Edit{
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"ask"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`15`)},
		{Path: []string{"prediction_grant_id"}, After: json.RawMessage(`"forged"`)},
	}
	before, err := json.Marshal(edits)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePredictionEdits(ScopeWorkspace, edits); err == nil {
		t.Fatal("batch containing a valid enable and later forged metadata accepted")
	}
	after, err := json.Marshal(edits)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("validation mutated the input batch")
	}
	stored := json.RawMessage(`{"prediction":{"mode":"ask","hourly_token_limit":15},"theme":"dark"}`)
	remove := []Edit{{Path: []string{"prediction", "mode"}}}
	off := []Edit{{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)}}
	for _, batch := range [][]Edit{remove, off} {
		if err := ValidatePredictionEdits(ScopeWorkspace, batch); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PatchStored(stored, remove)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := PatchStored(stored, off)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		raw  json.RawMessage
		want map[string]any
	}{
		{removed, map[string]any{"prediction": map[string]any{"hourly_token_limit": float64(15)}, "theme": "dark"}},
		{explicit, map[string]any{"prediction": map[string]any{"mode": "off", "hourly_token_limit": float64(15)}, "theme": "dark"}},
	} {
		var got map[string]any
		if err := json.Unmarshal(tt.raw, &got); err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("stored choices = %v, %v; want %v", got, err, tt.want)
		}
		prediction, err := json.Marshal(got["prediction"])
		if err != nil {
			t.Fatal(err)
		}
		if got, err := DecodeWorkspacePrediction(prediction); err != nil || got != (WorkspacePrediction{"off", 15}) {
			t.Fatalf("reset/Off must resolve to workspace Off retaining cap: %+v, %v", got, err)
		}
	}
}
