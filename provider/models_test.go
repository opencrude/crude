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
