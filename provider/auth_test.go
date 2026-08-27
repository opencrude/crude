package provider

import (
	"testing"
)

func TestAuthHeadersOmitEmptyKey(t *testing.T) {
	std := &AnthropicStandard{}
	if hdr := std.AuthHeaders(""); hdr.Get("x-api-key") != "" {
		t.Fatalf("empty key must yield no auth header, got %q", hdr.Get("x-api-key"))
	}
	if got := std.AuthHeaders("sk-ant").Get("x-api-key"); got != "sk-ant" {
		t.Fatalf("auth header = %q, want sk-ant", got)
	}
}

func TestHeaderSourceSkipsEmptyAuthHeader(t *testing.T) {
	p := FromPreset("ollama", "", "")
	// The header source used by every request must not emit an Authorization
	// header when the key is empty — keyless local endpoints (Ollama, LM
	// Studio) reject "Bearer " with 401/403.
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatalf("headerSource: %v", err)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("keyless provider auth = %q, want empty header", got)
	}
}

func TestHeaderSourceFallsBackToBearer(t *testing.T) {
	// The OpenAI protocol has no ProtocolAuth capability, so the header
	// source must apply the default Bearer scheme itself.
	p := FromPreset("openai", "sk-test", "")
	headers, err := p.RequestHeaders(t.Context())
	if err != nil {
		t.Fatalf("headerSource: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("auth header = %q, want Bearer sk-test", got)
	}
}

func TestValidateAllowsKeylessProviders(t *testing.T) {
	p := FromPreset("ollama", "", "http://localhost:11434/v1")
	if err := p.Validate(); err != nil {
		t.Fatalf("keyless provider must validate: %v", err)
	}
}
