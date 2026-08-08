package provider

import (
	"testing"
)

func TestOpenAIStandardAuthOmitsEmptyKey(t *testing.T) {
	std := &OpenAIStandard{}
	if hdr, val := std.Auth(""); hdr != "" || val != "" {
		t.Fatalf("empty key must yield no auth header, got %q=%q", hdr, val)
	}
	hdr, val := std.Auth("sk-test")
	if hdr != "Authorization" || val != "Bearer sk-test" {
		t.Fatalf("auth header=%q %q, want Authorization: Bearer sk-test", hdr, val)
	}
}

func TestFetchModelsSkipsEmptyAuthHeader(t *testing.T) {
	p := FromPreset("ollama", "", "")
	// The Provider.Auth path used by FetchModels must not emit a header
	// when the key is empty — keyless local endpoints (Ollama, LM Studio)
	// reject "Bearer " with 401/403.
	hdr, val := p.Auth()
	if hdr != "" || val != "" {
		t.Fatalf("keyless provider auth=%q %q, want empty header", hdr, val)
	}
}

func TestValidateAllowsKeylessProviders(t *testing.T) {
	p := FromPreset("ollama", "", "http://localhost:11434/v1")
	if err := p.Validate(); err != nil {
		t.Fatalf("keyless provider must validate: %v", err)
	}
}
