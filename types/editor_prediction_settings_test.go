package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPredictionSettingsDefaultsAndFailClosed(t *testing.T) {
	want := WorkspacePredictionSettings{Mode: "off", HourlyTokenLimit: 0, Protocol: "chat", EndpointPath: ""}
	if got := DefaultWorkspacePredictionSettings(); got != want {
		t.Fatalf("default = %+v, want %+v", got, want)
	}
	for _, s := range []WorkspacePredictionSettings{
		{},
		{Mode: "light", Protocol: "chat"},
		{Mode: "full", HourlyTokenLimit: -1, Protocol: "chat"},
		{Mode: "off", HourlyTokenLimit: 1099511627777, Protocol: "chat"},
		{Mode: "LIGHT", HourlyTokenLimit: 20, Protocol: "chat"},
		{Mode: "off", Protocol: "auto"},
		{Mode: "off", Protocol: "chat", EndpointPath: "completions"},
		{Mode: "off", Protocol: "deepseek-fim"},
		{Mode: "light", HourlyTokenLimit: 20, Protocol: "codestral-fim", EndpointPath: "../fim"},
	} {
		if err := ValidatePredictionSettings(s); err == nil {
			t.Errorf("accepted invalid settings %+v", s)
		}
		if got := NormalizeWorkspacePrediction(s); got != want {
			t.Errorf("invalid %+v normalized to %+v, want default Off", s, got)
		}
	}
	for _, s := range []WorkspacePredictionSettings{
		want,
		{Mode: "light", HourlyTokenLimit: 1, Protocol: "chat"},
		{Mode: "full", HourlyTokenLimit: 1099511627776, Protocol: "deepseek-fim", EndpointPath: "beta/completions"},
		{Mode: "off", HourlyTokenLimit: 321, Protocol: "codestral-fim", EndpointPath: "/fim/completions"},
	} {
		if err := ValidatePredictionSettings(s); err != nil {
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
		want WorkspacePredictionSettings
	}{
		{`{}`, WorkspacePredictionSettings{"off", 0, "chat", ""}},
		{`{"mode":"off"}`, WorkspacePredictionSettings{"off", 0, "chat", ""}},
		{`{"hourly_token_limit":23}`, WorkspacePredictionSettings{"off", 23, "chat", ""}},
		{`{"protocol":"chat","endpoint_path":""}`, WorkspacePredictionSettings{"off", 0, "chat", ""}},
		{`{"mode":"light","hourly_token_limit":1,"protocol":"chat","endpoint_path":""}`,
			WorkspacePredictionSettings{"light", 1, "chat", ""}},
		{`{"mode":"full","hourly_token_limit":1099511627776}`,
			WorkspacePredictionSettings{"full", 1099511627776, "chat", ""}},
		{`{"mode":"light","hourly_token_limit":19,"protocol":"openai-completions","endpoint_path":"completions"}`,
			WorkspacePredictionSettings{"light", 19, "openai-completions", "completions"}},
		{`{"mode":"full","hourly_token_limit":20,"protocol":"deepseek-fim","endpoint_path":"/beta/completions"}`,
			WorkspacePredictionSettings{"full", 20, "deepseek-fim", "/beta/completions"}},
		{`{"mode":"off","hourly_token_limit":21,"protocol":"codestral-fim","endpoint_path":"fim/completions"}`,
			WorkspacePredictionSettings{"off", 21, "codestral-fim", "fim/completions"}},
		{`{"protocol":"codestral-fim","endpoint_path":"\ud83d\ude80/fim"}`,
			WorkspacePredictionSettings{"off", 0, "codestral-fim", "🚀/fim"}},
		{`{"\u006dode":"off"}`, WorkspacePredictionSettings{"off", 0, "chat", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := DecodeWorkspacePredictionSettings(json.RawMessage(tt.raw))
			if err != nil || got != tt.want {
				t.Fatalf("decode = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	// Count whitespace in the stored byte bound.
	if _, err := DecodeWorkspacePredictionSettings(json.RawMessage(`{}` + strings.Repeat(" ", 4094))); err != nil {
		t.Fatalf("4096-byte object rejected: %v", err)
	}
}

func TestPredictionSettingsRejectMalformedObjects(t *testing.T) {
	tests := []string{
		"", " ", "null", "[]", `"off"`, "true", "1", "{}{}", "{",
		`{"mode":"off",}`, `{"mode":"off"} trailing`,
		`{"mode":"light"}`, `{"mode":"full","hourly_token_limit":0}`,
		`{"hourly_token_limit":-1}`, `{"hourly_token_limit":1099511627777}`,
		`{"hourly_token_limit":9223372036854775808}`,
		`{"hourly_token_limit":1.5}`, `{"hourly_token_limit":1.0}`, `{"hourly_token_limit":1e3}`,
		`{"hourly_token_limit":"10"}`, `{"hourly_token_limit":true}`,
		`{"mode":null}`, `{"protocol":null}`, `{"endpoint_path":null}`, `{"hourly_token_limit":null}`,
		`{"mode":{}}`, `{"protocol":[]}`, `{"endpoint_path":10}`,
		`{"mode":"Light"}`, `{"mode":""}`, `{"protocol":"openai"}`, `{"protocol":"CHAT"}`,
		`{"unknown":"secret-do-not-echo"}`, `{"Mode":"off"}`, `{"grant_id":"secret-do-not-echo"}`,
		`{"mode":"off","mode":"off"}`, `{"mode":"off","\u006dode":"off"}`,
		`{"mode":"off","mode":"light","hourly_token_limit":99}`,
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
		if _, err := DecodeWorkspacePredictionSettings(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted malformed object %q", raw)
		} else if strings.Contains(err.Error(), "secret-do-not-echo") {
			t.Errorf("error echoed source: %v", err)
		}
	}
}

func TestPredictionSettingsEndpointSyntax(t *testing.T) {
	for _, path := range []string{
		"", "/", "//completions", "///completions", "https://secret-do-not-echo/fim",
		"//secret-do-not-echo/fim", "completions?key=secret-do-not-echo", "fim#fragment",
		"api@host/fim", "api:port/fim", `api\fim`, "%2fcompletions", "api/%2E%2E/fim",
		"api/%41/fim", ".", "..", "./fim", "../fim", "api/./fim", "api/../fim",
		"api//fim", "api/fim/", " completions", "completions ", "api/f im",
		"api/\tfim", "api/\nfim", "api/\x00fim", "api/\x7ffim", "api/\u0085fim",
		"api/\u061cfim", "api/\u200efim", "api/\u200ffim", "api/\u202afim",
		"api/\u202efim", "api/\u2066fim", "api/\u2069fim",
		string([]byte{0xff}), strings.Repeat("x", 4097),
	} {
		t.Run(path, func(t *testing.T) {
			s := WorkspacePredictionSettings{Mode: "off", Protocol: "deepseek-fim", EndpointPath: path}
			if err := ValidatePredictionSettings(s); err == nil {
				t.Error("accepted unsafe or absent FIM path")
			} else if strings.Contains(err.Error(), "secret-do-not-echo") {
				t.Errorf("error echoed endpoint: %v", err)
			}
			raw, err := json.Marshal(path)
			if err != nil {
				t.Fatal(err)
			}
			// Marshal repairs invalid UTF-8; test those original bytes directly.
			if path != "" && utf8.ValidString(path) {
				edit := SettingEdit{Path: []string{"prediction", "endpoint_path"}, After: raw}
				if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err == nil {
					t.Error("accepted unsafe endpoint leaf")
				}
			}
		})
	}
	for _, path := range []string{"completions", "/completions", "v1/fim/completions", "路徑/fim", strings.Repeat("x", 4096)} {
		if err := ValidatePredictionSettings(WorkspacePredictionSettings{Mode: "off", Protocol: "openai-completions", EndpointPath: path}); err != nil {
			t.Errorf("safe mount-relative path rejected: %v", err)
		}
	}
	// A literal backslash is unsafe, not a surrogate escape.
	if _, err := DecodeWorkspacePredictionSettings(json.RawMessage(`{"protocol":"codestral-fim","endpoint_path":"\\ud800"}`)); err == nil {
		t.Error("accepted endpoint containing a literal backslash")
	}
}

func TestPredictionSettingEditsWorkspaceOnlyAndMetadata(t *testing.T) {
	for _, scope := range []SettingsScope{SettingsScopeApplication, "session", "", "WORKSPACE"} {
		for _, edit := range []SettingEdit{
			{Path: []string{"prediction"}},
			{Path: []string{"prediction"}, After: json.RawMessage(`{}`)},
			{Path: []string{"prediction"}, After: json.RawMessage(`{"mode":"light","hourly_token_limit":10}`)},
			{Path: []string{"prediction", "mode"}},
			{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)},
			{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`10`)},
		} {
			if err := ValidatePredictionSettingEdits(scope, []SettingEdit{edit}); err == nil {
				t.Errorf("non-workspace scope %q accepted %+v", scope, edit)
			}
		}
	}
	for _, key := range []string{"prediction_root_identity", "prediction_connection_identity", "prediction_grant_id"} {
		for _, path := range [][]string{{key}, {key, "child"}, {"prediction", key}, {"other", key, "child"}} {
			for _, value := range []json.RawMessage{nil, json.RawMessage(`"forged-secret"`)} {
				for _, scope := range []SettingsScope{SettingsScopeWorkspace, SettingsScopeApplication, "session"} {
					if err := ValidatePredictionSettingEdits(scope, []SettingEdit{{Path: path, After: value}}); err == nil {
						t.Errorf("accepted owner metadata: %q at %q", path, scope)
					}
				}
			}
		}
	}
	for _, path := range [][]string{{"prediction", "unknown"}, {"prediction", "mode", "child"}, {"prediction", ""}} {
		if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, []SettingEdit{{Path: path}}); err == nil {
			t.Errorf("accepted removal of invalid path %q", path)
		}
	}
	if err := ValidatePredictionSettingEdits(SettingsScopeApplication, []SettingEdit{{Path: []string{"theme"}, After: json.RawMessage(`"dark"`)}}); err != nil {
		t.Fatalf("unrelated settings must remain with their own validator: %v", err)
	}
}

func TestPredictionSettingEditsScalarVersusMergedValidation(t *testing.T) {
	valid := []SettingEdit{
		{Path: []string{"prediction"}},
		{Path: []string{"prediction"}, After: json.RawMessage(`{}`)},
		{Path: []string{"prediction"}, After: json.RawMessage(`{"mode":"light","hourly_token_limit":1}`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"light"`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"full"`)},
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`0`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`1099511627776`)},
		{Path: []string{"prediction", "protocol"}, After: json.RawMessage(`"chat"`)},
		{Path: []string{"prediction", "protocol"}, After: json.RawMessage(`"openai-completions"`)},
		{Path: []string{"prediction", "protocol"}, After: json.RawMessage(`"deepseek-fim"`)},
		{Path: []string{"prediction", "protocol"}, After: json.RawMessage(`"codestral-fim"`)},
		{Path: []string{"prediction", "endpoint_path"}, After: json.RawMessage(`"fim/completions"`)},
		{Path: []string{"prediction", "endpoint_path"}, After: json.RawMessage(`""`)},
	}
	for _, leaf := range []string{"mode", "hourly_token_limit", "protocol", "endpoint_path"} {
		valid = append(valid, SettingEdit{Path: []string{"prediction", leaf}})
	}
	for _, edit := range valid {
		if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, []SettingEdit{edit}); err != nil {
			t.Errorf("valid edit %q=%s: %v", edit.Path, edit.After, err)
		}
	}
	for _, raw := range []string{`{"mode":"light"}`, `{"mode":"off","mode":"off"}`, `{"mode":"off","\u006dode":"off"}`, `null`, `"off"`, `{"unknown":1}`} {
		if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, []SettingEdit{{Path: []string{"prediction"}, After: json.RawMessage(raw)}}); err == nil {
			t.Errorf("invalid whole object accepted: %s", raw)
		}
	}
	for leaf, values := range map[string][]string{
		"mode":               {`"LIGHT"`, `""`, `null`, `0`, `{}`, `"off" "light"`, `"\ud800"`},
		"protocol":           {`"auto"`, `null`, `false`, `[]`, `{"protocol":"chat","protocol":"chat"}`},
		"hourly_token_limit": {`-1`, `1099511627777`, `9223372036854775808`, `0.5`, `1e2`, `"1"`, `null`, `false`},
		"endpoint_path":      {`null`, `1`, `{}`, `"https://secret-do-not-echo/fim"`, `"\udc00"`, `"api/\u202efim"`},
	} {
		for _, raw := range values {
			err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, []SettingEdit{{Path: []string{"prediction", leaf}, After: json.RawMessage(raw)}})
			if err == nil {
				t.Errorf("invalid scalar accepted: %s=%s", leaf, raw)
			} else if strings.Contains(err.Error(), "secret-do-not-echo") {
				t.Errorf("error echoed source: %v", err)
			}
		}
	}
	// The owner checks cross-field invariants after merging the batch.
	edits := []SettingEdit{
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"light"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`25`)},
	}
	if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, edits); err != nil {
		t.Fatal(err)
	}
	patched, err := PatchStoredSettings(json.RawMessage(`{"prediction":{"protocol":"chat"}}`), edits)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(patched, &object); err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeWorkspacePredictionSettings(object["prediction"]); err != nil || got != (WorkspacePredictionSettings{"light", 25, "chat", ""}) {
		t.Fatalf("merged leaves = %+v, %v", got, err)
	}
	if _, err := DecodeWorkspacePredictionSettings(json.RawMessage(`{"mode":"light","hourly_token_limit":0}`)); err == nil {
		t.Fatal("final state incorrectly accepted zero cap")
	}
}

func TestPredictionSettingEditsNoMutationAndRemovalIsNotExplicitOff(t *testing.T) {
	edits := []SettingEdit{
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"light"`)},
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage(`15`)},
		{Path: []string{"prediction_grant_id"}, After: json.RawMessage(`"forged"`)},
	}
	before, err := json.Marshal(edits)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, edits); err == nil {
		t.Fatal("batch containing a valid enable and later forged metadata accepted")
	}
	after, err := json.Marshal(edits)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("validation mutated the input batch")
	}
	stored := json.RawMessage(`{"prediction":{"mode":"light","hourly_token_limit":15},"theme":"dark"}`)
	remove := []SettingEdit{{Path: []string{"prediction", "mode"}}}
	off := []SettingEdit{{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)}}
	for _, batch := range [][]SettingEdit{remove, off} {
		if err := ValidatePredictionSettingEdits(SettingsScopeWorkspace, batch); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PatchStoredSettings(stored, remove)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := PatchStoredSettings(stored, off)
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
		if got, err := DecodeWorkspacePredictionSettings(prediction); err != nil || got != (WorkspacePredictionSettings{"off", 15, "chat", ""}) {
			t.Fatalf("reset/Off must resolve to workspace Off retaining cap: %+v, %v", got, err)
		}
	}
}
