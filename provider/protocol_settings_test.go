package provider

import (
	"encoding/json"
	"testing"
)

// --- responses ---

func TestResponsesExtrasDefaultsRequestStorageExplicitly(t *testing.T) {
	extras := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "high", ProtocolSettings{})
	if got := extras["store"]; got != true {
		t.Fatalf("store = %v, want explicit true: %#v", got, extras)
	}
	if got := extras["reasoning"].(map[string]any)["summary"]; got != "auto" {
		t.Fatalf("summary = %v, want auto", got)
	}
}

func TestResponsesExtrasStoreOffAndZDR(t *testing.T) {
	off := false
	extras := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{Store: &off})
	if got := extras["store"]; got != false {
		t.Fatalf("store = %v, want false", got)
	}
	if _, ok := extras["include"]; ok {
		t.Fatalf("include without ZDR: %#v", extras)
	}

	zdr := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{ZeroDataRetention: true})
	if got := zdr["store"]; got != false {
		t.Fatalf("zdr store = %v, want false", got)
	}
	include, ok := zdr["include"].([]string)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("zdr include = %#v", zdr["include"])
	}

	// ZDR overrides an explicit Store=true.
	storeOn := true
	override := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{Store: &storeOn, ZeroDataRetention: true})
	if got := override["store"]; got != false {
		t.Fatalf("zdr overrides store=true: %v", got)
	}
}

func TestResponsesExtrasTruncationAndTier(t *testing.T) {
	extras := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{
		Truncation:  boolPtr(true),
		ServiceTier: "flex",
	})
	if got := extras["truncation"]; got != "auto" {
		t.Fatalf("truncation = %v, want auto", got)
	}
	if got := extras["service_tier"]; got != "flex" {
		t.Fatalf("service_tier = %v, want flex", got)
	}

	// Omitted toggles stay omitted.
	plain := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{ServiceTier: "flex"})
	if _, ok := plain["truncation"]; ok {
		t.Fatalf("truncation sent when unset: %#v", plain)
	}
}

func TestResponsesExtrasSummariesOff(t *testing.T) {
	summaries := false
	extras := (&OpenAIResponsesStandard{}).RequestExtras("gpt-5.6", "high", ProtocolSettings{ReasoningSummaries: &summaries})
	reasoning := extras["reasoning"].(map[string]any)
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("summary sent when disabled: %#v", reasoning)
	}
	if got := reasoning["effort"]; got != "high" {
		t.Fatalf("effort = %v, want high", got)
	}
}

// --- chat completions ---

func TestChatExtrasStoreOptInAndTier(t *testing.T) {
	extras := (&OpenAIStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{})
	if len(extras) != 0 {
		t.Fatalf("default extras = %#v, want empty", extras)
	}
	storeOn := true
	extras = (&OpenAIStandard{}).RequestExtras("gpt-5.6", "", ProtocolSettings{Store: &storeOn, ServiceTier: "priority"})
	if got := extras["store"]; got != true {
		t.Fatalf("store = %v, want true", got)
	}
	if got := extras["service_tier"]; got != "priority" {
		t.Fatalf("service_tier = %v, want priority", got)
	}
}

// --- capability declarations ---

func TestProtocolOptionsMatchImplementations(t *testing.T) {
	responses := (&OpenAIResponsesStandard{}).Info().Options
	if !responses.Store || !responses.StoreDefault || !responses.ZeroDataRetention ||
		!responses.ReasoningSummaries || !responses.Truncation || len(responses.ServiceTiers) == 0 {
		t.Fatalf("responses options = %+v", responses)
	}
	chat := (&OpenAIStandard{}).Info().Options
	if !chat.Store || chat.StoreDefault || chat.ZeroDataRetention || len(chat.ServiceTiers) == 0 {
		t.Fatalf("chat options = %+v", chat)
	}
	anthropic := (&AnthropicStandard{}).Info().Options
	if anthropic.Store || anthropic.ZeroDataRetention || len(anthropic.ServiceTiers) == 0 {
		t.Fatalf("anthropic options = %+v", anthropic)
	}
	ollama := (&OllamaStandard{}).Info().Options
	if ollama.Store || ollama.ZeroDataRetention || len(ollama.ServiceTiers) != 0 {
		t.Fatalf("ollama options = %+v", ollama)
	}
	codex := (&CodexStandard{}).Info().Options
	if codex.Store || codex.ZeroDataRetention || !codex.ReasoningSummaries {
		t.Fatalf("codex options = %+v", codex)
	}
}

// TestCodexForcesStoreOff pins the backend contract: even with Store
// enabled the codex envelope keeps store=false.
func TestCodexForcesStoreOff(t *testing.T) {
	storeOn := true
	p := &Provider{ID: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}
	if err := p.SetProtocolID("codex"); err != nil {
		t.Fatalf("SetProtocolID: %v", err)
	}
	p.SetProtocolSettings(&ProtocolSettings{Store: &storeOn})
	body, err := p.Protocol().EncodeRequest(&ChatRequest{
		Model:    "gpt-5.6",
		Messages: []ChatMessage{NewTextMessage("system", "s"), NewTextMessage("user", "hi")},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := wire["store"]; got != false {
		t.Fatalf("codex store = %v, want false", got)
	}
	if got := wire["stream"]; got != true {
		t.Fatalf("codex stream = %v, want true", got)
	}
}

// --- sampling overrides per protocol wire shape ---

func TestSamplingOverridesWireShapes(t *testing.T) {
	temp, topP := 0.3, 0.9
	req := &ChatRequest{
		Model:       "m",
		Messages:    []ChatMessage{NewTextMessage("user", "hi")},
		Temperature: &temp,
		TopP:        &topP,
	}

	chatWire := encodeToMap(t, &OpenAIStandard{}, req)
	if got := chatWire["temperature"]; got != 0.3 || chatWire["top_p"] != 0.9 {
		t.Fatalf("chat sampling = %v %v", chatWire["temperature"], chatWire["top_p"])
	}

	responsesWire := encodeToMap(t, &OpenAIResponsesStandard{}, req)
	if got := responsesWire["temperature"]; got != 0.3 || responsesWire["top_p"] != 0.9 {
		t.Fatalf("responses sampling = %v %v", responsesWire["temperature"], responsesWire["top_p"])
	}

	anthropicWire := encodeToMap(t, &AnthropicStandard{}, req)
	if got := anthropicWire["temperature"]; got != 0.3 {
		t.Fatalf("anthropic temperature = %v", got)
	}
	if _, ok := anthropicWire["top_p"]; ok {
		t.Fatalf("anthropic top_p sent (unsupported sampling knob): %v", anthropicWire["top_p"])
	}

	ollamaWire := encodeToMap(t, &OllamaStandard{}, req)
	options := ollamaWire["options"].(map[string]any)
	if got := options["temperature"]; got != 0.3 || options["top_p"] != 0.9 {
		t.Fatalf("ollama options = %v", options)
	}
}

func TestSamplingOmittedWhenNil(t *testing.T) {
	req := &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}}
	for name, proto := range map[string]Protocol{
		"chat":      &OpenAIStandard{},
		"responses": &OpenAIResponsesStandard{},
		"anthropic": &AnthropicStandard{},
		"ollama":    &OllamaStandard{},
	} {
		wire := encodeToMap(t, proto, req)
		if _, ok := wire["temperature"]; ok {
			t.Errorf("%s sent temperature when unset", name)
		}
		if _, ok := wire["top_p"]; ok {
			t.Errorf("%s sent top_p when unset", name)
		}
	}
}

func encodeToMap(t *testing.T, proto Protocol, req *ChatRequest) map[string]any {
	t.Helper()
	body, err := proto.EncodeRequest(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return wire
}

func boolPtr(v bool) *bool { return &v }
