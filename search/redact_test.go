package search

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactRequestURLHidesAPIKey(t *testing.T) {
	err := redactRequestURL(&url.Error{Op: "Post", URL: "https://mcp.exa.ai/mcp?exaApiKey=sk-secret", Err: errors.New("dial tcp: timeout")})
	if strings.Contains(err.Error(), "sk-secret") || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error leaked the key or lost its cause: %v", err)
	}
	plain := errors.New("other")
	if redactRequestURL(plain) != plain {
		t.Fatal("non-URL errors must pass through unchanged")
	}
}
