package providersim

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCredentialSpecificModelInventory(t *testing.T) {
	for _, tc := range []struct {
		wire                     Wire
		path, header, array, key string
	}{
		{Chat, "/models", "Authorization", "data", "id"},
		{Responses, "/models", "Authorization", "data", "id"},
		{Codex, "/models?client_version=0.121.0", "Authorization", "models", "slug"},
		{Messages, "/models", "x-api-key", "data", "id"},
		{Gemini, "/models", "x-goog-api-key", "models", "baseModelId"},
		{Ollama, "/tags", "Authorization", "models", "name"},
	} {
		t.Run(string(tc.wire), func(t *testing.T) {
			s := New(t, WithCredentialModels(tc.wire, "key-one", Model{ID: "one"}),
				WithCredentialModels(tc.wire, "key-two", Model{ID: "two"}))
			for _, id := range []string{"one", "two", "unknown", "missing"} {
				t.Run(id, func(t *testing.T) {
					t.Parallel() // Same URL and two accounts can be in flight together.
					r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.BaseURL(tc.wire)+tc.path, nil)
					if err != nil {
						t.Fatal(err)
					}
					if id != "missing" {
						value := "key-" + id
						if tc.header == "Authorization" {
							value = "Bearer " + value
						}
						r.Header.Set(tc.header, value)
					}
					res := do(t, r)
					if id == "unknown" || id == "missing" {
						status := http.StatusUnauthorized
						if tc.wire == Gemini {
							status = http.StatusForbidden
						}
						checkedTurn(t, tc.wire, res, false, status)
						return
					}
					rows := catalogRows(t, tc.wire, tc.array, res)
					if len(rows) != 1 || rows[0].(map[string]any)[tc.key] != id {
						t.Fatalf("account %s saw %v", id, rows)
					}
				})
			}
		})
	}
}

func catalogRows(t *testing.T, wire Wire, array string, res *http.Response) []any {
	t.Helper()
	var body map[string]any
	switch wire {
	case Codex:
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" {
			t.Fatal("invalid Codex catalog:", res.Status)
		}
		v, err := decodeJSON(readResponse(t, res))
		if err != nil {
			t.Fatal(err)
		}
		body = v.(map[string]any)
		// No Codex ModelInfo bundle root. Check the required shape of
		// openai/codex@ebd0765 codex-rs/protocol/src/openai_models.rs.
		for _, item := range body[array].([]any) {
			row := item.(map[string]any)
			if row["shell_type"] != "disabled" || row["visibility"] != "list" || row["supported_in_api"] != true ||
				row["support_verbosity"] != false || row["priority"] == nil || row["supported_reasoning_levels"] == nil ||
				row["experimental_supported_tools"] == nil || row["truncation_policy"].(map[string]any)["mode"] != "tokens" {
				t.Fatal("invalid required model fields:", row)
			}
		}
	case Messages:
		body = messageJSON(t, res, 200, "") // Page envelope is documented, not a bundle root.
		for _, row := range body[array].([]any) {
			validateMessageOutput(t, "ModelInfo", row)
		}
	case Ollama:
		body = ollamaJSON(t, res, 200, "ListResponse")
	default:
		body = googleJSON(t, wire, "ListModelsResponse", res, 200)
	}
	return body[array].([]any)
}

func TestCatalogModelMetadata(t *testing.T) {
	for _, tc := range []struct {
		wire                    Wire
		array, id, name, window string
	}{
		{Codex, "models", "slug", "display_name", "context_window"},
		{Messages, "data", "id", "display_name", "max_input_tokens"},
		{Gemini, "models", "baseModelId", "displayName", "inputTokenLimit"},
	} {
		models := []Model{{ID: "first", DisplayName: "First model", ContextWindow: 1000000}, {ID: "second", DisplayName: "Second model", ContextWindow: 272000}}
		s := New(t, WithModelCatalog(tc.wire, models...))
		models[0].ID = "changed"
		path := "/models"
		if tc.wire == Codex {
			path += "?client_version=0.121.0"
		}
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.BaseURL(tc.wire)+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer demo")
		r.Header.Set("chatgpt-account-id", "demo-id")
		r.Header.Set("originator", "demo-agent")
		rows := catalogRows(t, tc.wire, tc.array, do(t, r))
		first, next := rows[0].(map[string]any), rows[1].(map[string]any)
		if len(rows) != 2 || first[tc.id] != "first" || next[tc.id] != "second" ||
			first[tc.name] != "First model" || next[tc.name] != "Second model" ||
			first[tc.window] != json.Number("1000000") || next[tc.window] != json.Number("272000") {
			t.Fatal(tc.wire, rows)
		}
		got, ok := s.Last()
		if !ok || got.Method != http.MethodGet || got.Target != r.URL.RequestURI() || got.Header.Get("Authorization") != "Bearer demo" ||
			got.Header.Get("originator") != "demo-agent" || got.Header.Get("chatgpt-account-id") != "demo-id" ||
			tc.wire == Codex && got.Path != "/backend-api/codex/models" {
			t.Fatalf("inventory request lost metadata: %+v", got)
		}
	}
}

func TestGeminiCatalogQueryCredential(t *testing.T) {
	s := New(t, WithCredentialModels(Gemini, "test", Model{ID: "example"}))
	res := get(t, s.BaseURL(Gemini)+"/models/example?key=test")
	if obj := googleJSON(t, Gemini, "Model", res, 200); obj["baseModelId"] != "example" {
		t.Fatal(obj)
	}
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.BaseURL(Gemini)+"/models?key=test", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("x-goog-api-key", "other")
	googleJSON(t, Gemini, "ErrorResponse", do(t, r), 403)
}

func TestUnsupportedCatalogMetadata(t *testing.T) {
	for _, w := range []Wire{Chat, Responses, Ollama} {
		for _, m := range []Model{{ID: "test", DisplayName: "Test"}, {ID: "test", ContextWindow: 100}} {
			s := &Server{}
			if err := s.setCatalog(w, "", false, []Model{m}); err == nil || !strings.Contains(err.Error(), "supply only the ID") {
				t.Fatalf("%s accepted unsupported metadata: %v", w, err)
			}
		}
	}
	s := &Server{}
	if err := s.setCatalog(Interactions, "key", true, nil); err == nil || !strings.Contains(err.Error(), "no model inventory") {
		t.Fatalf("accepted a catalog for an unsupported operation: %v", err)
	}
}
