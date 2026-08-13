package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchModelsPaginatesAnthropic(t *testing.T) {
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
	models, err := FetchModels(p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
	if len(models) != 2 || models[0].ID != "claude-a" || models[1].ID != "claude-b" {
		t.Fatalf("models = %+v", models)
	}
}
