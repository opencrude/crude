package provider

import "testing"

// The tier a provider reports having served must reach accounting: Fast mode
// bills above the standard rate and can be silently downgraded to "default".
func TestChatStreamReportsServedServiceTier(t *testing.T) {
	decoder := &openAIChatStreamDecoder{}
	update, err := decoder.Decode(StreamEvent{Data: []byte(`{"id":"x","service_tier":"default","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if update.Usage == nil || update.Usage.ServiceTier != "default" {
		t.Fatalf("served tier lost: %+v", update.Usage)
	}
}

func TestResponsesReportsServedServiceTier(t *testing.T) {
	response := openAIResponse{ID: "resp", Status: "completed", ServiceTier: "priority", Usage: &openAIResponsesUsage{InputTokens: 10, OutputTokens: 2}}
	chat, err := response.toChatResponse()
	if err != nil {
		t.Fatal(err)
	}
	if chat.ServiceTier != "priority" {
		t.Fatalf("served tier lost: %q", chat.ServiceTier)
	}
}

func TestRequestPriceKnownNeverUnderstatesPremiumTiers(t *testing.T) {
	SetModelPrice("tier-test-model", ModelPrice{InputPricePerToken: 1e-6, OutputPricePerToken: 2e-6})
	fast := &Provider{}
	fast.SetProtocolSettings(&ProtocolSettings{ServiceTier: "fast"})
	if _, known, source := fast.RequestPriceKnown("tier-test-model", ""); known || source == "catalog" {
		t.Fatalf("fast request priced at the standard rate: known=%t source=%q", known, source)
	}
	if _, known, _ := fast.RequestPriceKnown("tier-test-model", "default"); !known {
		t.Fatal("a fast request served at the default tier is billed at the standard rate")
	}
	standard := &Provider{}
	if _, known, _ := standard.RequestPriceKnown("tier-test-model", ""); !known {
		t.Fatal("standard request lost its catalog price")
	}
	if _, known, _ := standard.RequestPriceKnown("tier-test-model", "priority"); known {
		t.Fatal("a request the provider reports as priority must not use the standard price")
	}
}

func TestRoleServiceTierMapsToOfferedProtocolTiers(t *testing.T) {
	openai := FromPreset("openai-chat", "key", "")
	openai.SetServiceTier("fast")
	if settings := openai.ProtocolSettings(); settings == nil || settings.ServiceTier != "fast" {
		t.Fatalf("openai fast tier=%+v", settings)
	}
	openai.SetServiceTier("standard")
	if openai.ProtocolSettings().ServiceTier != "default" {
		t.Fatalf("openai standard tier=%q", openai.ProtocolSettings().ServiceTier)
	}
	anthropic := FromPreset("anthropic", "key", "")
	before := anthropic.ProtocolSettings()
	anthropic.SetServiceTier("fast")
	if after := anthropic.ProtocolSettings(); (before == nil) != (after == nil) || after != nil && after.ServiceTier != before.ServiceTier {
		t.Fatalf("unsupported tier changed the account setting: %+v -> %+v", before, after)
	}
	anthropic.SetServiceTier("standard")
	if anthropic.ProtocolSettings() == nil || anthropic.ProtocolSettings().ServiceTier != "standard_only" {
		t.Fatalf("anthropic standard tier=%+v", anthropic.ProtocolSettings())
	}
	anthropic.SetServiceTier("bogus")
	if anthropic.ProtocolSettings().ServiceTier != "standard_only" {
		t.Fatal("unknown tier changed the setting")
	}
}
