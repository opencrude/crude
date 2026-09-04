package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsPaginatesAnthropic(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("x-api-key") != "key" ||
			r.Header.Get("anthropic-version") != anthropicAPIVersion {
			t.Errorf("headers = %#v", r.Header)
		}
		if r.URL.Query().Get("limit") != "1000" {
			t.Errorf("limit = %q", r.URL.Query().Get("limit"))
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("after_id") {
		case "":
			_, _ = fmt.Fprint(w, `{
				"data":[{"id":"claude-a","display_name":"Claude A"}],
				"has_more":true,"last_id":"claude-a"
			}`)
		case "claude-a":
			_, _ = fmt.Fprint(w, `{
				"data":[{"id":"claude-b","display_name":"Claude B"}],
				"has_more":false,"last_id":"claude-b"
			}`)
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	p := &Provider{
		ID: "anthropic", Name: "Anthropic", BaseURL: server.URL, APIKey: "key",
		StandardName: "anthropic",
	}
	client, err := p.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if len(models) != 2 || models[0].ID != "claude-a" || models[1].ID != "claude-b" {
		t.Fatalf("models = %+v", models)
	}
}

func TestListModelsMapsUnsupportedAndUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer unsupported":
			http.Error(w, `{}`, http.StatusNotFound)
		default:
			http.Error(w, `{}`, http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	unsupported := &Provider{ID: "x", BaseURL: server.URL, APIKey: "unsupported", StandardName: "openai"}
	client, err := unsupported.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.ListModels(t.Context()); !errors.Is(err, ErrModelsUnsupported) {
		t.Fatalf("404 error = %v, want ErrModelsUnsupported", err)
	}

	unauthorized := &Provider{ID: "x", BaseURL: server.URL, APIKey: "bad", StandardName: "openai"}
	client, err = unauthorized.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if _, err := client.ListModels(t.Context()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("401 error = %v, want ErrUnauthorized", err)
	}
}

// TestOpenAIStandardDecodesGatewayContextLength pins the gateway contract:
// Command Code and OpenRouter annotate their {"data":[...]} listings with a
// non-standard context_length (the enforced context window). The decoder must
// surface it so wire data beats the models.dev fallback, and leave the field
// zero when a plain OpenAI listing omits it.
func TestOpenAIStandardDecodesGatewayContextLength(t *testing.T) {
	std := &OpenAIStandard{}
	page, err := std.DecodeModelsPage([]byte(`{
		"object": "list",
		"data": [
			{
				"id": "deepseek/deepseek-v4-flash",
				"object": "model",
				"created": 1788430262,
				"owned_by": "command-code",
				"name": "DeepSeek V4 Flash (latest)",
				"context_length": 1000000
			},
			{"id": "gpt-4o", "object": "model"}
		]
	}`))
	if err != nil {
		t.Fatalf("DecodeModelsPage: %v", err)
	}
	if len(page.Models) != 2 {
		t.Fatalf("models = %+v", page.Models)
	}
	if page.Models[0].ID != "deepseek/deepseek-v4-flash" || page.Models[0].Name != "DeepSeek V4 Flash (latest)" {
		t.Fatalf("entry 0 = %+v", page.Models[0])
	}
	if got := page.Models[0].ContextWindow; got != 1000000 {
		t.Fatalf("context window = %d, want 1000000", got)
	}
	if got := page.Models[1].ContextWindow; got != 0 {
		t.Fatalf("missing context_length must stay 0, got %d", got)
	}
}
